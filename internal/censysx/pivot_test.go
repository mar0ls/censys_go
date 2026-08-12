package censysx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
)

// The timeline endpoint answers with its own versioned media type and the SDK
// rejects anything else.
const timelineMediaType = "application/vnd.censys.api.v3.host_timeline_event.v1+json"

// pivotServer serves the given pages in order, recording every request URL.
func pivotServer(t *testing.T, pages []map[string]any) (*Client, *[]string) {
	t.Helper()

	var queries []string
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.String())
		if idx >= len(pages) {
			t.Errorf("unexpected request #%d", idx+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body := pages[idx]
		idx++

		mediaType := "application/json"
		if strings.Contains(r.URL.Path, "/timeline") {
			mediaType = timelineMediaType
		}
		w.Header().Set("Content-Type", mediaType)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": body})
	}))
	t.Cleanup(srv.Close)

	return New(Options{OrgID: "org", Token: "tok", BaseURL: srv.URL, Timeout: 5 * time.Second}), &queries
}

func observationPage(ips []string, nextToken string, total int) map[string]any {
	ranges := make([]any, 0, len(ips))
	for _, ip := range ips {
		ranges = append(ranges, map[string]any{
			"ip":                 ip,
			"port":               443,
			"protocols":          []string{"HTTP"},
			"transport_protocol": "tcp",
			"start_time":         "2026-01-01T00:00:00Z",
			"end_time":           "2026-02-01T00:00:00Z",
		})
	}
	page := map[string]any{"ranges": ranges, "total_results": total}
	if nextToken != "" {
		page["next_page_token"] = nextToken
	}
	return page
}

func TestCertObservationsWalksEveryPage(t *testing.T) {
	client, queries := pivotServer(t, []map[string]any{
		observationPage([]string{"198.51.100.1", "198.51.100.2"}, "page-2", 3),
		observationPage([]string{"198.51.100.3"}, "", 3),
	})

	var got []string
	total, _, err := client.CertObservations(context.Background(),
		CertObservationParams{Fingerprint: strings.Repeat("ab", 32)},
		func(r components.HostObservationRange) error {
			got = append(got, r.IP)
			return nil
		})
	if err != nil {
		t.Fatalf("CertObservations: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("collected %v, want 3 ranges", got)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(*queries) != 2 {
		t.Fatalf("made %d requests, want 2", len(*queries))
	}
	if !strings.Contains((*queries)[1], "page_token=page-2") {
		t.Errorf("second request did not carry the page token: %s", (*queries)[1])
	}
}

// Each page costs ObservationCreditsPerPage, so an unbounded walk on a widely
// deployed certificate is expensive. MaxPages has to actually stop it.
func TestCertObservationsHonoursMaxPages(t *testing.T) {
	client, queries := pivotServer(t, []map[string]any{
		observationPage([]string{"198.51.100.1"}, "page-2", 500),
		observationPage([]string{"198.51.100.2"}, "page-3", 500),
	})

	total, pages, err := client.CertObservations(context.Background(),
		CertObservationParams{Fingerprint: strings.Repeat("ab", 32), MaxPages: 2},
		func(components.HostObservationRange) error { return nil })
	if err != nil {
		t.Fatalf("CertObservations: %v", err)
	}
	if pages != 2 {
		t.Errorf("fetched %d pages, want 2", pages)
	}
	if len(*queries) != 2 {
		t.Errorf("made %d requests, want 2 (a token was still outstanding)", len(*queries))
	}
	if total != 500 {
		t.Errorf("total = %d, want the API's figure of 500", total)
	}
}

func TestCertObservationsReportsPagesFetched(t *testing.T) {
	client, _ := pivotServer(t, []map[string]any{
		observationPage([]string{"198.51.100.1"}, "page-2", 2),
		observationPage([]string{"198.51.100.2"}, "", 2),
	})

	_, pages, err := client.CertObservations(context.Background(),
		CertObservationParams{Fingerprint: strings.Repeat("ab", 32)},
		func(components.HostObservationRange) error { return nil })
	if err != nil {
		t.Fatalf("CertObservations: %v", err)
	}
	if pages != 2 {
		t.Errorf("pages = %d, want 2", pages)
	}
}

func TestCertObservationsAppliesFilters(t *testing.T) {
	client, queries := pivotServer(t, []map[string]any{observationPage(nil, "", 0)})

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := client.CertObservations(context.Background(), CertObservationParams{
		Fingerprint: strings.Repeat("ab", 32),
		Start:       start,
		End:         end,
		Port:        8443,
		Protocol:    "tcp",
	}, func(components.HostObservationRange) error { return nil }); err != nil {
		t.Fatalf("CertObservations: %v", err)
	}

	query := (*queries)[0]
	for _, want := range []string{"port=8443", "protocol=tcp", "start_time=", "end_time="} {
		if !strings.Contains(query, want) {
			t.Errorf("query %s missing %q", query, want)
		}
	}
}

func TestCertObservationsCapsPageSize(t *testing.T) {
	client, queries := pivotServer(t, []map[string]any{observationPage(nil, "", 0)})

	if _, _, err := client.CertObservations(context.Background(), CertObservationParams{
		Fingerprint: strings.Repeat("ab", 32),
		PageSize:    5000,
	}, func(components.HostObservationRange) error { return nil }); err != nil {
		t.Fatalf("CertObservations: %v", err)
	}
	if !strings.Contains((*queries)[0], "page_size=100") {
		t.Errorf("page size not capped at the documented maximum: %s", (*queries)[0])
	}
}

func TestCertObservationsValidatesFingerprint(t *testing.T) {
	client, _ := pivotServer(t, nil)
	if _, _, err := client.CertObservations(context.Background(),
		CertObservationParams{Fingerprint: "nope"},
		func(components.HostObservationRange) error { return nil }); err == nil {
		t.Error("CertObservations accepted a malformed fingerprint")
	}
}

// The API's start_time is the end of the window nearest to now and end_time the
// one furthest away. Timeline takes the range the ordinary way round, so the
// values must come out swapped on the wire.
func TestTimelineSwapsTheWindowForTheAPI(t *testing.T) {
	client, queries := pivotServer(t, []map[string]any{{"events": []any{}, "scanned_to": "2026-08-01T00:00:00Z"}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := client.Timeline(context.Background(), "198.51.100.1", from, to); err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	query := (*queries)[0]
	if !strings.Contains(query, "start_time=2026-06-01") {
		t.Errorf("start_time should carry the later instant: %s", query)
	}
	if !strings.Contains(query, "end_time=2026-01-01") {
		t.Errorf("end_time should carry the earlier instant: %s", query)
	}
}

func TestTimelineRejectsInvertedWindow(t *testing.T) {
	client, _ := pivotServer(t, nil)

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := client.Timeline(context.Background(), "198.51.100.1", from, to); err == nil {
		t.Error("Timeline accepted a window that ends before it starts")
	}
}

func TestTimelineRejectsEmptyHost(t *testing.T) {
	client, _ := pivotServer(t, nil)
	if _, err := client.Timeline(context.Background(), "", time.Now().Add(-time.Hour), time.Now()); err == nil {
		t.Error("Timeline accepted an empty host ID")
	}
}
