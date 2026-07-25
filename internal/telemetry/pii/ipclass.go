package pii

import (
	"net/netip"
	"strings"
)

type ipClass int

const (
	ipNotIP ipClass = iota
	ipTailscale
	ipInternal
	ipExternal
)

var (
	cgnat4     = netip.MustParsePrefix("100.64.0.0/10")
	tailscale6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	rfc1918a   = netip.MustParsePrefix("10.0.0.0/8")
	rfc1918b   = netip.MustParsePrefix("172.16.0.0/12")
	rfc1918c   = netip.MustParsePrefix("192.168.0.0/16")
	ula6       = netip.MustParsePrefix("fc00::/7")
	linkLocal4 = netip.MustParsePrefix("169.254.0.0/16")
	linkLocal6 = netip.MustParsePrefix("fe80::/10")
)

// classifyIP buckets an address string. A value that does not parse as an IP
// (hostnames, sentinels like "external"/"unknown") returns ipNotIP so the caller
// falls back to the key's registry category.
//
// A value formatted as "host:port" or "[host]:port" (e.g. the default
// node-metrics identity, #198) is classified by its address portion: a bare
// netip.ParseAddr is tried first, and only on failure does a netip.ParseAddrPort
// attempt strip the port. A genuine "hostname:port" still fails both parses and
// returns ipNotIP, so it correctly falls back to the key's registry category.
//
// The value is NORMALIZED before range classification (#465): surrounding
// whitespace is trimmed and an IPv4-mapped IPv6 address is unmapped. Both are
// representation differences, not different addresses, and netip deliberately
// does NOT match a 4-in-6 address against an IPv4 prefix
// (netip.Prefix.Contains), so without Unmap the same CGNAT address written
// "::ffff:100.64.0.1" (or "::ffff:6440:1") would classify as external and
// bypass a disabled tailscale_ips category. Unmap only rewrites the ::ffff:/96
// range, so a genuine IPv6 address is untouched.
func classifyIP(s string) ipClass {
	s = strings.TrimSpace(s)
	if s == "" {
		return ipNotIP
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		addrPort, apErr := netip.ParseAddrPort(s)
		if apErr != nil {
			return ipNotIP
		}
		addr = addrPort.Addr()
	}
	addr = addr.Unmap()
	switch {
	case cgnat4.Contains(addr) || tailscale6.Contains(addr):
		return ipTailscale
	case addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return ipInternal
	case rfc1918a.Contains(addr) || rfc1918b.Contains(addr) || rfc1918c.Contains(addr):
		return ipInternal
	case ula6.Contains(addr):
		return ipInternal
	case linkLocal4.Contains(addr) || linkLocal6.Contains(addr):
		return ipInternal
	default:
		return ipExternal
	}
}
