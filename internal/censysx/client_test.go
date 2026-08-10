package censysx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// statusServer fails the first failures requests with the given status, then
// succeeds. It reports how many requests it saw.
func statusServer(t *testing.T, status, failures int) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(calls.Add(1)) <= failures {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "title": http.StatusText(status)})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": hostPage("198.51.100.1", "", 1)})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// The SDK retry policy covers 429 and 5xx, so a transient 503 must be absorbed
// without the caller doing anything.
func TestClientRetriesTransientStatuses(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, calls := statusServer(t, status, 1)
			client := New(Options{
				OrgID:   "org",
				Token:   "tok",
				BaseURL: srv.URL,
				Timeout: 20 * time.Second,
			})

			if _, err := client.Search(context.Background(), SearchParams{Query: "x"}, ""); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := calls.Load(); got != 2 {
				t.Errorf("server saw %d requests, want 2 (one failure then success)", got)
			}
		})
	}
}

// A 400 means the query itself is wrong. Retrying it cannot help and, on metered
// endpoints, is not free.
func TestClientDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, calls := statusServer(t, status, 99)
			client := New(Options{
				OrgID:   "org",
				Token:   "tok",
				BaseURL: srv.URL,
				Timeout: 20 * time.Second,
			})

			_, err := client.Search(context.Background(), SearchParams{Query: "x"}, "")
			if err == nil {
				t.Fatal("Search succeeded, want an error")
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("server saw %d requests, want 1", got)
			}
			if status == 401 || status == 403 {
				if !IsAuth(err) {
					t.Errorf("IsAuth(%v) = false, want true", err)
				}
			}
			if status == 404 && !IsNotFound(err) {
				t.Errorf("IsNotFound(%v) = false, want true", err)
			}
		})
	}
}

func TestClientDisableRetryAttemptsOnce(t *testing.T) {
	srv, calls := statusServer(t, http.StatusServiceUnavailable, 99)
	client := New(Options{
		OrgID:        "org",
		Token:        "tok",
		BaseURL:      srv.URL,
		Timeout:      20 * time.Second,
		DisableRetry: true,
	})

	if _, err := client.Search(context.Background(), SearchParams{Query: "x"}, ""); err == nil {
		t.Fatal("Search succeeded, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
}

func TestClientAppliesDefaults(t *testing.T) {
	c := New(Options{OrgID: "org", Token: "tok"})
	if c.pageSize != DefaultPageSize {
		t.Errorf("pageSize = %d, want %d", c.pageSize, DefaultPageSize)
	}
	if c.OrgID() != "org" {
		t.Errorf("OrgID() = %q, want org", c.OrgID())
	}
	if c.SDK() == nil {
		t.Error("SDK() = nil")
	}
}
