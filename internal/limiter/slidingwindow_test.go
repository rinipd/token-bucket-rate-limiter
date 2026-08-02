package limiter

import (
	"testing"
	"time"
)

// newTestSlidingWindow builds a SlidingWindow wired to a caller-controlled
// fake clock, mirroring newTestBucket's pattern in tokenbucket_test.go. The
// returned advance function moves the fake clock forward so tests can
// exercise window-rolling and boundary behavior deterministically, without
// real sleeps.
func newTestSlidingWindow(limit float64, window time.Duration) (s *SlidingWindow, advance func(time.Duration)) {
	current := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	s = &SlidingWindow{
		limit:       limit,
		window:      window,
		windowStart: current,
		now:         func() time.Time { return current },
	}
	advance = func(d time.Duration) { current = current.Add(d) }
	return s, advance
}

// TestSlidingWindow_BurstThenDeny verifies that, back-to-back with no time
// advancing, a window serves exactly `limit` requests and then denies the
// next one — the same basic burst-capacity guarantee TokenBucket provides,
// just measured per fixed window instead of via continuous refill.
func TestSlidingWindow_BurstThenDeny(t *testing.T) {
	tests := []struct {
		name   string
		limit  float64
		window time.Duration
	}{
		{name: "limit 1", limit: 1, window: time.Second},
		{name: "limit 5", limit: 5, window: time.Second},
		{name: "limit 10", limit: 10, window: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestSlidingWindow(tt.limit, tt.window)

			allowed := 0
			for i := 0; i < int(tt.limit); i++ {
				if !s.Allow().Allowed {
					t.Fatalf("request %d: got DENY, want ALLOW", i+1)
				}
				allowed++
			}
			if allowed != int(tt.limit) {
				t.Fatalf("burst allowed %d requests, want %d", allowed, int(tt.limit))
			}

			// The next request exceeds the limit and must be denied.
			if s.Allow().Allowed {
				t.Fatalf("request after burst: got ALLOW, want DENY")
			}
		})
	}
}

// TestSlidingWindow_SlideAllowsAgain verifies that once the fake clock moves
// well into the next window, the previous window's count has faded enough
// for new requests to be allowed again.
//
// We advance by 1.5x the window rather than exactly 1x: at precisely one
// window boundary, weight is still 1 (the previous window's count hasn't
// started fading at all yet), so a previous count equal to the limit would
// still deny the very next request — that exact-boundary behavior is the
// point of TestSlidingWindow_BoundaryAntiDoubleBurst below. Advancing
// further into window 2 gives the previous window's contribution room to
// decay before we check that capacity has opened back up.
func TestSlidingWindow_SlideAllowsAgain(t *testing.T) {
	const limit = 5
	const window = time.Second

	s, advance := newTestSlidingWindow(limit, window)

	// Exhaust the current window's burst.
	for i := 0; i < limit; i++ {
		if !s.Allow().Allowed {
			t.Fatalf("drain request %d: got DENY, want ALLOW", i+1)
		}
	}
	if s.Allow().Allowed {
		t.Fatalf("window should be exhausted but allowed a request")
	}

	// Move deep into the next window so the previous window's weight has
	// decayed well below 1.
	advance(window + window/2)

	if !s.Allow().Allowed {
		t.Fatalf("request after sliding past the window: got DENY, want ALLOW")
	}
}

// TestSlidingWindow_BoundaryAntiDoubleBurst is the key property that
// distinguishes sliding window from a naive fixed window: exhausting the
// limit late in one window and then crossing into the next window must NOT
// hand the client a full fresh burst immediately, because the previous
// window's (still heavily weighted) count keeps counting against the limit.
// A naive fixed-window counter would reset to 0 at the boundary and allow a
// full second burst; sliding window must allow strictly fewer than `limit`
// requests immediately after crossing.
func TestSlidingWindow_BoundaryAntiDoubleBurst(t *testing.T) {
	const limit = 5
	const window = time.Second

	s, advance := newTestSlidingWindow(limit, window)

	// Fill the burst late in window 1 (90% of the way through it), so the
	// requests are legitimately "recent" from the perspective of window 2.
	advance(900 * time.Millisecond)
	for i := 0; i < limit; i++ {
		if !s.Allow().Allowed {
			t.Fatalf("late-window drain request %d: got DENY, want ALLOW", i+1)
		}
	}

	// Cross just slightly past the window boundary into window 2.
	advance(150 * time.Millisecond)

	allowedAfterBoundary := 0
	for i := 0; i < limit; i++ {
		if s.Allow().Allowed {
			allowedAfterBoundary++
		}
	}

	if allowedAfterBoundary >= limit {
		t.Fatalf("allowed %d requests immediately after the boundary, want fewer than %d (double-burst not prevented)", allowedAfterBoundary, limit)
	}
}
