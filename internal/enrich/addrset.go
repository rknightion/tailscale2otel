package enrich

import "net/netip"

// AddrSet classifies addresses against a fixed collection of tailnet prefixes.
// NewAddrSet copies its input, so an AddrSet is safe to share between the
// device cache and node-metrics discovery without later caller mutation.
type AddrSet struct {
	prefixes []netip.Prefix
}

var defaultAddrSet = NewAddrSet(
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
)

// NewAddrSet returns a set containing prefixes. Prefix validation belongs at
// the configuration boundary: callers using this set as an outbound-address
// allowlist must supply only the validated private, CGNAT, or ULA prefixes.
func NewAddrSet(prefixes ...netip.Prefix) AddrSet {
	return AddrSet{prefixes: append([]netip.Prefix(nil), prefixes...)}
}

// DefaultAddrSet returns the Tailscale IPv4 CGNAT and IPv6 ULA ranges. The
// returned value exposes no mutable prefix storage and is safe to copy.
func DefaultAddrSet() AddrSet {
	return defaultAddrSet
}

// Contains reports whether addr falls within any prefix in the set.
func (s AddrSet) Contains(addr netip.Addr) bool {
	for _, prefix := range s.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// IsTailscaleAddr reports whether addr falls within Tailscale's default address
// ranges (the IPv4 CGNAT block 100.64.0.0/10 and IPv6 ULA block
// fd7a:115c:a1e0::/48).
func IsTailscaleAddr(addr netip.Addr) bool {
	return DefaultAddrSet().Contains(addr)
}
