package limiter

import "time"

// BucketState is the serializable form of a TokenBucket's data.
//
// TokenBucket itself can't be marshaled directly: it embeds a sync.Mutex
// (meaningless outside the process) and a `now func() time.Time` clock
// (functions can't be encoded as JSON at all). BucketState carries just the
// plain data fields needed to reconstruct an equivalent bucket elsewhere —
// see TokenBucket.State and NewTokenBucketFromState.
type BucketState struct {
	Tokens     float64   `json:"tokens"`
	LastRefill time.Time `json:"lastRefill"`
	Capacity   float64   `json:"capacity"`
	RefillRate float64   `json:"refillRate"`
}

// SlidingWindowState is the serializable form of a SlidingWindow's data, for
// the same reason BucketState exists for TokenBucket: SlidingWindow embeds a
// mutex and an injectable clock, neither of which can (or should) be
// serialized. See SlidingWindow.State and NewSlidingWindowFromState.
type SlidingWindowState struct {
	Limit         float64       `json:"limit"`
	Window        time.Duration `json:"window"`
	WindowStart   time.Time     `json:"windowStart"`
	CurrentCount  float64       `json:"currentCount"`
	PreviousCount float64       `json:"previousCount"`
}

// LimiterState is the discriminated (tagged-union) persisted state for one
// client's limiter. Algorithm names which concrete algorithm produced this
// state, and exactly one of the pointer fields is populated to match it —
// TokenBucket for AlgorithmTokenBucket, SlidingWindow for
// AlgorithmSlidingWindow.
//
// This shape, rather than storing a Limiter interface value directly, is
// what makes persistence possible at all: encoding/json can't marshal an
// interface value back into its original concrete type on its own (it has
// no way to know which concrete type to allocate when unmarshaling into a
// Limiter). A tagged struct with one field per known concrete type sidesteps
// that: Manager.Snapshot picks the field that matches the algorithm that
// produced the state, and Manager.Restore switches on Algorithm to decide
// which FromState constructor to call. The pointers are `omitempty` so the
// serialized JSON only ever contains the one field that's actually
// meaningful for a given entry, instead of always emitting both with one
// forced to null.
type LimiterState struct {
	Algorithm     string              `json:"algorithm"`
	TokenBucket   *BucketState        `json:"tokenBucket,omitempty"`
	SlidingWindow *SlidingWindowState `json:"slidingWindow,omitempty"`
}

// PersistedState is a full snapshot of a Manager: every client's Config plus
// every live client's LimiterState, suitable for writing to disk and later
// restoring.
type PersistedState struct {
	Configs  map[string]Config       `json:"configs"`
	Limiters map[string]LimiterState `json:"limiters"`
}
