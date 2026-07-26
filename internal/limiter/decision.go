package limiter

import "time"

// neverRefills is the sentinel used for RetryAfter/ResetAfter when a
// bucket's refillRate is 0. Such a bucket never gains tokens again once
// spent, so there's no finite "time until N tokens are available" to report;
// a very large duration communicates "don't bother retrying" without a
// divide-by-zero.
const neverRefills = time.Duration(1<<63 - 1) // ~292 years; treat as "infinite"

// Decision is the result of a rate-limit check against a single bucket.
type Decision struct {
	Allowed bool // whether the request may proceed

	Limit     float64 // the bucket's capacity (max burst size)
	Remaining float64 // whole tokens left in the bucket after this decision

	// RetryAfter is how long the caller should wait before retrying. It is
	// only meaningful when Allowed is false; 0 when Allowed is true.
	RetryAfter time.Duration

	// ResetAfter is how long until the bucket refills back to full capacity.
	ResetAfter time.Duration
}
