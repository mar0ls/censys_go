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

func hostBody(ip string) map[string]any {
	return map[string]any{"result": map[string]any{"resource": map[string]any{
		"ip":                ip,
		"autonomous_system": map[string]any{"asn": 64500, "name": "TEST-AS"},
		"location":          map[string]any{"country": "Poland"},
		"services":          []any{map[string]any{"port": 443, "protocol": "HTTP"}},
	}}}
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
		"/v3/global/asset/host/198.51.100.5": hostBody("198.51.100.5"),
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

func TestHostExpandsCIDRArgument(t *testing.T) {
	api, paths := stubAPI(t, map[string]any{
		"/v3/global/asset/host/203.0.113.0": hostBody("203.0.113.0"),
		"/v3/global/asset/host/203.0.113.1": hostBody("203.0.113.1"),
	})

	_, stderr, err := runCLI(t, api, "", "host", "203.0.113.0/31")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if len(*paths) != 2 {
		t.Errorf("made %d requests, want 2: %v", len(*paths), *paths)
	}
}

// One dead host must not abort the rest of the list.
func TestHostContinuesPastNotFound(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host/198.51.100.2": hostBody("198.51.100.2"),
	})

	stdout, stderr, err := runCLI(t, api, "", "host", "198.51.100.1", "198.51.100.2")
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if got := strings.Count(stdout, "\n"); got != 1 {
		t.Errorf("wrote %d records, want 1:\n%s", got, stdout)
	}
	if !strings.Contains(stderr, "not present in Censys") {
		t.Errorf("stderr missing the not-found note:\n%s", stderr)
	}
	if !strings.Contains(stderr, "1 hosts written, 1 failed") {
		t.Errorf("stderr missing the tally:\n%s", stderr)
	}
}

func TestFormatCSV(t *testing.T) {
	api, _ := stubAPI(t, map[string]any{
		"/v3/global/asset/host/198.51.100.5": hostBody("198.51.100.5"),
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
		"/v3/global/asset/host/198.51.100.5": hostBody("198.51.100.5"),
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
		"/v3/global/asset/host/198.51.100.5": hostBody("198.51.100.5"),
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
		"/v3/global/asset/host/198.51.100.5": hostBody("198.51.100.5"),
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
