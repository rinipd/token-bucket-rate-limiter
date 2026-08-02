package limiter

import (
	"math"
	"sync"
	"time"
)

// SlidingWindow is a sliding-window-counter rate limiter: an approximation
// of a true sliding log that needs only two counters (not a timestamp per
// request) while avoiding the flaw of a naive fixed window.
//
// The fixed-window flaw: a limiter that just counts requests per calendar
// window and resets the counter at each boundary lets a client burst up to
// the limit right before a boundary and again right after it — e.g. at
// 100/min, 100 requests at 12:00:59 followed by 100 more at 12:01:00 is 200
// requests in under a second, because the counter happened to reset between
// them. Sliding window fixes this by never fully discarding the previous
// window's count: it fades that count out smoothly as the current window
// progresses, so a burst that just filled the previous window is still
// "remembered" (and counted against the limit) for a while into the next
// one.
//
// The estimate, recomputed on every Allow:
//
//	weight    = (window - elapsedInCurrentWindow) / window   // 1 → 0 as the window progresses
//	estimated = previousCount*weight + currentCount
//
// If estimated is still below the limit, the request is allowed and
// currentCount is incremented; otherwise it's denied. Right after a window
// boundary, weight is still close to 1, so a previous-window burst continues
// to count almost fully against the limit — this is what blocks the
// double-burst that a fixed window would allow.
type SlidingWindow struct {
	mu sync.Mutex // protects everything below

	limit  float64       // max requests allowed per window
	window time.Duration // window size

	windowStart   time.Time // start of the current fixed window
	currentCount  float64   // requests counted in the current window so far
	previousCount float64   // requests counted in the immediately preceding window

	// now is an injectable clock, same pattern as TokenBucket: production
	// code gets the real time.Now, tests substitute a fake clock they fully
	// control so window-rolling and boundary behavior can be exercised
	// deterministically without real sleeps.
	now func() time.Time
}

// NewSlidingWindow returns a SlidingWindow that allows up to limit requests
// per window, starting with an empty current window anchored to now.
func NewSlidingWindow(limit float64, window time.Duration) *SlidingWindow {
	now := time.Now
	return &SlidingWindow{
		limit:       limit,
		window:      window,
		windowStart: now(),
		now:         now,
	}
}

// Allow reports the outcome of a request against this sliding window,
// counting it in the current window if allowed. See Decision for the
// meaning of each field, and the RetryAfter/ResetAfter note below for how
// they differ from TokenBucket's.
func (s *SlidingWindow) Allow() Decision {
	// Lock for the whole roll-estimate-decide sequence so it executes as one
	// atomic unit, for the same reason as TokenBucket.Allow: concurrent
	// callers must not interleave reads/writes of the window state.
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	elapsed := now.Sub(s.windowStart)

	switch {
	case elapsed >= 2*s.window:
		// A long idle gap: even the "previous" window is entirely stale (more
		// than one full window old), so there's nothing left to fade out —
		// start over with a clean window anchored to now.
		s.currentCount = 0
		s.previousCount = 0
		s.windowStart = now
	case elapsed >= s.window:
		// We've crossed exactly one window boundary since the last Allow:
		// the current window becomes the previous one (to be faded out) and
		// we start a new, empty current window right after it — not at now,
		// so windowStart stays aligned to fixed-size windows rather than
		// drifting with each request.
		s.previousCount = s.currentCount
		s.currentCount = 0
		s.windowStart = s.windowStart.Add(s.window)
	}

	// elapsedInCurrent is always in [0, window) here: the switch above rolled
	// windowStart forward until now falls within the current window.
	elapsedInCurrent := now.Sub(s.windowStart)
	weight := float64(s.window-elapsedInCurrent) / float64(s.window)
	estimated := s.previousCount*weight + s.currentCount

	allowed := estimated < s.limit
	var consumed float64
	if allowed {
		s.currentCount++
		consumed = 1
	}

	// RetryAfter/ResetAfter are approximations for sliding window, unlike
	// TokenBucket's exact values: because the estimate decays continuously as
	// the window slides, there's no single instant at which "you're now
	// under the limit" in general (it depends on how much of the count is in
	// the fading previous window vs. the growing current one). We use the
	// end of the current window as a safe, easy-to-compute approximation —
	// by then the previous window's contribution has fully faded (weight
	// reaches 0), which is always safe (the client may actually be allowed
	// sooner) but never optimistic (it never claims a shorter wait than the
	// window can guarantee).
	resetAfter := s.window - elapsedInCurrent
	var retryAfter time.Duration
	if !allowed {
		retryAfter = resetAfter
	}

	return Decision{
		Allowed:    allowed,
		Limit:      s.limit,
		Remaining:  math.Max(0, math.Floor(s.limit-(estimated+consumed))),
		RetryAfter: retryAfter,
		ResetAfter: resetAfter,
	}
}
