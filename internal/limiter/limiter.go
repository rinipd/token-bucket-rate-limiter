package limiter

// Limiter is the common interface satisfied by every rate-limiting
// algorithm: given a request, decide whether to allow it and report the
// outcome as a Decision.
//
// Both *TokenBucket and *SlidingWindow already expose a method with this
// exact signature (Allow() Decision), so they satisfy Limiter implicitly —
// Go's structural typing means neither type needs any change to become
// interchangeable through this interface; Manager can hold either behind a
// single Limiter value without knowing which concrete algorithm it is.
type Limiter interface {
	Allow() Decision
}
