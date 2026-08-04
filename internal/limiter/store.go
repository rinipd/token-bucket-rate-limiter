package limiter

// Store is the backend abstraction for rate-limiter state: given a client
// ID, decide whether to allow a request and manage that client's Config.
// The in-memory Manager satisfies this today; a future Redis-backed store
// can satisfy it too, letting the HTTP layer (and main) pick a backend
// without caring which concrete implementation is behind it.
//
// Snapshot/Restore are deliberately NOT part of this interface: they're
// specific to Manager's in-memory nature (a Redis store persists itself, so
// it has no equivalent "dump to a PersistedState" operation). They stay as
// methods on Manager only, used directly by the concrete *Manager the
// caller constructs — not through the Store interface.
//
// Likewise, there is no recordDecision in this interface: counting a
// decision is an implementation detail private to each backend's Allow
// (Manager updates its own counts map, RedisStore issues an HINCRBY) —
// callers of Store never need to invoke it themselves. Store only exposes
// the read side, Stats, for whoever wants to report on those counts.
type Store interface {
	// Allow reports the outcome of a request for clientID, consuming
	// capacity from that client's limiter if the request is allowed.
	Allow(clientID string) Decision

	// SetConfig validates cfg and stores it as clientID's rate-limit
	// configuration.
	SetConfig(clientID string, cfg Config) error

	// GetConfig returns clientID's stored config, if any.
	GetConfig(clientID string) (Config, bool)

	// RemoveClient deletes clientID's config (and any limiter state).
	RemoveClient(clientID string)

	// Stats returns a snapshot of every client's cumulative allow/deny
	// counts. For Manager this bookkeeping is purely in-process; for
	// RedisStore it's backed by Redis so multiple server instances sharing
	// that Redis all see (and contribute to) the same counts. That mirrors
	// why the limit itself lives in Redis for the distributed case: a
	// shared rate limit only means something if its state is shared, and
	// the same is true of reporting on it — without shared stats, each
	// instance would only ever report the slice of traffic it personally
	// happened to handle, not the whole picture.
	Stats() Stats
}
