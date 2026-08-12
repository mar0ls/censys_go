package hunt

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestParseTargetsAcceptsAddressesAndPrefixes(t *testing.T) {
	got, errs := ParseTargets([]string{
		"198.51.100.1",
		"  198.51.100.2  ",
		"# a comment",
		"",
		"2001:db8::1",
		"203.0.113.0/30",
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	want := []string{
		"198.51.100.1",
		"198.51.100.2",
		"2001:db8::1",
		"203.0.113.0", "203.0.113.1", "203.0.113.2", "203.0.113.3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTargets() = %v\nwant %v", got, want)
	}
}

// Every duplicate is a duplicate billed lookup, whether it came in verbatim or
// via an overlapping prefix.
func TestParseTargetsDeduplicates(t *testing.T) {
	got, errs := ParseTargets([]string{
		"198.51.100.1",
		"198.51.100.1",
		"198.51.100.0/31", // contains .0 and .1
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []string{"198.51.100.1", "198.51.100.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTargets() = %v, want %v", got, want)
	}
}

func TestParseTargetsReportsBadEntriesButKeepsGoing(t *testing.T) {
	got, errs := ParseTargets([]string{
		"198.51.100.1",
		"not-an-ip",
		"999.999.999.999",
		"198.51.100.2",
	})

	if want := []string{"198.51.100.1", "198.51.100.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(errs), errs)
	}
}

func TestParseTargetsRejectsOversizedPrefix(t *testing.T) {
	got, errs := ParseTargets([]string{"10.0.0.0/8"})
	if len(got) != 0 {
		t.Errorf("expanded %d addresses, want 0", len(got))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "narrow the prefix") {
		t.Errorf("error = %v, want a size complaint", errs[0])
	}
}

// An IPv6 prefix has far more host bits than fit in a uint64 shift; the guard
// must reject it rather than overflow.
func TestExpandPrefixRejectsHugeIPv6Prefix(t *testing.T) {
	if _, err := ExpandPrefix(netip.MustParsePrefix("2001:db8::/32")); err == nil {
		t.Error("ExpandPrefix accepted a /32 IPv6 prefix")
	}
}

func TestExpandPrefixMasksHostBits(t *testing.T) {
	got, err := ExpandPrefix(netip.MustParsePrefix("203.0.113.5/30"))
	if err != nil {
		t.Fatalf("ExpandPrefix: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d addresses, want 4", len(got))
	}
	if got[0].String() != "203.0.113.4" {
		t.Errorf("first address = %s, want 203.0.113.4", got[0])
	}
}

func TestExpandPrefixSingleHost(t *testing.T) {
	got, err := ExpandPrefix(netip.MustParsePrefix("198.51.100.9/32"))
	if err != nil {
		t.Fatalf("ExpandPrefix: %v", err)
	}
	if len(got) != 1 || got[0].String() != "198.51.100.9" {
		t.Errorf("got %v, want [198.51.100.9]", got)
	}
}

func TestReadTargets(t *testing.T) {
	got, errs := ReadTargets(strings.NewReader("198.51.100.1\n# comment\n\n198.51.100.2\n"))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := []string{"198.51.100.1", "198.51.100.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ReadTargets() = %v, want %v", got, want)
	}
}

func TestReadTargetsFileMissing(t *testing.T) {
	if _, errs := ReadTargetsFile("/nonexistent/ips.txt"); len(errs) == 0 {
		t.Error("ReadTargetsFile succeeded on a missing file")
	}
}
