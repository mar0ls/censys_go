package censysx

import (
	"strings"
	"testing"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
)

func TestNormalizeFingerprint(t *testing.T) {
	valid := strings.Repeat("ab", 32)

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercase", valid, valid, false},
		{"uppercase is normalized", strings.ToUpper(valid), valid, false},
		{"surrounding space", "  " + valid + "\n", valid, false},
		{"empty", "", "", true},
		{"too short", strings.Repeat("a", 63), "", true},
		{"too long", strings.Repeat("a", 65), "", true},
		{"non-hex", strings.Repeat("z", 64), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeFingerprint(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNextExpiryPicksSoonest(t *testing.T) {
	early := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	credits := &components.OrganizationCredits{
		CreditExpirations: []components.CreditExpiration{
			{Balance: 10, ExpiresAt: &late},
			{Balance: 20, ExpiresAt: &early},
			{Balance: 30}, // no expiry set — must be ignored
		},
	}

	got := NextExpiry(credits)
	if got == nil {
		t.Fatal("NextExpiry() = nil")
	}
	if !got.ExpiresAt.Equal(early) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, early)
	}
}

func TestNextExpiryWithoutExpirations(t *testing.T) {
	if got := NextExpiry(nil); got != nil {
		t.Errorf("NextExpiry(nil) = %+v, want nil", got)
	}
	if got := NextExpiry(&components.OrganizationCredits{}); got != nil {
		t.Errorf("NextExpiry(empty) = %+v, want nil", got)
	}
}
