package render

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

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

// Records that are not host-shaped must still tabulate, otherwise
// `cert-hosts --format csv` silently emits JSON into a .csv file.
func TestRecordUsesItsOwnColumns(t *testing.T) {
	rec := Observation(components.HostObservationRange{
		IP:                "198.51.100.4",
		Port:              9001,
		TransportProtocol: "tcp",
		Protocols:         []string{"HTTP", "TLS"},
		StartTime:         time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
		EndTime:           time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	})

	var buf bytes.Buffer
	s := NewStream(&buf, CSV)
	if err := s.Record(rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v\n%s", err, buf.String())
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want header + 1", len(rows))
	}
	if want := []string{"ip", "port", "transport_protocol", "protocols", "first_seen", "last_seen"}; !reflect.DeepEqual(rows[0], want) {
		t.Errorf("header = %v, want %v", rows[0], want)
	}
	if rows[1][0] != "198.51.100.4" || rows[1][1] != "9001" || rows[1][3] != "HTTP,TLS" {
		t.Errorf("row = %v", rows[1])
	}
}

// A CSV cannot change shape midway, so a record with different columns has to
// degrade rather than corrupt the file.
func TestRecordWithDifferentColumnsFallsBack(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf, CSV)
	if err := s.Host(sampleRecords()[0]); err != nil {
		t.Fatalf("Host: %v", err)
	}
	if err := s.Record(Bucket("services.port", components.SearchAggregateResponseBucket{Key: "443", Count: 12})); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "ip,asn") {
		t.Errorf("header = %q, want the host schema", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], `"key"`) {
		t.Errorf("mismatched record was not degraded to JSON: %q", lines[len(lines)-1])
	}
}

func TestBucketCarriesTheAggregatedField(t *testing.T) {
	rec := Bucket("location.country", components.SearchAggregateResponseBucket{Key: "Seychelles", Count: 97})
	if want := []string{"field", "key", "count"}; !reflect.DeepEqual(rec.Columns, want) {
		t.Errorf("Columns = %v, want %v", rec.Columns, want)
	}
	if want := []string{"location.country", "Seychelles", "97"}; !reflect.DeepEqual(rec.Values, want) {
		t.Errorf("Values = %v, want %v", rec.Values, want)
	}
}

// The table header is upper-cased for legibility; the CSV header must not be,
// or downstream tools stop recognising the field names.
func TestCSVHeaderKeepsFieldNamesVerbatim(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf, CSV)
	if err := s.Host(sampleRecords()[0]); err != nil {
		t.Fatalf("Host: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "ip,asn,as_name") {
		t.Errorf("header = %q", strings.SplitN(buf.String(), "\n", 2)[0])
	}
}

// A value with no columns still has to come out, and the file has to stay
// parseable: in CSV that means one quoted field, not a raw JSON line.
func TestValueFallsBackToJSONInTabularFormats(t *testing.T) {
	t.Run("csv", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf, CSV)
		if err := s.Value(map[string]string{"key": "value"}); err != nil {
			t.Fatalf("Value: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
		if err != nil {
			t.Fatalf("output is not valid CSV: %v\n%s", err, buf.String())
		}
		if len(rows) != 1 || rows[0][0] != `{"key":"value"}` {
			t.Errorf("rows = %v", rows)
		}
	})

	t.Run("table", func(t *testing.T) {
		var buf bytes.Buffer
		s := NewStream(&buf, Table)
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
