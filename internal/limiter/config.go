package limiter

import "fmt"

// AlgorithmTokenBucket is the only rate-limiting algorithm currently
// supported. Additional algorithms (e.g. sliding window) may be added later.
const AlgorithmTokenBucket = "token_bucket"

// Config describes the rate limit applied to a single client.
type Config struct {
	RPS       float64 // tokens (requests) refilled per second
	Burst     float64 // maximum tokens the bucket can hold (burst capacity)
	Algorithm string  // rate-limiting algorithm to use; only "token_bucket" for now
}

// Validate reports whether c is a usable configuration, returning a
// descriptive error for the first problem found.
func (c Config) Validate() error {
	if c.RPS <= 0 {
		return fmt.Errorf("invalid config: RPS must be > 0, got %v", c.RPS)
	}
	if c.Burst < 1 {
		return fmt.Errorf("invalid config: Burst must be >= 1, got %v", c.Burst)
	}
	if c.Algorithm != AlgorithmTokenBucket {
		return fmt.Errorf("invalid config: unsupported algorithm %q (only %q is supported)", c.Algorithm, AlgorithmTokenBucket)
	}
	return nil
}
