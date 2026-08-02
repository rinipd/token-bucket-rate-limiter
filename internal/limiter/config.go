package limiter

import "fmt"

// The rate-limiting algorithms currently supported by Config.Algorithm.
const (
	AlgorithmTokenBucket   = "token_bucket"
	AlgorithmSlidingWindow = "sliding_window"
)

// Config describes the rate limit applied to a single client.
type Config struct {
	RPS       float64 // tokens (requests) refilled per second
	Burst     float64 // maximum tokens the bucket can hold (burst capacity)
	Algorithm string  // rate-limiting algorithm to use: "token_bucket" or "sliding_window"
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
	if c.Algorithm != AlgorithmTokenBucket && c.Algorithm != AlgorithmSlidingWindow {
		return fmt.Errorf("invalid config: unsupported algorithm %q (supported: %q, %q)", c.Algorithm, AlgorithmTokenBucket, AlgorithmSlidingWindow)
	}
	return nil
}
