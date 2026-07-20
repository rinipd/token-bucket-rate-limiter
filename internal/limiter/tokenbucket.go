package limiter

import "time"

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
// NOTE: this type is NOT safe for concurrent use — synchronization is a later
// phase.
type TokenBucket struct {
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

// Allow reports whether a request may proceed, consuming a token if so.
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
func (b *TokenBucket) Allow() bool {
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
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
