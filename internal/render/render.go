// Package render writes results in the shapes a pipeline or a human needs.
//
// Everything here streams: results are written as they arrive rather than
// accumulated, so a large search does not have to fit in memory before the
// first byte reaches stdout.
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mar0ls/censys_go/internal/censysx"
)

// Format selects an output encoding.
type Format string

const (
	// NDJSON writes one JSON document per line. The default: it streams, it
	// survives truncation, and jq/duckdb read it directly.
	NDJSON Format = "ndjson"
	// JSON writes a single indented JSON array.
	JSON Format = "json"
	// Table writes aligned columns for reading in a terminal.
	Table Format = "table"
	// CSV writes a header row followed by one row per host.
	CSV Format = "csv"
)

// Formats lists every supported format, for flag help and validation.
var Formats = []Format{NDJSON, JSON, Table, CSV}

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Formats {
		if f == known {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown format %q (want one of %s)", s, FormatNames())
}

// FormatNames renders the supported formats for help text.
func FormatNames() string {
	names := make([]string, len(Formats))
	for i, f := range Formats {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}

// hostColumns is the column order for host rows in Table and CSV output.
var hostColumns = []string{"ip", "asn", "as_name", "country", "ports", "software", "cert_sha256", "jarm", "dns"}

// Record is one result in both of the shapes the formats need: Doc for the JSON
// encodings, Columns and Values for the tabular ones.
type Record struct {
	Columns []string
	Values  []string
	Doc     any
}

// Stream writes records incrementally in one format. Callers must call Close to
// finish the document; for JSON and Table that is where the trailing structure
// is emitted.
type Stream struct {
	format Format
	w      io.Writer

	enc   *json.Encoder
	csv   *csv.Writer
	tab   *tabwriter.Writer
	count int
	err   error

	// header is written on the first tabular record rather than up front, so
	// the columns can come from whatever kind of record the caller emits.
	header []string
}

// NewStream starts a stream in the given format.
func NewStream(w io.Writer, f Format) *Stream {
	s := &Stream{format: f, w: w}
	switch f {
	case NDJSON:
		s.enc = json.NewEncoder(w)
	case JSON:
		s.write("[\n")
	case CSV:
		s.csv = csv.NewWriter(w)
	case Table:
		s.tab = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	}
	return s
}

// Host writes one host record.
func (s *Stream) Host(rec censysx.HostRecord) error {
	return s.Record(Record{Columns: hostColumns, Values: hostRow(rec), Doc: rec})
}

// Record writes one result. Tabular formats use Columns and Values, emitting the
// header on the first record; the JSON formats use Doc.
//
// Once a stream has emitted a header, records with different columns fall back
// to compact JSON in a single field, because a CSV cannot change shape midway.
func (s *Stream) Record(r Record) error {
	if s.err != nil {
		return s.err
	}

	switch s.format {
	case CSV, Table:
		if len(r.Columns) == 0 {
			return s.Value(r.Doc)
		}
		if s.header == nil {
			s.header = r.Columns
			// Upper case reads better in a terminal table, but a CSV header has
			// to stay verbatim so downstream tools see the real field names.
			if s.format == Table {
				s.writeRow(upper(r.Columns))
			} else {
				s.writeRow(r.Columns)
			}
		} else if !sameColumns(s.header, r.Columns) {
			return s.Value(r.Doc)
		}
		s.writeRow(r.Values)
		s.count++
		return s.err
	default:
		return s.Value(r.Doc)
	}
}

// Value writes an arbitrary payload. In Table and CSV formats it falls back to
// compact JSON in a single field, since such a value has no declared columns.
func (s *Stream) Value(v any) error {
	if s.err != nil {
		return s.err
	}
	switch s.format {
	case JSON:
		if s.count > 0 {
			s.write(",\n")
		}
		s.write("  ")
		// Encode appends its own newline, which would double up against the
		// separator written before the next element and before the closing
		// bracket, so trim it back off.
		var encoded []byte
		encoded, s.err = json.MarshalIndent(v, "  ", "  ")
		if s.err == nil {
			s.write(string(encoded))
		}
	case NDJSON:
		s.err = s.enc.Encode(v)
	default:
		var line []byte
		line, s.err = json.Marshal(v)
		if s.err == nil {
			// Route this through the same writer the tabular rows use. The csv
			// package buffers until Flush, so writing straight to s.w here would
			// land the fallback ahead of rows emitted before it.
			s.writeRow([]string{string(line)})
		}
	}
	s.count++
	return s.err
}

// Count reports how many records have been written.
func (s *Stream) Count() int { return s.count }

// Close finishes the document and flushes any buffered writer.
func (s *Stream) Close() error {
	switch s.format {
	case JSON:
		if s.count > 0 {
			s.write("\n")
		}
		s.write("]\n")
	case CSV:
		s.csv.Flush()
		if err := s.csv.Error(); err != nil && s.err == nil {
			s.err = err
		}
	case Table:
		if err := s.tab.Flush(); err != nil && s.err == nil {
			s.err = err
		}
	}
	return s.err
}

// writeRow emits one tabular row in whichever of CSV or Table is active.
func (s *Stream) writeRow(values []string) {
	if s.err != nil {
		return
	}
	if s.format == CSV {
		s.err = s.csv.Write(values)
		return
	}
	s.write(strings.Join(values, "\t") + "\n")
}

func upper(columns []string) []string {
	out := make([]string, len(columns))
	for i, c := range columns {
		out[i] = strings.ToUpper(c)
	}
	return out
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// write appends to the active writer, remembering the first failure.
func (s *Stream) write(str string) {
	if s.err != nil {
		return
	}
	dst := s.w
	if s.tab != nil {
		dst = s.tab
	}
	_, s.err = io.WriteString(dst, str)
}

// hostRow flattens a record into the columns declared by hostColumns.
func hostRow(rec censysx.HostRecord) []string {
	ports := make([]string, 0, len(rec.Services))
	for _, p := range rec.Ports() {
		ports = append(ports, strconv.Itoa(p))
	}

	software := map[string]struct{}{}
	for _, svc := range rec.Services {
		if svc.Software != "" {
			software[svc.Software] = struct{}{}
		}
	}
	products := make([]string, 0, len(software))
	for name := range software {
		products = append(products, name)
	}
	sort.Strings(products)

	asn := ""
	if rec.ASN != 0 {
		asn = strconv.Itoa(rec.ASN)
	}

	return []string{
		rec.IP,
		asn,
		rec.ASName,
		rec.Country,
		strings.Join(ports, ","),
		strings.Join(products, "; "),
		strings.Join(rec.CertHashes(), ","),
		strings.Join(rec.JARMHashes(), ","),
		strings.Join(rec.DNSNames, ","),
	}
}
