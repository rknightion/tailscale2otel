// Package listenaddr classifies HTTP listener bind addresses so a receiver can
// tell "only this host can reach me" from "anyone who can route to me can".
//
// It exists because two surfaces — the admin status page (#227) and the HEC
// streaming receiver (#228) — must refuse to serve without a credential when
// they are network-reachable, but stay usable with no credential on a loopback
// bind. Both need the same classification, so it lives in one leaf package
// rather than being re-derived (subtly differently) in each.
package listenaddr

import (
	"net"
	"strings"
)

// IsLoopback reports whether addr binds ONLY the loopback interface, i.e. is
// unreachable from another host.
//
// It fails CLOSED: an empty, wildcard, unparseable, or non-literal host returns
// false. A hostname is not resolved — resolution is environment-dependent and a
// DNS answer is not a security boundary — so only the "localhost" literal is
// accepted by name. A tailnet (100.64/10) address is deliberately NOT loopback:
// every peer on the tailnet can reach it.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
