// Package hunt turns loosely typed operator input into the target lists the
// Censys API expects.
package hunt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
)

// MaxCIDRHosts caps how many addresses one prefix may expand to. Every expanded
// address becomes a metered lookup, so an accidental /8 must fail loudly rather
// than silently queue 16 million billable requests.
const MaxCIDRHosts = 4096

// ParseTargets turns operator input into a deduplicated list of IP addresses.
//
// Each entry may be a single IPv4/IPv6 address or a CIDR prefix, which is
// expanded. Blank lines and lines starting with '#' are ignored. Unusable
// entries are collected and returned alongside the targets that did parse, so
// one typo in a large list does not discard the rest.
func ParseTargets(entries []string) ([]string, []error) {
	var (
		targets []string
		errs    []error
		seen    = map[string]struct{}{}
	)

	add := func(addr netip.Addr) {
		s := addr.String()
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		targets = append(targets, s)
	}

	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}

		if addr, err := netip.ParseAddr(entry); err == nil {
			add(addr)
			continue
		}

		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q is neither an IP address nor a CIDR prefix", entry))
			continue
		}

		expanded, err := ExpandPrefix(prefix)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, addr := range expanded {
			add(addr)
		}
	}

	return targets, errs
}

// ExpandPrefix lists every address in a prefix, refusing prefixes wider than
// MaxCIDRHosts.
func ExpandPrefix(prefix netip.Prefix) ([]netip.Addr, error) {
	prefix = prefix.Masked()

	// Reject on the exponent before computing the count: an IPv6 prefix has more
	// host bits than a uint64 can hold, so 1<<hostBits would overflow.
	hostBits := prefix.Addr().BitLen() - prefix.Bits()
	if hostBits < 0 || hostBits > 32 || uint64(1)<<hostBits > MaxCIDRHosts {
		return nil, fmt.Errorf("%s expands to more than %d addresses; narrow the prefix", prefix, MaxCIDRHosts)
	}

	var addrs []netip.Addr
	for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// ReadTargets parses targets from a reader, one entry per line.
func ReadTargets(r io.Reader) ([]string, []error) {
	var entries []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entries = append(entries, scanner.Text())
	}
	targets, errs := ParseTargets(entries)
	if err := scanner.Err(); err != nil {
		errs = append(errs, fmt.Errorf("reading input: %w", err))
	}
	return targets, errs
}

// ReadTargetsFile parses targets from a file, one entry per line.
func ReadTargetsFile(path string) ([]string, []error) {
	if path == "" {
		return nil, []error{errors.New("no input file given")}
	}
	f, err := os.Open(path) // #nosec G304 -- the path is supplied by the operator running the tool
	if err != nil {
		return nil, []error{fmt.Errorf("opening %s: %w", path, err)}
	}
	defer func() { _ = f.Close() }()

	return ReadTargets(f)
}
