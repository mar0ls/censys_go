package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/censys/censys-sdk-go/models/components"

	"github.com/mar0ls/censys_go/internal/censysx"
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

// certColumns is the column order for certificate rows.
var certColumns = []string{
	"fingerprint_sha256", "subject_cn", "subject_org", "issuer_cn",
	"not_before", "not_after", "self_signed", "seen_in_scan", "ja4x", "names",
}

// Certificate renders one certificate. The raw record runs to several kilobytes
// of CT log entries and lint results, which is not what a tabular format is
// for; this keeps the fields that identify the certificate and the two that can
// be pivoted on, self_signed and ja4x.
func Certificate(c *components.Certificate) Record {
	if c == nil {
		return Record{}
	}

	var (
		subjectCN, subjectOrg, issuerCN string
		notBefore, notAfter, ja4x       string
		selfSigned                      string
	)

	if parsed := c.GetParsed(); parsed != nil {
		if subject := parsed.GetSubject(); subject != nil {
			subjectCN = strings.Join(subject.GetCommonName(), ",")
			subjectOrg = strings.Join(subject.GetOrganization(), ",")
		}
		if issuer := parsed.GetIssuer(); issuer != nil {
			issuerCN = strings.Join(issuer.GetCommonName(), ",")
		}
		if validity := parsed.GetValidityPeriod(); validity != nil {
			notBefore = derefString(validity.GetNotBefore())
			notAfter = derefString(validity.GetNotAfter())
		}
		if sig := parsed.GetSignature(); sig != nil && sig.GetSelfSigned() != nil {
			selfSigned = strconv.FormatBool(*sig.GetSelfSigned())
		}
		ja4x = derefString(parsed.GetJa4x())
	}

	seen := ""
	if c.GetEverSeenInScan() != nil {
		seen = strconv.FormatBool(*c.GetEverSeenInScan())
	}

	return Record{
		Columns: certColumns,
		Values: []string{
			derefString(c.GetFingerprintSha256()),
			subjectCN,
			subjectOrg,
			issuerCN,
			notBefore,
			notAfter,
			selfSigned,
			seen,
			ja4x,
			truncateList(c.GetNames(), maxTabularDNSNames),
		},
		Doc: c,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// balanceColumns is the column order for a credit balance row.
var balanceColumns = []string{"scope", "balance", "renews_at", "expires_at", "window_days", "consumed", "transactions"}

// BalanceRow renders a credit balance, optionally with the usage figures that
// only an organization wallet reports. Columns make the balance trackable from
// a scheduled job appending to a CSV.
func BalanceRow(b *censysx.Balance, usage *components.CreditUsageReport, windowDays int) Record {
	if b == nil {
		return Record{}
	}

	values := []string{b.Scope, strconv.FormatInt(b.Credits, 10), formatTime(b.Renews), formatTime(b.Expires), "", "", ""}
	doc := map[string]any{"scope": b.Scope, "balance": b.Credits}
	if b.Renews != nil {
		doc["renews_at"] = b.Renews.Format(time.RFC3339)
	}
	if b.Expires != nil {
		doc["expires_at"] = b.Expires.Format(time.RFC3339)
	}

	if usage != nil {
		values[4] = strconv.Itoa(windowDays)
		values[5] = strconv.FormatInt(usage.TotalConsumed, 10)
		values[6] = strconv.FormatInt(usage.TransactionCount, 10)
		doc["window_days"] = windowDays
		doc["consumed"] = usage.TotalConsumed
		doc["transactions"] = usage.TransactionCount
		doc["consumed_api"] = usage.CreditsConsumedBySource.API
		doc["consumed_ui"] = usage.CreditsConsumedBySource.UI
	}

	return Record{Columns: balanceColumns, Values: values, Doc: doc}
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
