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

// PersistedState is a full snapshot of a Manager: every client's Config plus
// every live bucket's BucketState, suitable for writing to disk and later
// restoring.
type PersistedState struct {
	Configs map[string]Config      `json:"configs"`
	Buckets map[string]BucketState `json:"buckets"`
}
