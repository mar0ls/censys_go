package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
)

// Observation renders one certificate observation range. Both the CLI and the
// interactive menu emit these, so the column order lives here rather than at
// either call site.
func Observation(r components.HostObservationRange) Record {
	first := r.StartTime.Format(time.RFC3339)
	last := r.EndTime.Format(time.RFC3339)

	return Record{
		Columns: []string{"ip", "port", "transport_protocol", "protocols", "first_seen", "last_seen"},
		Values: []string{
			r.IP,
			strconv.Itoa(r.Port),
			r.TransportProtocol,
			strings.Join(r.Protocols, ","),
			first,
			last,
		},
		Doc: map[string]any{
			"ip":                 r.IP,
			"port":               r.Port,
			"transport_protocol": r.TransportProtocol,
			"protocols":          r.Protocols,
			"first_seen":         first,
			"last_seen":          last,
		},
	}
}

// Bucket renders one aggregation bucket, carrying the aggregated field name so
// a saved file still says what it counted.
func Bucket(field string, b components.SearchAggregateResponseBucket) Record {
	return Record{
		Columns: []string{"field", "key", "count"},
		Values:  []string{field, b.Key, strconv.FormatInt(b.Count, 10)},
		Doc:     map[string]any{"field": field, "key": b.Key, "count": b.Count},
	}
}
