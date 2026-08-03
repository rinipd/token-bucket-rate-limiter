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
}
