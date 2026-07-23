package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestManager_ConcurrentSameClient hammers a single client's bucket from many
// goroutines at once. Manager is deliberately unsynchronized (see manager.go),
// so this test demonstrates the resulting data race: concurrent goroutines
// can race on both the lazy bucket-creation check and the bucket's own
// internal state.
//
// With refillRate 0, no tokens are ever added back, so a correctly
// synchronized limiter would allow exactly `capacity` (100) of the 1000
// concurrent requests. Under the current unsynchronized implementation this
// assertion may fail, and `go test -race` should flag the race directly —
// both outcomes are the expected result of this step, not a bug to fix here.
func TestManager_ConcurrentSameClient(t *testing.T) {
	const (
		clientID   = "client-1"
		capacity   = 100
		refillRate = 0 // no refill during the test
		numCallers = 1000
	)

	m := NewManager()

	var wg sync.WaitGroup
	var allowedCount int64

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Allow(clientID, capacity, refillRate) {
				atomic.AddInt64(&allowedCount, 1)
			}
		}()
	}

	wg.Wait()

	if got := atomic.LoadInt64(&allowedCount); got != capacity {
		t.Fatalf("allowed %d requests, want exactly %d", got, capacity)
	}
}
