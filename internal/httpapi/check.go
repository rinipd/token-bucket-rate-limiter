package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// CheckHandler serves the /check/{key} route for testing a rate-limit
// decision against a client.
type CheckHandler struct {
	manager *limiter.Manager
}

// NewCheckHandler returns a CheckHandler backed by mgr.
func NewCheckHandler(mgr *limiter.Manager) *CheckHandler {
	return &CheckHandler{manager: mgr}
}

// Register wires the /check route onto mux.
func (h *CheckHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /check/{key}", h.check)
}

// check consumes a token for the client identified by the {key} path value
// and reports the outcome via standard rate-limit headers, set on every
// response regardless of outcome:
//
//	X-RateLimit-Limit:     the bucket's capacity
//	X-RateLimit-Remaining: tokens left after this request
//	X-RateLimit-Reset:     seconds until the bucket is back to full capacity
//
// If denied, Retry-After (seconds until a token is available) is also set,
// and the response is 429 DENY; otherwise it's 200 ALLOW.
func (h *CheckHandler) check(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	decision := h.manager.Allow(key)

	header := w.Header()
	header.Set("X-RateLimit-Limit", formatFloat(decision.Limit))
	header.Set("X-RateLimit-Remaining", formatFloat(decision.Remaining))
	header.Set("X-RateLimit-Reset", formatSeconds(decision.ResetAfter))

	if !decision.Allowed {
		header.Set("Retry-After", formatSecondsCeil(decision.RetryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("DENY"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ALLOW"))
}

// formatFloat renders a token count without unnecessary decimal places.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// formatSeconds renders d as whole seconds, rounded down.
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(math.Floor(d.Seconds()), 'f', 0, 64)
}

// formatSecondsCeil renders d as whole seconds, rounded up so a client never
// retries before it actually has a token available.
func formatSecondsCeil(d time.Duration) string {
	return strconv.FormatFloat(math.Ceil(d.Seconds()), 'f', 0, 64)
}
