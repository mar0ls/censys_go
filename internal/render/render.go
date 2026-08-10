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

// hostColumns is the column order for Table and CSV output.
var hostColumns = []string{"ip", "asn", "as_name", "country", "ports", "software", "cert_sha256", "jarm", "dns"}

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
}

// NewStream starts a stream in the given format.
func NewStream(w io.Writer, f Format) *Stream {
	s := &Stream{format: f, w: w}
	switch f {
	case NDJSON:
		s.enc = json.NewEncoder(w)
	case JSON:
		s.enc = json.NewEncoder(w)
		s.enc.SetIndent("  ", "  ")
		s.write("[\n")
	case CSV:
		s.csv = csv.NewWriter(w)
		s.err = s.csv.Write(hostColumns)
	case Table:
		s.tab = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		s.write(strings.ToUpper(strings.Join(hostColumns, "\t")) + "\n")
	}
	return s
}

// Host writes one host record.
func (s *Stream) Host(rec censysx.HostRecord) error {
	if s.err != nil {
		return s.err
	}
	switch s.format {
	case CSV:
		s.err = s.csv.Write(hostRow(rec))
	case Table:
		s.write(strings.Join(hostRow(rec), "\t") + "\n")
	default:
		return s.Value(rec)
	}
	s.count++
	return s.err
}

// Value writes an arbitrary payload. In Table and CSV formats, which have a
// fixed host-shaped schema, it falls back to compact JSON in a single column.
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
		s.err = s.enc.Encode(v)
	case NDJSON:
		s.err = s.enc.Encode(v)
	default:
		var line []byte
		line, s.err = json.Marshal(v)
		if s.err == nil {
			s.write(string(line) + "\n")
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
