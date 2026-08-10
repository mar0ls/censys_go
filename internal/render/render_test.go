package render

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/mar0ls/censys_go/internal/censysx"
)

func ptr[T any](v T) *T { return &v }

func sampleRecords() []censysx.HostRecord {
	tcp := components.ServiceTransportProtocolTCP
	return []censysx.HostRecord{
		censysx.NewHostRecord(&components.Host{
			IP:               ptr("198.51.100.7"),
			AutonomousSystem: &components.Routing{Asn: ptr(64500), Name: ptr("BULLETPROOF-AS")},
			Location:         &components.Location{Country: ptr("Seychelles")},
			DNS:              &components.HostDNS{Names: []string{"panel.example.test"}},
			Services: []components.Service{{
				Port:              ptr(443),
				Protocol:          ptr("HTTP"),
				TransportProtocol: &tcp,
				Software:          []components.Attribute{{Product: ptr("Cobalt Strike")}},
				TLS:               &components.TLS{FingerprintSha256: ptr("deadbeef")},
				Jarm:              &components.JarmScan{Fingerprint: ptr("07d14d16d21d21d")},
			}},
		}),
		censysx.NewHostRecord(&components.Host{
			IP: ptr("198.51.100.8"),
			Services: []components.Service{
				{Port: ptr(8080)},
				{Port: ptr(80)},
			},
		}),
	}
}

func renderAll(t *testing.T, f Format) string {
	t.Helper()
	var buf bytes.Buffer
	s := NewStream(&buf, f)
	for _, rec := range sampleRecords() {
		if err := s.Host(rec); err != nil {
			t.Fatalf("Host: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.String()
}

func TestNDJSONWritesOneDocumentPerLine(t *testing.T) {
	out := renderAll(t, NDJSON)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestJSONProducesOneValidArray(t *testing.T) {
	out := renderAll(t, JSON)

	var recs []map[string]any
	if err := json.Unmarshal([]byte(out), &recs); err != nil {
		t.Fatalf("output is not a valid JSON array: %v\n%s", err, out)
	}
	if len(recs) != 2 {
		t.Errorf("got %d records, want 2", len(recs))
	}
	if recs[0]["ip"] != "198.51.100.7" {
		t.Errorf("first ip = %v", recs[0]["ip"])
	}
}

func TestJSONEmptyStreamIsStillValid(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf, JSON)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var recs []any
	if err := json.Unmarshal(buf.Bytes(), &recs); err != nil {
		t.Fatalf("empty stream is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want 0", len(recs))
	}
}

func TestCSVHasHeaderAndOneRowPerHost(t *testing.T) {
	out := renderAll(t, CSV)

	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (header + 2 hosts)", len(rows))
	}
	if rows[0][0] != "ip" {
		t.Errorf("header[0] = %q, want ip", rows[0][0])
	}
	if rows[1][0] != "198.51.100.7" || rows[1][1] != "64500" {
		t.Errorf("row = %v", rows[1])
	}
	if rows[1][6] != "deadbeef" {
		t.Errorf("cert_sha256 = %q, want deadbeef", rows[1][6])
	}
	// Ports must be sorted, not left in scan order.
	if rows[2][4] != "80,8080" {
		t.Errorf("ports = %q, want 80,8080", rows[2][4])
	}
}

func TestTableIsAlignedAndHeadered(t *testing.T) {
	out := renderAll(t, Table)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "IP") {
		t.Errorf("header = %q", lines[0])
	}
	// tabwriter pads every row to the same column offsets.
	if strings.Index(lines[1], "64500") != strings.Index(lines[0], "ASN") {
		t.Errorf("columns not aligned:\n%s", out)
	}
}

func TestValueFallsBackToJSONInTabularFormats(t *testing.T) {
	for _, f := range []Format{CSV, Table} {
		t.Run(string(f), func(t *testing.T) {
			var buf bytes.Buffer
			s := NewStream(&buf, f)
			if err := s.Value(map[string]string{"key": "value"}); err != nil {
				t.Fatalf("Value: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if !strings.Contains(buf.String(), `{"key":"value"}`) {
				t.Errorf("output missing JSON payload:\n%s", buf.String())
			}
		})
	}
}

func TestCount(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf, NDJSON)
	for _, rec := range sampleRecords() {
		if err := s.Host(rec); err != nil {
			t.Fatalf("Host: %v", err)
		}
	}
	if s.Count() != 2 {
		t.Errorf("Count() = %d, want 2", s.Count())
	}
}

func TestParseFormat(t *testing.T) {
	for _, f := range Formats {
		if got, err := ParseFormat(strings.ToUpper(string(f))); err != nil || got != f {
			t.Errorf("ParseFormat(%q) = %q, %v", f, got, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(yaml) succeeded, want an error")
	}
}
