package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// newAdminTestMux builds a fresh Manager and AdminHandler and registers the
// admin routes on a mux, returning both so tests can drive HTTP requests
// through the mux (so {key} path values resolve) while asserting directly
// against the Manager's state.
func newAdminTestMux() (*http.ServeMux, *limiter.Manager) {
	mgr := limiter.NewManager()
	mux := http.NewServeMux()
	NewAdminHandler(mgr).Register(mux)
	return mux, mgr
}

// doJSON issues method/path through mux with body (if non-nil) JSON-encoded
// as the request body, returning the recorded response.
func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestAdmin_SetClientConfig_Valid verifies a valid PUT stores the config
// (checked directly against the Manager) and returns 200.
func TestAdmin_SetClientConfig_Valid(t *testing.T) {
	mux, mgr := newAdminTestMux()

	cfg := limiter.Config{RPS: 1, Burst: 5, Algorithm: limiter.AlgorithmTokenBucket}
	rec := doJSON(t, mux, http.MethodPut, "/admin/clients/alice", cfg)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	got, ok := mgr.GetConfig("alice")
	if !ok {
		t.Fatalf("GetConfig(alice) ok = false, want true after successful PUT")
	}
	if got != cfg {
		t.Fatalf("stored config = %+v, want %+v", got, cfg)
	}
}

// TestAdmin_SetClientConfig_InvalidConfig verifies a structurally valid JSON
// body that fails Config.Validate (negative RPS) is rejected with 400 and
// nothing is stored.
func TestAdmin_SetClientConfig_InvalidConfig(t *testing.T) {
	mux, mgr := newAdminTestMux()

	cfg := limiter.Config{RPS: -1, Burst: 5, Algorithm: limiter.AlgorithmTokenBucket}
	rec := doJSON(t, mux, http.MethodPut, "/admin/clients/alice", cfg)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if _, ok := mgr.GetConfig("alice"); ok {
		t.Fatalf("GetConfig(alice) ok = true, want false after rejected PUT")
	}
}

// TestAdmin_SetClientConfig_MalformedJSON verifies a body that isn't valid
// JSON at all is rejected with 400.
func TestAdmin_SetClientConfig_MalformedJSON(t *testing.T) {
	mux, _ := newAdminTestMux()

	req := httptest.NewRequest(http.MethodPut, "/admin/clients/alice", bytes.NewReader([]byte("{not valid json")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestAdmin_GetClientConfig covers both the found and not-found paths of
// GET /admin/clients/{key}.
func TestAdmin_GetClientConfig(t *testing.T) {
	mux, mgr := newAdminTestMux()

	cfg := limiter.Config{RPS: 2, Burst: 10, Algorithm: limiter.AlgorithmTokenBucket}
	if err := mgr.SetConfig("alice", cfg); err != nil {
		t.Fatalf("SetConfig(alice): %v", err)
	}

	tests := []struct {
		name       string
		key        string
		wantStatus int
		wantBody   bool // whether we expect a Config JSON body to compare
	}{
		{name: "existing client", key: "alice", wantStatus: http.StatusOK, wantBody: true},
		{name: "unknown client", key: "bob", wantStatus: http.StatusNotFound, wantBody: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/clients/"+tt.key, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("GET status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody {
				var got limiter.Config
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("unmarshal response body: %v; body: %s", err, rec.Body.String())
				}
				if got != cfg {
					t.Fatalf("returned config = %+v, want %+v", got, cfg)
				}
			}
		})
	}
}

// TestAdmin_RemoveClient verifies DELETE removes the client's config (and
// bucket) and returns 200.
func TestAdmin_RemoveClient(t *testing.T) {
	mux, mgr := newAdminTestMux()

	if err := mgr.SetConfig("alice", limiter.Config{RPS: 1, Burst: 5, Algorithm: limiter.AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(alice): %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/admin/clients/alice", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, ok := mgr.GetConfig("alice"); ok {
		t.Fatalf("GetConfig(alice) ok = true, want false after DELETE")
	}
}
