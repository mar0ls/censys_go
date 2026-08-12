package censysx

import (
	"reflect"
	"testing"

	"github.com/censys/censys-sdk-go/models/components"
)

func ptr[T any](v T) *T { return &v }

func testHost() *components.Host {
	tcp := components.ServiceTransportProtocolTCP
	return &components.Host{
		IP: ptr("203.0.113.10"),
		AutonomousSystem: &components.Routing{
			Asn:  ptr(64496),
			Name: ptr("EXAMPLE-AS"),
		},
		Location: &components.Location{
			Country: ptr("Poland"),
			City:    ptr("Warsaw"),
		},
		DNS: &components.HostDNS{Names: []string{"c2.example.test", ""}},
		Services: []components.Service{
			{
				Port:              ptr(443),
				Protocol:          ptr("HTTP"),
				TransportProtocol: &tcp,
				Software: []components.Attribute{
					{Vendor: ptr("nginx"), Product: ptr("nginx"), Update: ptr("1.25.3")},
				},
				TLS: &components.TLS{
					FingerprintSha256: ptr("aa11"),
					Ja4s:              ptr("t130200_1301_a56c5b993250"),
				},
				Jarm: &components.JarmScan{Fingerprint: ptr("29d3fd00029d29d00042d43d00041d")},
			},
			{
				Port:              ptr(22),
				Protocol:          ptr("SSH"),
				TransportProtocol: &tcp,
				// Same certificate as :443 — the flattener must not report it twice.
				TLS: &components.TLS{FingerprintSha256: ptr("aa11")},
			},
		},
	}
}

func TestNewHostRecordFlattensTypedFields(t *testing.T) {
	got := NewHostRecord(testHost())

	if got.IP != "203.0.113.10" {
		t.Errorf("IP = %q", got.IP)
	}
	if got.ASN != 64496 || got.ASName != "EXAMPLE-AS" {
		t.Errorf("AS = %d/%q, want 64496/EXAMPLE-AS", got.ASN, got.ASName)
	}
	if got.Country != "Poland" || got.City != "Warsaw" {
		t.Errorf("location = %q/%q", got.Country, got.City)
	}
	if want := []string{"c2.example.test"}; !reflect.DeepEqual(got.DNSNames, want) {
		t.Errorf("DNSNames = %v, want %v (empty names must be dropped)", got.DNSNames, want)
	}
	if len(got.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(got.Services))
	}

	https := got.Services[0]
	if https.Port != 443 || https.Protocol != "HTTP" || https.Transport != "tcp" {
		t.Errorf("service[0] = %+v", https)
	}
	if https.Software != "nginx nginx 1.25.3" {
		t.Errorf("Software = %q, want %q", https.Software, "nginx nginx 1.25.3")
	}
	if https.JARM == "" || https.JA4S == "" || https.CertSHA256 != "aa11" {
		t.Errorf("pivot fields not populated: %+v", https)
	}
}

// A host with no optional sections must flatten without panicking; the SDK
// leaves almost every field as a nil pointer.
func TestNewHostRecordHandlesSparseHost(t *testing.T) {
	got := NewHostRecord(&components.Host{})
	if got.IP != "" || got.ASN != 0 || len(got.Services) != 0 {
		t.Errorf("empty host produced %+v", got)
	}
	if got := NewHostRecord(nil); got.IP != "" {
		t.Errorf("nil host produced %+v", got)
	}
}

func TestHostRecordPortsAreSorted(t *testing.T) {
	got := NewHostRecord(testHost()).Ports()
	if want := []int{22, 443}; !reflect.DeepEqual(got, want) {
		t.Errorf("Ports() = %v, want %v", got, want)
	}
}

func TestHostRecordCertHashesDeduplicates(t *testing.T) {
	got := NewHostRecord(testHost()).CertHashes()
	if want := []string{"aa11"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CertHashes() = %v, want %v", got, want)
	}
}

func TestIPsFromHitsDropsNonHostsAndDuplicates(t *testing.T) {
	hits := []components.SearchQueryHit{
		{HostV1: &components.HostAssetWithMatchedServices{Resource: components.Host{IP: ptr("198.51.100.1")}}},
		{CertificateV1: &components.CertificateAsset{}},
		{HostV1: &components.HostAssetWithMatchedServices{Resource: components.Host{IP: ptr("198.51.100.1")}}},
		{HostV1: &components.HostAssetWithMatchedServices{Resource: components.Host{IP: ptr("198.51.100.2")}}},
		{HostV1: &components.HostAssetWithMatchedServices{Resource: components.Host{}}},
	}

	got := IPsFromHits(hits)
	want := []string{"198.51.100.1", "198.51.100.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IPsFromHits() = %v, want %v", got, want)
	}
}

func TestHostRecordsFromHitsSkipsNonHostHits(t *testing.T) {
	hits := []components.SearchQueryHit{
		{CertificateV1: &components.CertificateAsset{}},
		{HostV1: &components.HostAssetWithMatchedServices{Resource: *testHost()}},
		{WebpropertyV1: &components.WebpropertyAsset{}},
	}

	got := HostRecordsFromHits(hits)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if got[0].IP != "203.0.113.10" {
		t.Errorf("IP = %q", got[0].IP)
	}
}
