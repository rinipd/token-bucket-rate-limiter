package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestManager_ConcurrentSameClient hammers a single client's bucket from many
// goroutines at once, exercising Manager's two-level locking (map access
// serialized by Manager.mu, token accounting serialized by the bucket's own
// mutex).
//
// With a vanishingly small RPS (see noRefillRPS below), effectively no tokens
// are added back during the test, so exactly `capacity` (100) of the 1000
// concurrent requests should be allowed.
func TestManager_ConcurrentSameClient(t *testing.T) {
	const (
		clientID   = "client-1"
		capacity   = 100
		numCallers = 1000
	)

	m := NewManager()
	// Validate() rejects RPS <= 0, so use a vanishingly small (but positive)
	// RPS: over the test's runtime this adds a negligible fraction of a
	// token, which is indistinguishable from "no refill" for this assertion.
	const noRefillRPS = 1e-9
	if err := m.SetConfig(clientID, Config{RPS: noRefillRPS, Burst: capacity, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	var wg sync.WaitGroup
	var allowedCount int64

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Allow(clientID).Allowed {
				atomic.AddInt64(&allowedCount, 1)
			}
		}()
	}

	wg.Wait()

	if got := atomic.LoadInt64(&allowedCount); got != capacity {
		t.Fatalf("allowed %d requests, want exactly %d", got, capacity)
	}
}
