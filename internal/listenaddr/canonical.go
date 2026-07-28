package listenaddr

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// wildcardHost is the canonical spelling of "every interface". The empty host,
// 0.0.0.0 and :: are three ways of writing the same bind, and Go's net.Listen
// treats them alike; collapsing them means a collision between two listeners
// cannot hide behind a difference in spelling.
//
// It is deliberately not a valid host, so a canonical form can never be handed
// back to net.Listen by mistake — this package classifies addresses, it does not
// produce them.
const wildcardHost = "*"

// Canonical returns a comparable form of a host:port listen address, or an
// error explaining why the address cannot be bound.
//
// Validation used to be a raw string comparison, which accepted anything: a
// bare "9091" with no colon, a port outside the 16-bit range, a service name.
// Every one of those is refused by net.Listen instead — on a goroutine, after
// startup, as a log line on a listener that never served (#306). Parsing here
// turns each into a refusal to start.
//
// Hostnames are lowercased but never resolved. Resolution depends on the
// resolver and the moment, so it would make the same config valid on one host
// and invalid on another; two listeners spelled with different names that
// happen to resolve alike are not detected, which is the honest limit of a
// static check.
func Canonical(addr string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("empty listen address: want host:port (e.g. 127.0.0.1:9091 or :9091 for every interface)")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: want host:port (e.g. 127.0.0.1:9091 or :9091 for every interface)", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", fmt.Errorf("invalid port %q in listen address %q: want a number between 0 and 65535 (service names are not resolved)", port, addr)
	}
	if n < 0 || n > 65535 {
		return "", fmt.Errorf("port %d in listen address %q is outside the valid range 0-65535", n, addr)
	}
	// JoinHostPort, not concatenation: an IPv6 host has to keep its brackets or
	// the canonical form is itself unparseable, and Collides splits it again.
	return net.JoinHostPort(canonicalHost(host), strconv.Itoa(n)), nil
}

// canonicalHost normalizes the host half: every wildcard spelling to one token,
// IP literals through net.IP so cosmetic differences (::1 versus 0:0:0:0:0:0:0:1)
// compare equal, and anything else lowercased as written.
func canonicalHost(host string) string {
	if host == "" {
		return wildcardHost
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return wildcardHost
		}
		return ip.String()
	}
	return strings.ToLower(host)
}

// Collides reports whether two listen addresses cannot both be bound by this
// process. Unparseable addresses never collide — Canonical reports those
// separately, and a single clear error beats two.
//
// A wildcard bind owns its port on every interface, so it collides with any
// address on the same port rather than only with an identical string. Port 0
// asks the kernel for a free port, so it never collides, not even with itself.
func Collides(a, b string) bool {
	ca, errA := Canonical(a)
	cb, errB := Canonical(b)
	if errA != nil || errB != nil {
		return false
	}
	hostA, portA, _ := net.SplitHostPort(ca)
	hostB, portB, _ := net.SplitHostPort(cb)
	if portA != portB || portA == "0" {
		return false
	}
	return hostA == hostB || hostA == wildcardHost || hostB == wildcardHost
}
