package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mar0ls/censys_go/internal/config"
)

// The asset endpoints answer with a versioned media type and the SDK rejects
// anything else, so the stub has to reproduce it per route.
const (
	hostMediaType = "application/vnd.censys.api.v3.host.v1+json"
	certMediaType = "application/vnd.censys.api.v3.certificate.v1+json"
)

// stubAPI serves canned JSON for whichever endpoint is called, recording the
// request paths it saw.
func stubAPI(t *testing.T, routes map[string]any) (string, *[]string) {
	t.Helper()

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		body, ok := routes[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 404, "title": "Not Found"})
			return
		}
		w.Header().Set("Content-Type", mediaTypeFor(r.URL.Path))
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &paths
}

func mediaTypeFor(path string) string {
	switch {
	case strings.HasPrefix(path, "/v3/global/asset/host"):
		return hostMediaType
	case strings.HasPrefix(path, "/v3/global/asset/certificate"):
		return certMediaType
	default:
		return "application/json"
	}
}

func hostResource(ip string) map[string]any {
	return map[string]any{
		"ip":                ip,
		"autonomous_system": map[string]any{"asn": 64500, "name": "TEST-AS"},
		"location":          map[string]any{"country": "Poland"},
		"services":          []any{map[string]any{"port": 443, "protocol": "HTTP"}},
	}
}

// hostBody is the single-host response, used by `host --no-batch`.
func hostBody(ip string) map[string]any {
	return map[string]any{"result": map[string]any{"resource": hostResource(ip)}}
}

// hostsBody is the batch response: hosts Censys does not know are absent rather
// than reported as errors.
func hostsBody(known ...string) map[string]any {
	assets := make([]any, 0, len(known))
	for _, ip := range known {
		assets = append(assets, map[string]any{"resource": hostResource(ip)})
	}
	return map[string]any{"result": assets}
}

func searchBody(ips []string, nextToken string) map[string]any {
	hits := make([]any, 0, len(ips))
	for _, ip := range ips {
		hits = append(hits, map[string]any{"host_v1": map[string]any{"resource": map[string]any{
			"ip":       ip,
			"services": []any{map[string]any{"port": 443}},
		}}})
	}
	return map[string]any{"result": map[string]any{
		"hits":            hits,
		"next_page_token": nextToken,
		"total_hits":      float64(len(ips)),
	}}
}

// runCLI invokes Run with isolated credentials and captures both streams.
func runCLI(t *testing.T, apiURL string, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvOrgID, "test-org")
	t.Setenv(config.EnvToken, "test-token")

	var out, msg bytes.Buffer
	full := append([]string{"--api-url", apiURL}, args...)
	err = Run(context.Background(), Env{
		In:      strings.NewReader(stdin),
		Out:     &out,
		Msg:     &msg,
		Version: "test",
	}, full)
	return out.String(), msg.String(), err
}

func TestSearchWritesNDJSONToStdoutOnly(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/search/query": searchBody([]string{"198.51.100.1", "198.51.100.2"}, ""),
	})

	stdout, stderr, err := runCLI(t, api, "", "search", "-q", "services.port:443")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout has %d lines, want 2:\n%s", len(lines), stdout)
	}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stdout line is not JSON: %v (%q)", err, line)
		}
	}
	// Status output must not contaminate the result stream.
	if strings.Contains(stdout, "[OK]") || strings.Contains(stdout, "[**]") {
		t.Errorf("status messages leaked into stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "[OK] 2 hosts written") {
		t.Errorf("stderr missing the summary:\n%s", stderr)
	}
}

func TestSearchFollowsAllPagesWhenAsked(t *testing.T) {
	// The stub returns the same page each time, so --pages caps the walk.
	api, paths := stubAPI(t, map[string]any{
		"/v3/global/search/query": searchBody([]string{"198.51.100.1"}, "next"),
	})

	stdout, stderr, err := runCLI(t, api, "", "search", "-q", "x", "--pages", "3")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if got := strings.Count(strings.TrimRight(stdout, "\n"), "\n") + 1; got != 3 {
		t.Errorf("wrote %d records, want 3", got)
	}
	if len(*paths) != 3 {
		t.Errorf("made %d requests, want 3", len(*paths))
	}
}

