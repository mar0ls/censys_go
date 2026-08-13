package render

import (
	"strings"
	"testing"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/mar0ls/censys_go/internal/censysx"
)

// The raw certificate runs to several kilobytes of CT entries and lint results.
// A tabular format needs the fields that identify it, not the blob.
func TestCertificateRecordPicksIdentifyingFields(t *testing.T) {
	selfSigned := false
	cert := &components.Certificate{
		FingerprintSha256: ptr("aa11"),
		EverSeenInScan:    ptr(true),
		Names:             []string{"a.test", "b.test", "c.test", "d.test"},
		Parsed: &components.CertificateParsed{
			Subject: &components.DistinguishedName{
				CommonName:   []string{"dns.google"},
				Organization: []string{"Google LLC"},
			},
			Issuer:         &components.DistinguishedName{CommonName: []string{"WR2"}},
			ValidityPeriod: &components.ValidityPeriod{NotBefore: ptr("2026-07-20T18:08:33Z"), NotAfter: ptr("2026-10-12T18:08:32Z")},
			Signature:      &components.Signature{SelfSigned: &selfSigned},
			Ja4x:           ptr("a1b2c3"),
		},
	}

	got := Certificate(cert)
	want := []string{
		"aa11", "dns.google", "Google LLC", "WR2",
		"2026-07-20T18:08:33Z", "2026-10-12T18:08:32Z",
		"false", "true", "a1b2c3", "a.test,b.test,c.test (+1 more)",
	}
	for i := range want {
		if got.Values[i] != want[i] {
			t.Errorf("column %q = %q, want %q", got.Columns[i], got.Values[i], want[i])
		}
	}
	if got.Doc != cert {
		t.Error("Doc should carry the full certificate for the JSON formats")
	}
}

// Almost every field is an optional pointer; a sparse certificate must not panic.
func TestCertificateRecordHandlesSparseInput(t *testing.T) {
	if got := Certificate(nil); got.Columns != nil {
		t.Errorf("Certificate(nil) = %+v, want a zero record", got)
	}
	got := Certificate(&components.Certificate{})
	if len(got.Values) != len(certColumns) {
		t.Fatalf("got %d values for %d columns", len(got.Values), len(certColumns))
	}
	for i, v := range got.Values {
		if v != "" {
			t.Errorf("column %q = %q, want empty", got.Columns[i], v)
		}
	}
}

func TestBalanceRowWithoutUsage(t *testing.T) {
	renews := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	got := BalanceRow(&censysx.Balance{Credits: 75, Scope: "user", Renews: &renews}, nil, 30)

	if got.Values[0] != "user" || got.Values[1] != "75" {
		t.Errorf("scope/balance = %q/%q", got.Values[0], got.Values[1])
	}
	if !strings.HasPrefix(got.Values[2], "2026-09-03") {
		t.Errorf("renews_at = %q", got.Values[2])
	}
	// A user wallet reports no usage, so those columns stay blank rather than
	// showing a misleading zero.
	for _, i := range []int{4, 5, 6} {
		if got.Values[i] != "" {
			t.Errorf("column %q = %q, want empty without a usage report", got.Columns[i], got.Values[i])
		}
	}
}

func TestBalanceRowWithUsage(t *testing.T) {
	got := BalanceRow(
		&censysx.Balance{Credits: 4200, Scope: "organization"},
		&components.CreditUsageReport{TotalConsumed: 812, TransactionCount: 37},
		30,
	)
	if got.Values[4] != "30" || got.Values[5] != "812" || got.Values[6] != "37" {
		t.Errorf("usage columns = %v", got.Values[4:])
	}
}
