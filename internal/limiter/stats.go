package limiter

// ClientStats holds one client's cumulative allow/deny counts.
type ClientStats struct {
	Allowed int64 `json:"allowed"`
	Denied  int64 `json:"denied"`
}

// Stats maps client ID to that client's cumulative ClientStats. It is
// cumulative and monotonically increasing for the lifetime of the
// underlying store (or until a Redis stats key expires via TTL) — it is
// NOT reset by RemoveClient or a config change, since it's a record of
// traffic history, not limiter state.
type Stats map[string]ClientStats