func TestHostReadsTargetsFromStdin(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.5"),
	})

	stdout, stderr, err := runCLI(t, api, "198.51.100.5\n# comment\n\n", "host")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &rec); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, stdout)
	}
	if rec["ip"] != "198.51.100.5" {
		t.Errorf("ip = %v", rec["ip"])
	}
}

// A CIDR expands into targets, but they still travel in a single batch request.
func TestHostExpandsCIDRArgumentIntoOneBatch(t *testing.T) {
	api, paths := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("203.0.113.0", "203.0.113.1"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "203.0.113.0/31")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if len(*paths) != 1 {
		t.Errorf("made %d requests, want 1: %v", len(*paths), *paths)
	}
	if got := strings.Count(stdout, "\n"); got != 2 {
		t.Errorf("wrote %d records, want 2:\n%s", got, stdout)
	}
}

// Hosts the batch response omits are counted, not treated as failures.
func TestHostReportsAbsentHosts(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.2"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.1", "198.51.100.2")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if got := strings.Count(stdout, "\n"); got != 1 {
		t.Errorf("wrote %d records, want 1:\n%s", got, stdout)
	}
	if !strings.Contains(stderr, "1 hosts written, 1 not present in Censys") {
		t.Errorf("stderr missing the tally:\n%s", stderr)
	}
}

// --no-batch trades round trips for per-host error reporting.
func TestHostNoBatchNamesTheFailingHost(t *testing.T) {
	api, paths := stubAPI(t, map[string]any{
		"/v3/global/asset/host/198.51.100.2": hostBody("198.51.100.2"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.1", "198.51.100.2", "--no-batch")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if len(*paths) != 2 {
		t.Errorf("made %d requests, want 2: %v", len(*paths), *paths)
	}
	if got := strings.Count(stdout, "\n"); got != 1 {
		t.Errorf("wrote %d records, want 1:\n%s", got, stdout)
	}
	if !strings.Contains(stderr, "198.51.100.1: not present in Censys") {
		t.Errorf("stderr should name the missing host:\n%s", stderr)
	}
}

func TestFormatCSV(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.5"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.5", "--format", "csv")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}

	rows, err := csv.NewReader(strings.NewReader(stdout)).ReadAll()
	if err != nil {
		t.Fatalf("stdout is not CSV: %v\n%s", err, stdout)
	}
	if len(rows) != 2 || rows[1][0] != "198.51.100.5" || rows[1][1] != "64500" {
		t.Errorf("rows = %v", rows)
	}
}

func TestOutputFlagWritesToFile(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.5"),
	})
	path := filepath.Join(t.TempDir(), "hits.ndjson")

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.5", "--output", path)
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty when --output is a file, got %q", stdout)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path is created by this test
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "198.51.100.5") {
		t.Errorf("file missing the record:\n%s", data)
	}
}

func TestQuietSilencesStderrButKeepsResults(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.5"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.5", "--quiet")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty under --quiet", stderr)
	}
	if !strings.Contains(stdout, "198.51.100.5") {
		t.Errorf("stdout missing the record:\n%s", stdout)
	}
}

func TestAggregateStreamsBuckets(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/search/aggregate": map[string]any{"result": map[string]any{
			"buckets":     []any{map[string]any{"key": "443", "count": 12}, map[string]any{"key": "22", "count": 5}},
			"total_count": 17,
		}},
	})

	stdout, stderr, err := runCLI(t, api, "", "aggregate", "-q", "x", "--field", "services.port")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if got := strings.Count(strings.TrimRight(stdout, "\n"), "\n") + 1; got != 2 {
		t.Errorf("wrote %d buckets, want 2:\n%s", got, stdout)
	}
	if !strings.Contains(stdout, `"key":"443"`) {
		t.Errorf("stdout missing bucket key:\n%s", stdout)
	}
}

