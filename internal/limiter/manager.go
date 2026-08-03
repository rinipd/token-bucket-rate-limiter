package limiter

import (
	"sync"
	"time"
)

// DefaultConfig is applied to a client that calls Allow before any config
// has been set for it via SetConfig.
var DefaultConfig = Config{RPS: 1, Burst: 5, Algorithm: AlgorithmTokenBucket}

// Compile-time assertion that *Manager satisfies Store: if a future change
// to either type breaks that, this line fails to compile instead of the
// mismatch surfacing later as a runtime type error somewhere Manager is
// passed as a Store.
var _ Store = (*Manager)(nil)

// Manager manages one Limiter per client plus each client's rate-limit
// Config, creating limiters lazily on first use.
//
// Manager is safe for concurrent use: mu protects the configs and limiters
// maps (the get-or-create/get-or-set steps), while rate limiting for an
// individual client is serialized by that client's own limiter's mutex
// (TokenBucket.mu or SlidingWindow.mu, depending on the algorithm). This
// split means requests for different clients don't block each other — only
// concurrent requests for the same client serialize, on that limiter's mutex.
type Manager struct {
	mu       sync.Mutex // protects configs and limiters (the maps, not their contents)
	configs  map[string]Config
	limiters map[string]Limiter
}

// NewManager returns an empty Manager ready to track client configs and
// limiters.
func NewManager() *Manager {
	return &Manager{
		configs:  make(map[string]Config),
		limiters: make(map[string]Limiter),
	}
}

// newLimiter builds the concrete Limiter matching cfg.Algorithm.
//
// Both algorithms are driven from the same Config fields (RPS, Burst) so a
// client's limits stay roughly comparable across algorithms:
//   - token_bucket: capacity Burst, refilling continuously at RPS tokens/sec.
//   - sliding_window: limit Burst requests per window, where
//     window = Burst/RPS seconds. Burst requests every Burst/RPS seconds
//     works out to a sustained rate of RPS/sec — matching token_bucket's
//     steady-state rate — with Burst as the max burst size for both. This is
//     a deliberate design choice for comparability, not a mathematical law.
func newLimiter(cfg Config) Limiter {
	if cfg.Algorithm == AlgorithmSlidingWindow {
		window := time.Duration(cfg.Burst / cfg.RPS * float64(time.Second))
		return NewSlidingWindow(cfg.Burst, window)
	}
	return NewTokenBucket(cfg.Burst, cfg.RPS)
}

// SetConfig validates cfg and stores it as clientID's rate-limit
// configuration. If clientID already has a limiter, it is discarded so the
// next Allow call recreates it under the new limits — otherwise the client
// would keep being rate-limited by its old limits (or even its old
// algorithm, if that changed) until the limiter happened to be recreated
// some other way.
func (m *Manager) SetConfig(clientID string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[clientID] = cfg
	delete(m.limiters, clientID)
	return nil
}

// GetConfig returns clientID's stored config, if any.
func (m *Manager) GetConfig(clientID string) (Config, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.configs[clientID]
	return cfg, ok
}

// RemoveClient deletes clientID's config and limiter, if any. A subsequent
// Allow call for clientID starts over under DefaultConfig.
func (m *Manager) RemoveClient(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, clientID)
	delete(m.limiters, clientID)
}

// Allow reports the outcome of a request for clientID, consuming capacity
// from that client's limiter if the request is allowed. The client's rate
// limit and algorithm come from its stored Config (or DefaultConfig if none
// was set via SetConfig). If clientID has no limiter yet, one is created via
// newLimiter and stored for future calls.
func (m *Manager) Allow(clientID string) Decision {
	// Hold m.mu only for the get-or-create of the config and limiter: this is
	// the part that touches shared state (the maps), so it must be atomic or
	// two goroutines could both see "absent" and create/store two different
	// limiters for the same new client.
	m.mu.Lock()
	cfg, ok := m.configs[clientID]
	if !ok {
		cfg = DefaultConfig
	}

	lim, ok := m.limiters[clientID]
	if !ok {
		lim = newLimiter(cfg)
		m.limiters[clientID] = lim
	}
	m.mu.Unlock()

	// Release the manager lock before calling into the limiter: Allow() can
	// take a while (or block on the limiter's own mutex under contention),
	// and the limiter's state is already protected by its own mutex, so
	// holding m.mu here would only add unnecessary contention across
	// unrelated clients.
	return lim.Allow()
}

// Snapshot captures the current configs and limiter states as a
// PersistedState, suitable for writing to disk (see the store package).
//
// Lock ordering: m.mu is held only long enough to copy the configs map and
// collect the Limiter values, then released before calling each limiter's
// State(). Each concrete limiter's State() takes that limiter's own mutex,
// so calling it while still holding m.mu would mean acquiring two locks at
// once — for no benefit, since the limiters map itself isn't touched again
// afterward, and it would block unrelated Allow calls (which briefly need
// m.mu) for the entire duration of every limiter read instead of just the
// map copy.
func (m *Manager) Snapshot() PersistedState {
	m.mu.Lock()
	configs := make(map[string]Config, len(m.configs))
	for id, cfg := range m.configs {
		configs[id] = cfg
	}
	limiters := make(map[string]Limiter, len(m.limiters))
	for id, lim := range m.limiters {
		limiters[id] = lim
	}
	m.mu.Unlock()

	limiterStates := make(map[string]LimiterState, len(limiters))
	for id, lim := range limiters {
		// Type-switch on the concrete algorithm to build the matching
		// discriminated LimiterState, tagging it with the Algorithm that
		// Restore will later switch on to pick the right FromState
		// constructor.
		switch l := lim.(type) {
		case *TokenBucket:
			state := l.State()
			limiterStates[id] = LimiterState{Algorithm: AlgorithmTokenBucket, TokenBucket: &state}
		case *SlidingWindow:
			state := l.State()
			limiterStates[id] = LimiterState{Algorithm: AlgorithmSlidingWindow, SlidingWindow: &state}
		}
	}

	return PersistedState{
		Configs:  configs,
		Limiters: limiterStates,
	}
}

// Restore replaces m's configs and limiters with those captured in s,
// reconstructing the correct concrete Limiter for each persisted
// LimiterState by switching on its Algorithm tag.
func (m *Manager) Restore(s PersistedState) {
	configs := make(map[string]Config, len(s.Configs))
	for id, cfg := range s.Configs {
		configs[id] = cfg
	}
	limiters := make(map[string]Limiter, len(s.Limiters))
	for id, ls := range s.Limiters {
		switch {
		case ls.Algorithm == AlgorithmSlidingWindow && ls.SlidingWindow != nil:
			limiters[id] = NewSlidingWindowFromState(*ls.SlidingWindow)
		case ls.Algorithm == AlgorithmTokenBucket && ls.TokenBucket != nil:
			limiters[id] = NewTokenBucketFromState(*ls.TokenBucket)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs = configs
	m.limiters = limiters
}
