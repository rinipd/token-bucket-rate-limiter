package limiter

import "sync"

// DefaultConfig is applied to a client that calls Allow before any config
// has been set for it via SetConfig.
var DefaultConfig = Config{RPS: 1, Burst: 5, Algorithm: AlgorithmTokenBucket}

// Manager manages one TokenBucket per client plus each client's rate-limit
// Config, creating buckets lazily on first use.
//
// Manager is safe for concurrent use: mu protects the configs and buckets
// maps (the get-or-create/get-or-set steps), while rate limiting for an
// individual client is serialized by that client's own TokenBucket.mu. This
// split means requests for different clients don't block each other — only
// concurrent requests for the same client serialize, on that bucket's mutex.
type Manager struct {
	mu      sync.Mutex // protects configs and buckets (the maps, not the buckets' contents)
	configs map[string]Config
	buckets map[string]*TokenBucket
}

// NewManager returns an empty Manager ready to track client configs and
// buckets.
func NewManager() *Manager {
	return &Manager{
		configs: make(map[string]Config),
		buckets: make(map[string]*TokenBucket),
	}
}

// SetConfig validates cfg and stores it as clientID's rate-limit
// configuration. If clientID already has a bucket, it is discarded so the
// next Allow call recreates it under the new limits — otherwise the client
// would keep being rate-limited by its old capacity/refill rate until the
// bucket happened to be recreated some other way.
func (m *Manager) SetConfig(clientID string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[clientID] = cfg
	delete(m.buckets, clientID)
	return nil
}

// GetConfig returns clientID's stored config, if any.
func (m *Manager) GetConfig(clientID string) (Config, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.configs[clientID]
	return cfg, ok
}

// RemoveClient deletes clientID's config and bucket, if any. A subsequent
// Allow call for clientID starts over under DefaultConfig.
func (m *Manager) RemoveClient(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, clientID)
	delete(m.buckets, clientID)
}

// Allow reports the outcome of a request for clientID, consuming a token
// from that client's bucket if the request is allowed. The client's rate
// limit comes from its stored Config (or DefaultConfig if none was set via
// SetConfig). If clientID has no bucket yet, one is created from that config
// and stored for future calls.
func (m *Manager) Allow(clientID string) Decision {
	// Hold m.mu only for the get-or-create of the config and bucket: this is
	// the part that touches shared state (the maps), so it must be atomic or
	// two goroutines could both see "absent" and create/store two different
	// buckets for the same new client.
	m.mu.Lock()
	cfg, ok := m.configs[clientID]
	if !ok {
		cfg = DefaultConfig
	}

	b, ok := m.buckets[clientID]
	if !ok {
		b = NewTokenBucket(cfg.Burst, cfg.RPS)
		m.buckets[clientID] = b
	}
	m.mu.Unlock()

	// Release the manager lock before calling into the bucket: Allow() can
	// take a while (or block on the bucket's own mutex under contention), and
	// the bucket's state is already protected by its own mutex, so holding
	// m.mu here would only add unnecessary contention across unrelated
	// clients.
	return b.Allow()
}

// Snapshot captures the current configs and bucket states as a
// PersistedState, suitable for writing to disk (see the store package).
//
// Lock ordering: m.mu is held only long enough to copy the configs map and
// collect the *TokenBucket pointers, then released before calling each
// bucket's State(). Bucket.State() takes that bucket's own mutex, so calling
// it while still holding m.mu would mean acquiring two locks at once — for
// no benefit, since the buckets map itself isn't touched again afterward,
// and it would block unrelated Allow calls (which briefly need m.mu) for the
// entire duration of every bucket read instead of just the map copy.
func (m *Manager) Snapshot() PersistedState {
	m.mu.Lock()
	configs := make(map[string]Config, len(m.configs))
	for id, cfg := range m.configs {
		configs[id] = cfg
	}
	buckets := make(map[string]*TokenBucket, len(m.buckets))
	for id, b := range m.buckets {
		buckets[id] = b
	}
	m.mu.Unlock()

	bucketStates := make(map[string]BucketState, len(buckets))
	for id, b := range buckets {
		bucketStates[id] = b.State()
	}

	return PersistedState{
		Configs: configs,
		Buckets: bucketStates,
	}
}

// Restore replaces m's configs and buckets with those captured in s,
// reconstructing a live *TokenBucket for each persisted BucketState.
func (m *Manager) Restore(s PersistedState) {
	configs := make(map[string]Config, len(s.Configs))
	for id, cfg := range s.Configs {
		configs[id] = cfg
	}
	buckets := make(map[string]*TokenBucket, len(s.Buckets))
	for id, bs := range s.Buckets {
		buckets[id] = NewTokenBucketFromState(bs)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs = configs
	m.buckets = buckets
}
