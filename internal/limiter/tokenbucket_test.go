package limiter

import (
	"testing"
	"time"
)

// newTestBucket builds a TokenBucket wired to a caller-controlled fake clock.
// The returned advance function moves the fake clock forward by a duration so
// tests can simulate the passage of time without real sleeps.
func newTestBucket(capacity, refillRate float64) (b *TokenBucket, advance func(time.Duration)) {
	// A fixed, arbitrary base time. Nothing here depends on the wall clock.
	current := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	b = &TokenBucket{
		tokens:     capacity,
		lastRefill: current,
		capacity:   capacity,
		refillRate: refillRate,
		now:        func() time.Time { return current },
	}
	advance = func(d time.Duration) { current = current.Add(d) }
	return b, advance
}

// TestAllow_BurstThenDeny verifies that a full bucket serves exactly `capacity`
// requests back-to-back (no time advancing) and then denies further requests.
func TestAllow_BurstThenDeny(t *testing.T) {
	tests := []struct {
		name       string
		capacity   float64
		refillRate float64
	}{
		{name: "capacity 1", capacity: 1, refillRate: 1},
		{name: "capacity 5", capacity: 5, refillRate: 2},
		{name: "capacity 10", capacity: 10, refillRate: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := newTestBucket(tt.capacity, tt.refillRate)

			// Without advancing the clock, exactly `capacity` requests should
			// be allowed (the initial full burst).
			allowed := 0
			for i := 0; i < int(tt.capacity); i++ {
				if !b.Allow().Allowed {
					t.Fatalf("request %d: got DENY, want ALLOW", i+1)
				}
				allowed++
			}

			if allowed != int(tt.capacity) {
				t.Fatalf("burst allowed %d requests, want %d", allowed, int(tt.capacity))
			}

			// The next request drains past the bucket and must be denied.
			if b.Allow().Allowed {
				t.Fatalf("request after burst: got ALLOW, want DENY")
			}
		})
	}
}

// TestAllow_RefillAfterTime verifies that once the fake clock advances far
// enough, previously-exhausted buckets allow requests again.
func TestAllow_RefillAfterTime(t *testing.T) {
	tests := []struct {
		name        string
		capacity    float64
		refillRate  float64
		advance     time.Duration
		wantAllowed int // requests expected to be allowed after advancing
	}{
		{
			name:        "one token refilled",
			capacity:    5,
			refillRate:  1, // 1 token/sec
			advance:     1 * time.Second,
			wantAllowed: 1,
		},
		{
			name:        "several tokens refilled",
			capacity:    10,
			refillRate:  2, // 2 tokens/sec
			advance:     2 * time.Second,
			wantAllowed: 4,
		},
		{
			name:        "refill capped at capacity",
			capacity:    3,
			refillRate:  1,
			advance:     1 * time.Hour, // way more than enough to overflow
			wantAllowed: 3,             // capped at capacity, not unbounded
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, advance := newTestBucket(tt.capacity, tt.refillRate)

			// Drain the initial full burst so the bucket is empty.
			for i := 0; i < int(tt.capacity); i++ {
				if !b.Allow().Allowed {
					t.Fatalf("drain request %d: got DENY, want ALLOW", i+1)
				}
			}
			if b.Allow().Allowed {
				t.Fatalf("bucket should be empty but allowed a request")
			}

			// Advance the fake clock; lazy refill happens on the next Allow.
			advance(tt.advance)

			// Count how many requests are allowed now.
			allowed := 0
			for b.Allow().Allowed {
				allowed++
			}

			if allowed != tt.wantAllowed {
				t.Fatalf("after advancing %v: allowed %d, want %d", tt.advance, allowed, tt.wantAllowed)
			}
		})
	}
}
