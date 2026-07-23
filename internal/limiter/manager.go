package limiter

import "sync"

// Manager manages one TokenBucket per client, creating buckets lazily on
// first use.
//
// Manager is safe for concurrent use: mu protects the buckets map itself
// (the get-or-create step), while rate limiting for an individual client is
// serialized by that client's own TokenBucket.mu. This split means requests
// for different clients don't block each other — only concurrent requests
// for the same client serialize, on that bucket's mutex.
type Manager struct {
	mu      sync.Mutex // protects buckets (the map, not the buckets' contents)
	buckets map[string]*TokenBucket
}

// NewManager returns an empty Manager ready to track client buckets.
func NewManager() *Manager {
	return &Manager{
		buckets: make(map[string]*TokenBucket),
	}
}

// Allow reports whether a request for clientID may proceed, consuming a
// token from that client's bucket. If clientID has no bucket yet, one is
// created with the given capacity and refillRate and stored for future
// calls.
func (m *Manager) Allow(clientID string, capacity, refillRate float64) bool {
	// Hold m.mu only for the get-or-create: this is the part that touches
	// shared state (the map), so it must be atomic or two goroutines could
	// both see "absent" and create/store two different buckets for the same
	// new client.
	m.mu.Lock()
	b, ok := m.buckets[clientID]
	if !ok {
		b = NewTokenBucket(capacity, refillRate)
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
