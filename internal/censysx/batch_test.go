package censysx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
)

// batchServer echoes a host asset for every requested ID, recording each
// request's ID list so tests can assert on the chunking.
func batchServer(t *testing.T, known map[string]bool) (*Client, *[][]string) {
	t.Helper()

	var batches [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		var req struct {
			HostIds []string `json:"host_ids"`
			AtTime  *string  `json:"at_time"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decoding body %q: %v", body, err)
		}
		batches = append(batches, req.HostIds)

		results := make([]any, 0, len(req.HostIds))
		for _, id := range req.HostIds {
			// A host Censys has never seen is simply absent from the response.
			if known != nil && !known[id] {
				continue
			}
			results = append(results, map[string]any{"resource": map[string]any{"ip": id}})
		}

		w.Header().Set("Content-Type", "application/vnd.censys.api.v3.host.v1+json")
		if err := json.NewEncoder(w).Encode(map[string]any{"result": results}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return New(Options{OrgID: "org", Token: "tok", BaseURL: srv.URL, Timeout: 5 * time.Second}), &batches
}

func ips(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "198.51.100." + strconv.Itoa(i)
	}
	return out
}

func TestHostsEachChunksAtTheBatchLimit(t *testing.T) {
	client, batches := batchServer(t, nil)

	targets := ips(250)
	found, missing, err := client.HostsEach(context.Background(), targets, nil, func(components.Host) error {
		return nil
	})
	if err != nil {
		t.Fatalf("HostsEach: %v", err)
	}
	if found != 250 || missing != 0 {
		t.Errorf("found/missing = %d/%d, want 250/0", found, missing)
	}

	if len(*batches) != 3 {
		t.Fatalf("made %d requests, want 3 (250 hosts at %d per batch)", len(*batches), MaxBatchSize)
	}
	for i, sizes := range []int{100, 100, 50} {
		if got := len((*batches)[i]); got != sizes {
			t.Errorf("batch %d had %d hosts, want %d", i, got, sizes)
		}
	}
}

func TestHostsEachCountsAbsentHostsAsMissing(t *testing.T) {
	client, _ := batchServer(t, map[string]bool{"198.51.100.0": true, "198.51.100.2": true})

	var seen []string
	found, missing, err := client.HostsEach(context.Background(), ips(4), nil, func(h components.Host) error {
		seen = append(seen, deref(h.GetIP()))
		return nil
	})
	if err != nil {
		t.Fatalf("HostsEach: %v", err)
	}
	if found != 2 || missing != 2 {
		t.Errorf("found/missing = %d/%d, want 2/2", found, missing)
	}
	if len(seen) != 2 {
		t.Errorf("callback saw %v, want 2 hosts", seen)
	}
}

func TestHostsEachSendsAtTime(t *testing.T) {
	var sent *string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AtTime *string `json:"at_time"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		sent = req.AtTime

		w.Header().Set("Content-Type", "application/vnd.censys.api.v3.host.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
	}))
	t.Cleanup(srv.Close)
	client := New(Options{OrgID: "org", Token: "tok", BaseURL: srv.URL, Timeout: 5 * time.Second})

	at := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if _, _, err := client.HostsEach(context.Background(), ips(1), &at, func(components.Host) error {
		return nil
	}); err != nil {
		t.Fatalf("HostsEach: %v", err)
	}

	if sent == nil {
		t.Fatal("at_time was not sent")
	}
	if !strings.HasPrefix(*sent, "2026-01-15T12:00:00") {
		t.Errorf("at_time = %q, want the requested instant", *sent)
	}
}

func TestHostsBatchRejectsOversizedBatch(t *testing.T) {
	client, _ := batchServer(t, nil)
	if _, err := client.HostsBatch(context.Background(), ips(MaxBatchSize+1), nil); err == nil {
		t.Error("HostsBatch accepted a batch over the limit")
	}
}

func TestHostsEachEmptyInput(t *testing.T) {
	client, batches := batchServer(t, nil)

	found, missing, err := client.HostsEach(context.Background(), nil, nil, func(components.Host) error {
		t.Error("callback ran for an empty input")
		return nil
	})
	if err != nil || found != 0 || missing != 0 {
		t.Errorf("HostsEach(nil) = %d, %d, %v", found, missing, err)
	}
	if len(*batches) != 0 {
		t.Errorf("made %d requests for an empty input", len(*batches))
	}
}
