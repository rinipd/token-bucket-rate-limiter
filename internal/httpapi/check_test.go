package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// newCheckTestMux builds a fresh Manager with both the admin and check
// routes registered on one mux, so tests can configure a client via
// /admin/clients/{key} and then exercise /check/{key} against it.
func newCheckTestMux() (*http.ServeMux, *limiter.Manager) {
	mgr := limiter.NewManager()
	mux := http.NewServeMux()
	NewAdminHandler(mgr).Register(mux)
	NewCheckHandler(mgr).Register(mux)
	return mux, mgr
}

// doCheck issues a GET /check/{key} through mux and returns the recorded
// response.
func doCheck(mux *http.ServeMux, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/check/"+key, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestCheck_BurstThenDeny verifies that a client configured with burst 2
// gets exactly 2 ALLOWs back to back, then a DENY, with the standard
// rate-limit headers present throughout and Retry-After set on the denial.
//
// RPS is set to a vanishingly small (but positive, since Validate rejects
// RPS <= 0) value so refill during the test's real (sub-millisecond)
// runtime is negligible — this keeps the burst-then-deny outcome
// deterministic instead of depending on scheduling timing.
func TestCheck_BurstThenDeny(t *testing.T) {
	mux, mgr := newCheckTestMux()

	const noRefillRPS = 1e-9
	if err := mgr.SetConfig("alice", limiter.Config{RPS: noRefillRPS, Burst: 2, Algorithm: limiter.AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(alice): %v", err)
	}

	tests := []struct {
		name           string
		wantStatus     int
		wantBody       string
		wantRemaining  string
		wantRetryAfter bool // whether Retry-After must be present
	}{
		{name: "request 1", wantStatus: http.StatusOK, wantBody: "ALLOW", wantRemaining: "1"},
		{name: "request 2", wantStatus: http.StatusOK, wantBody: "ALLOW", wantRemaining: "0"},
		{name: "request 3", wantStatus: http.StatusTooManyRequests, wantBody: "DENY", wantRemaining: "0", wantRetryAfter: true},
	}

	var previousRemaining float64 = -1 // sentinel: no previous call yet

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doCheck(mux, "alice")

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Body.String(); got != tt.wantBody {
				t.Fatalf("body = %q, want %q", got, tt.wantBody)
			}

			limitHeader := rec.Header().Get("X-RateLimit-Limit")
			if limitHeader != "2" {
				t.Fatalf("X-RateLimit-Limit = %q, want %q", limitHeader, "2")
			}

			remainingHeader := rec.Header().Get("X-RateLimit-Remaining")
			if remainingHeader == "" {
				t.Fatalf("X-RateLimit-Remaining header missing")
			}
			if remainingHeader != tt.wantRemaining {
				t.Fatalf("X-RateLimit-Remaining = %q, want %q", remainingHeader, tt.wantRemaining)
			}

			remaining, err := strconv.ParseFloat(remainingHeader, 64)
			if err != nil {
				t.Fatalf("parse X-RateLimit-Remaining %q: %v", remainingHeader, err)
			}
			if previousRemaining >= 0 && remaining > previousRemaining {
				t.Fatalf("X-RateLimit-Remaining increased from %v to %v, want non-increasing", previousRemaining, remaining)
			}
			previousRemaining = remaining

			retryAfter := rec.Header().Get("Retry-After")
			if tt.wantRetryAfter && retryAfter == "" {
				t.Fatalf("Retry-After header missing on denied response")
			}
			if !tt.wantRetryAfter && retryAfter != "" {
				t.Fatalf("Retry-After header = %q, want unset on allowed response", retryAfter)
			}
		})
	}
}