func TestCertHostsStreamsObservationRanges(t *testing.T) {
	fingerprint := strings.Repeat("ab", 32)
	api, _ := stubAPI(t, map[string]any{
		"/v3/threat-hunting/certificate/" + fingerprint + "/observations/hosts": map[string]any{"result": map[string]any{
			"ranges": []any{
				map[string]any{
					"ip": "198.51.100.1", "port": 443, "protocols": []string{"HTTP"},
					"transport_protocol": "tcp",
					"start_time":         "2026-01-01T00:00:00Z", "end_time": "2026-02-01T00:00:00Z",
				},
				map[string]any{
					"ip": "198.51.100.1", "port": 8443, "protocols": []string{"HTTP"},
					"transport_protocol": "tcp",
					"start_time":         "2026-01-01T00:00:00Z", "end_time": "2026-02-01T00:00:00Z",
				},
			},
			"total_results": 2,
		}},
	})

	stdout, stderr, err := runCLI(t, api, "", "cert-hosts", fingerprint)
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}

	var first map[string]any
	line := strings.SplitN(strings.TrimSpace(stdout), "\n", 2)[0]
	if err := json.Unmarshal([]byte(line), &first); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, line)
	}
	if first["ip"] != "198.51.100.1" || first["first_seen"] != "2026-01-01T00:00:00Z" {
		t.Errorf("record = %v", first)
	}
	// Two ranges on one host: the summary must not double-count the host.
	if !strings.Contains(stderr, "2 observation ranges across 1 unique hosts") {
		t.Errorf("stderr summary wrong:\n%s", stderr)
	}
}

func TestTimelineUsesTheRequestedWindow(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/vnd.censys.api.v3.host_timeline_event.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
			"events": []any{map[string]any{"resource": map[string]any{
				"event_time":      "2026-03-04T00:00:00Z",
				"service_scanned": map[string]any{"port": 9001},
			}}},
			"scanned_to": "2026-08-01T00:00:00Z",
		}})
	}))
	t.Cleanup(srv.Close)

	stdout, stderr, err := runCLI(t, srv.URL, "", "timeline", "198.51.100.1",
		"--since", "2026-01-01", "--until", "2026-06-01")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "service_scanned") {
		t.Errorf("stdout missing the event:\n%s", stdout)
	}
	// The API's start_time is the instant nearest to now.
	if !strings.Contains(query, "start_time=2026-06-01") || !strings.Contains(query, "end_time=2026-01-01") {
		t.Errorf("window sent as %q", query)
	}
}

func TestVersionGoesToStdout(t *testing.T) {
	stdout, _, err := runCLI(t, "http://127.0.0.1:1", "", "version")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(stdout) != "test" {
		t.Errorf("stdout = %q, want the version", stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"nope"}},
		{"search without a query", []string{"search"}},
		{"aggregate without a query", []string{"aggregate"}},
		{"cert without a fingerprint", []string{"cert"}},
		{"bad format", []string{"host", "198.51.100.1", "--format", "yaml"}},
		{"negative pages", []string{"search", "-q", "x", "--pages", "-1"}},
		{"bad --at", []string{"host", "198.51.100.1", "--at", "yesterday"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCLI(t, "http://127.0.0.1:1", "", tc.args...)
			if !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v, want ErrUsage", err)
			}
		})
	}
}

func TestGlobalFlagBeforeCommandIsKept(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host": hostsBody("198.51.100.5"),
	})

	stdout, stderr, err := runCLI(t, api, "", "--format", "csv", "host", "198.51.100.5")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if !strings.HasPrefix(stdout, "ip,asn,") {
		t.Errorf("format flag before the command was dropped:\n%s", stdout)
	}
}

func TestMissingCredentialsIsReportedClearly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvOrgID, "")
	t.Setenv(config.EnvToken, "")

	var out, msg bytes.Buffer
	err := Run(context.Background(), Env{Out: &out, Msg: &msg, In: strings.NewReader("")},
		[]string{"host", "198.51.100.1"})
	if err == nil {
		t.Fatal("Run succeeded without credentials")
	}
	if !strings.Contains(err.Error(), config.EnvToken) {
		t.Errorf("error should name the environment variable: %v", err)
	}
}
