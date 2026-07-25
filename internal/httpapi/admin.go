// Package httpapi exposes HTTP handlers for administering the rate limiter.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// AdminHandler serves the /admin/clients/{key} routes for managing
// per-client rate-limit configuration.
type AdminHandler struct {
	manager *limiter.Manager
}

// NewAdminHandler returns an AdminHandler backed by mgr.
func NewAdminHandler(mgr *limiter.Manager) *AdminHandler {
	return &AdminHandler{manager: mgr}
}

// Register wires the admin routes onto mux using Go 1.22+ method+path
// pattern matching.
func (h *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /admin/clients/{key}", h.setClientConfig)
	mux.HandleFunc("GET /admin/clients/{key}", h.getClientConfig)
	mux.HandleFunc("DELETE /admin/clients/{key}", h.removeClient)
}

// setClientConfig decodes a Config from the request body and stores it for
// the client identified by the {key} path value. It replies 200 on success
// or 400 with the validation error message if the config is invalid.
func (h *AdminHandler) setClientConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	var cfg limiter.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.manager.SetConfig(key, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// getClientConfig returns the stored Config for the client identified by the
// {key} path value as JSON, or 404 if no config has been set for it.
func (h *AdminHandler) getClientConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	cfg, ok := h.manager.GetConfig(key)
	if !ok {
		http.Error(w, "no config for client "+key, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// removeClient deletes the config and bucket for the client identified by
// the {key} path value and replies 200.
func (h *AdminHandler) removeClient(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	h.manager.RemoveClient(key)
	w.WriteHeader(http.StatusOK)
}
