package censysx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// searchServer serves the given pages in order, recording each request body.
// The last page is returned with an empty next_page_token.
func searchServer(t *testing.T, pages []map[string]any) (*Client, *[]map[string]any) {
	t.Helper()

	var requests []map[string]any
	var idx atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Errorf("decoding request body %q: %v", body, err)
		}
		requests = append(requests, decoded)

		i := int(idx.Add(1)) - 1
		if i >= len(pages) {
			t.Errorf("unexpected request #%d, only %d pages configured", i+1, len(pages))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"result": pages[i]}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := New(Options{OrgID: "org", Token: "tok", BaseURL: srv.URL, Timeout: 5 * time.Second})
	return client, &requests
}

func hostPage(ip, nextToken string, total float64) map[string]any {
	return map[string]any{
		"hits":            []any{map[string]any{"host_v1": map[string]any{"resource": map[string]any{"ip": ip}}}},
		"next_page_token": nextToken,
		"total_hits":      total,
	}
}

func TestSearchEachFollowsPageTokens(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{
		hostPage("198.51.100.1", "tok-2", 3),
		hostPage("198.51.100.2", "tok-3", 3),
		hostPage("198.51.100.3", "", 3),
	})

	var ips []string
	err := client.SearchEach(context.Background(), SearchParams{Query: "services.port:443"}, 0, func(p SearchPage) error {
		ips = append(ips, IPsFromHits(p.Hits)...)
		return nil
	})
	if err != nil {
		t.Fatalf("SearchEach: %v", err)
	}

	if len(ips) != 3 {
		t.Fatalf("collected %d IPs, want 3: %v", len(ips), ips)
	}
	if len(*requests) != 3 {
		t.Fatalf("made %d requests, want 3", len(*requests))
	}
	if tok, ok := (*requests)[0]["page_token"]; ok {
		t.Errorf("first request carried page_token %v, want none", tok)
	}
	if tok := (*requests)[1]["page_token"]; tok != "tok-2" {
		t.Errorf("second request page_token = %v, want tok-2", tok)
	}
	if tok := (*requests)[2]["page_token"]; tok != "tok-3" {
		t.Errorf("third request page_token = %v, want tok-3", tok)
	}
}

func TestSearchEachHonoursMaxPages(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{
		hostPage("198.51.100.1", "tok-2", 99),
		hostPage("198.51.100.2", "tok-3", 99),
	})

	pages := 0
	err := client.SearchEach(context.Background(), SearchParams{Query: "x"}, 2, func(SearchPage) error {
		pages++
		return nil
	})
	if err != nil {
		t.Fatalf("SearchEach: %v", err)
	}
	if pages != 2 {
		t.Errorf("visited %d pages, want 2", pages)
	}
	if len(*requests) != 2 {
		t.Errorf("made %d requests, want 2 (must stop even though a token remained)", len(*requests))
	}
}

func TestSearchEachStopsOnCallbackError(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{
		hostPage("198.51.100.1", "tok-2", 9),
		hostPage("198.51.100.2", "tok-3", 9),
	})

	sentinel := errors.New("caller gave up")
	err := client.SearchEach(context.Background(), SearchParams{Query: "x"}, 0, func(SearchPage) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if len(*requests) != 1 {
		t.Errorf("made %d requests, want 1", len(*requests))
	}
}

func TestSearchSendsDefaultFieldsAndPageSize(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{hostPage("198.51.100.1", "", 1)})

	if _, err := client.Search(context.Background(), SearchParams{Query: "x", PageSize: 25}, ""); err != nil {
		t.Fatalf("Search: %v", err)
	}

	req := (*requests)[0]
	if got := req["page_size"]; got != float64(25) {
		t.Errorf("page_size = %v, want 25", got)
	}
	fields, ok := req["fields"].([]any)
	if !ok {
		t.Fatalf("fields = %v, want a list", req["fields"])
	}
	if len(fields) != len(DefaultSearchFields) {
		t.Errorf("sent %d fields, want %d", len(fields), len(DefaultSearchFields))
	}
}

func TestSearchAllFieldsOmitsFieldSelection(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{hostPage("198.51.100.1", "", 1)})

	if _, err := client.Search(context.Background(), SearchParams{Query: "x", AllFields: true}, ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if fields, present := (*requests)[0]["fields"]; present {
		t.Errorf("fields = %v, want the key to be absent", fields)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	client := New(Options{OrgID: "org", Token: "tok"})
	if _, err := client.Search(context.Background(), SearchParams{}, ""); !errors.Is(err, ErrEmptyQuery) {
		t.Errorf("err = %v, want ErrEmptyQuery", err)
	}
}

func TestSearchReportsTotalHits(t *testing.T) {
	client, _ := searchServer(t, []map[string]any{hostPage("198.51.100.1", "", 4242)})

	page, err := client.Search(context.Background(), SearchParams{Query: "x"}, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.TotalHits != 4242 {
		t.Errorf("TotalHits = %v, want 4242", page.TotalHits)
	}
}

func TestSearchEachStopsOnCancelledContext(t *testing.T) {
	client, requests := searchServer(t, []map[string]any{hostPage("198.51.100.1", "tok-2", 9)})

	ctx, cancel := context.WithCancel(context.Background())
	err := client.SearchEach(ctx, SearchParams{Query: "x"}, 0, func(SearchPage) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(*requests) != 1 {
		t.Errorf("made %d requests, want 1", len(*requests))
	}
}
