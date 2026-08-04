package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// StatsHandler serves the /stats route, exposing per-client allow/deny
// counts. It works identically for either backend (the in-memory Manager or
// a Redis-backed store) since both satisfy limiter.Store's Stats method.
type StatsHandler struct {
	store limiter.Store
}

// NewStatsHandler returns a StatsHandler backed by store.
func NewStatsHandler(store limiter.Store) *StatsHandler {
	return &StatsHandler{store: store}
}

// Register wires the /stats route onto mux.
func (h *StatsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /stats", h.stats)
}

// stats returns every client's cumulative allowed/denied counts as JSON.
//
// Access-Control-Allow-Origin: * is set so a browser-based dashboard served
// from a different origin (a different port during local development, most
// commonly) can fetch this endpoint directly without the browser blocking
// the response as cross-origin. That's a reasonable default for a dev
// dashboard reading read-only, non-sensitive counters; a production
// deployment should scope this to the dashboard's actual origin (or put
// this endpoint behind auth) rather than allowing any site to read it.
func (h *StatsHandler) stats(w http.ResponseWriter, r *http.Request) {
	stats := h.store.Stats()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(stats)
}
