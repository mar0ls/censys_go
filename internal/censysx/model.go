package censysx

import (
	"sort"
	"strings"

	"github.com/censys/censys-sdk-go/models/components"
)

// HostRecord is a flattened view of a host, holding the fields worth putting in
// a table, a CSV row, or a pivot query. The full asset is kept in Raw for
// callers that need everything.
type HostRecord struct {
	IP       string          `json:"ip"`
	ASN      int             `json:"asn,omitempty"`
	ASName   string          `json:"as_name,omitempty"`
	Country  string          `json:"country,omitempty"`
	City     string          `json:"city,omitempty"`
	DNSNames []string        `json:"dns_names,omitempty"`
	Services []ServiceRecord `json:"services,omitempty"`

	Raw *components.Host `json:"-"`
}

// ServiceRecord is one exposed service, reduced to the fields that identify it
// and the fingerprints that can be pivoted on.
type ServiceRecord struct {
	Port      int    `json:"port"`
	Transport string `json:"transport,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Software  string `json:"software,omitempty"`

	// Pivot material: each of these can be fed straight back into a CenQL query
	// to find other hosts sharing the same artifact.
	CertSHA256 string `json:"cert_sha256,omitempty"`
	JARM       string `json:"jarm,omitempty"`
	JA3S       string `json:"ja3s,omitempty"`
	JA4S       string `json:"ja4s,omitempty"`
	BannerHash string `json:"banner_sha256,omitempty"`
}

// Ports returns the host's distinct open ports in ascending order. A port can
// carry more than one service — 443 answering both HTTP and something
// unidentified, 53 over TCP and UDP — and listing it twice says nothing.
func (h HostRecord) Ports() []int {
	seen := make(map[int]struct{}, len(h.Services))
	ports := make([]int, 0, len(h.Services))
	for _, s := range h.Services {
		if s.Port == 0 {
			continue
		}
		if _, dup := seen[s.Port]; dup {
			continue
		}
		seen[s.Port] = struct{}{}
		ports = append(ports, s.Port)
	}
	sort.Ints(ports)
	return ports
}

// Scanned reports whether Censys observed any service on the host.
//
// The API returns a record for almost any address: 240.0.0.1, which is not even
// routable, comes back with 73 DNS names and nothing else, and an unscanned
// address inside a live prefix comes back with routing and location but no
// services. Neither is a live host, so callers can tell them from one.
func (h HostRecord) Scanned() bool {
	return len(h.Services) > 0
}

// CertHashes returns the distinct certificate SHA-256 digests served by the host.
func (h HostRecord) CertHashes() []string {
	return distinct(func(yield func(string)) {
		for _, s := range h.Services {
			yield(s.CertSHA256)
		}
	})
}

// JARMHashes returns the distinct JARM fingerprints observed on the host.
func (h HostRecord) JARMHashes() []string {
	return distinct(func(yield func(string)) {
		for _, s := range h.Services {
			yield(s.JARM)
		}
	})
}

// NewHostRecord flattens a Host into a HostRecord using the SDK's typed getters.
func NewHostRecord(host *components.Host) HostRecord {
	if host == nil {
		return HostRecord{}
	}
	rec := HostRecord{
		IP:  deref(host.GetIP()),
		Raw: host,
	}
	if as := host.GetAutonomousSystem(); as != nil {
		rec.ASN = deref(as.GetAsn())
		rec.ASName = deref(as.GetName())
	}
	if loc := host.GetLocation(); loc != nil {
		rec.Country = deref(loc.GetCountry())
		rec.City = deref(loc.GetCity())
	}
	if dns := host.GetDNS(); dns != nil {
		for _, n := range dns.GetNames() {
			if n != "" {
				rec.DNSNames = append(rec.DNSNames, n)
			}
		}
	}
	for i := range host.Services {
		rec.Services = append(rec.Services, newServiceRecord(&host.Services[i]))
	}
	return rec
}

func newServiceRecord(svc *components.Service) ServiceRecord {
	rec := ServiceRecord{
		Port:       deref(svc.GetPort()),
		Protocol:   deref(svc.GetProtocol()),
		BannerHash: deref(svc.GetBannerHashSha256()),
	}
	if tp := svc.GetTransportProtocol(); tp != nil {
		rec.Transport = string(*tp)
	}
	if sw := svc.GetSoftware(); len(sw) > 0 {
		rec.Software = softwareLabel(sw[0])
	}
	// The digest appears in two places. host.services.cert.fingerprint_sha256 is
	// the field the docs point at for pivoting and the one DefaultSearchFields
	// requests; the TLS handshake object carries the same value and covers a
	// caller who asked for the full record instead.
	if cert := svc.GetCert(); cert != nil {
		rec.CertSHA256 = deref(cert.GetFingerprintSha256())
	}
	if tls := svc.GetTLS(); tls != nil {
		if rec.CertSHA256 == "" {
			rec.CertSHA256 = deref(tls.GetFingerprintSha256())
		}
		rec.JA3S = deref(tls.GetJa3s())
		rec.JA4S = deref(tls.GetJa4s())
	}
	if jarm := svc.GetJarm(); jarm != nil {
		rec.JARM = deref(jarm.GetFingerprint())
	}
	return rec
}

// softwareLabel renders an Attribute as "vendor product version", skipping the
// parts the scan did not identify.
func softwareLabel(a components.Attribute) string {
	parts := make([]string, 0, 3)
	for _, p := range []*string{a.GetVendor(), a.GetProduct(), a.GetUpdate()} {
		if v := deref(p); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// HostRecordsFromHits flattens search hits, keeping only host results. Certificate
// and web-property hits are skipped; a query can return a mix of all three.
func HostRecordsFromHits(hits []components.SearchQueryHit) []HostRecord {
	records := make([]HostRecord, 0, len(hits))
	for i := range hits {
		hostHit := hits[i].GetHostV1()
		if hostHit == nil {
			continue
		}
		records = append(records, NewHostRecord(&hostHit.Resource))
	}
	return records
}

// IPsFromHits returns the distinct IPs in a set of search hits, in result order.
func IPsFromHits(hits []components.SearchQueryHit) []string {
	return distinct(func(yield func(string)) {
		for i := range hits {
			if hostHit := hits[i].GetHostV1(); hostHit != nil {
				yield(deref(hostHit.Resource.GetIP()))
			}
		}
	})
}

// distinct collects the non-empty strings produced by push, dropping duplicates
// and preserving first-seen order.
func distinct(push func(yield func(string))) []string {
	seen := map[string]struct{}{}
	var out []string
	push(func(v string) {
		if v == "" {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	})
	return out
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
