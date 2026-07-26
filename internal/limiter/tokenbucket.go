package limiter

import (
	"math"
	"sync"
	"time"
)

// TokenBucket is a single-bucket rate limiter using the token bucket algorithm.
//
// Conceptually the bucket holds up to `capacity` tokens. Each allowed request
// consumes one token. Tokens are replenished continuously at `refillRate`
// tokens per second, up to the capacity (the "burst" size).
//
// This implementation uses *lazy* refill: instead of a background goroutine
// topping up the bucket on a timer, we compute how many tokens should have
// accrued since the last refill at the moment a request arrives. This keeps
// the type free of goroutines and timers.
//
// TokenBucket is safe for concurrent use: mu serializes the refill-check-
// consume sequence in Allow so concurrent callers can't observe or clobber
// each other's partial updates to tokens/lastRefill.
type TokenBucket struct {
	mu sync.Mutex // protects tokens and lastRefill below

	tokens     float64 // current number of tokens available (fractional)
	lastRefill time.Time
	capacity   float64 // maximum tokens the bucket can hold (max burst size)
	refillRate float64 // tokens added per second

	// now is an injectable clock. In production it points at time.Now, but
	// tests can substitute a fake clock they fully control. This lets us
	// exercise time-dependent behavior (elapsed-time refill) deterministically
	// without real sleeps.
	now func() time.Time
}

// NewTokenBucket returns a bucket that starts full (tokens == capacity), so the
// first `capacity` requests can be served as an immediate burst. The clock is
// wired to the real time.Now and last-refill is anchored to the current time.
func NewTokenBucket(capacity, refillRate float64) *TokenBucket {
	now := time.Now
	return &TokenBucket{
		tokens:     capacity,
		lastRefill: now(),
		capacity:   capacity,
		refillRate: refillRate,
		now:        now,
	}
}

// Allow reports the outcome of a request against this bucket, consuming a
// token if one is available. See Decision for the meaning of each field.
//
// Lazy-refill math, performed on every call:
//
//	elapsed := now - lastRefill          // seconds since we last refilled
//	tokens  += elapsed * refillRate      // tokens that accrued in that window
//	tokens   = min(tokens, capacity)     // never exceed the burst capacity
//	lastRefill = now                     // advance the refill anchor
//
// After refilling, if at least one whole token is available we subtract one and
// ALLOW the request; otherwise we leave the token count untouched and DENY.
func (b *TokenBucket) Allow() Decision {
	// Lock for the whole refill-check-consume-and-compute-Decision sequence
	// so it executes as one atomic unit: the Decision fields are derived from
	// b.tokens, which concurrent callers would otherwise be free to mutate
	// between our read and our computation, producing a Decision that
	// doesn't match what was actually consumed.
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	// Add the tokens that have accrued since the last refill, capped at
	// capacity so the bucket can't overflow past its burst size.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now

	// Consume a token if one is available.
	allowed := b.tokens >= 1
	if allowed {
		b.tokens--
	}

	return Decision{
		Allowed:    allowed,
		Limit:      b.capacity,
		Remaining:  math.Floor(b.tokens),
		RetryAfter: b.retryAfter(allowed),
		ResetAfter: b.resetAfter(),
	}
}

// retryAfter returns how long the caller should wait before at least one
// token is available. It's 0 when the request was allowed (no need to
// retry); otherwise it's the time for the current token count to reach 1.
func (b *TokenBucket) retryAfter(allowed bool) time.Duration {
	if allowed {
		return 0
	}
	if b.refillRate <= 0 {
		// A bucket that never refills will never have another token to give;
		// see neverRefills.
		return neverRefills
	}
	seconds := (1 - b.tokens) / b.refillRate
	return time.Duration(seconds * float64(time.Second))
}

// resetAfter returns how long until the bucket refills back to full
// capacity.
func (b *TokenBucket) resetAfter() time.Duration {
	if b.tokens >= b.capacity {
		return 0
	}
	if b.refillRate <= 0 {
		// Below capacity but never refilling: capacity will never be reached
		// again; see neverRefills.
		return neverRefills
	}
	seconds := (b.capacity - b.tokens) / b.refillRate
	return time.Duration(seconds * float64(time.Second))
}
