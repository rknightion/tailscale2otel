// Package httpguard contains request-origin checks shared by local HTTP
// surfaces. It deliberately never resolves hostnames: DNS is attacker
// controlled and cannot be an authorization boundary.
package httpguard

import (
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

var corsSafelistedMediaTypes = map[string]bool{
	"application/x-www-form-urlencoded": true,
	"multipart/form-data":               true,
	"text/plain":                        true,
}

// IsLoopbackHost reports whether host names a loopback literal or localhost.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// TokenlessReceiverReason returns a closed reason when a request carries a
// browser/cross-site fingerprint that is unsafe for a tokenless loopback
// ingestion endpoint. An empty result means the request is a supported local
// non-browser shape.
func TokenlessReceiverReason(r *http.Request) string {
	if strings.TrimSpace(r.Header.Get("Origin")) != "" {
		return "origin"
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "none", "same-origin":
	default:
		return "fetch_site"
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode"))) {
	case "", "cors", "same-origin":
	default:
		return "fetch_mode"
	}
	if dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest"))); dest != "" && dest != "empty" {
		return "fetch_destination"
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || corsSafelistedMediaTypes[strings.ToLower(mediaType)] {
			return "safelisted_content_type"
		}
	}
	if !IsLoopbackHost(r.Host) {
		return "untrusted_host"
	}
	return ""
}

// SameOrigin allows non-browser clients with no Origin/Fetch Metadata and
// verifies browser requests against the request Host.
func SameOrigin(r *http.Request) bool {
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, r.Host)
}

// NoRedirectClient returns a shallow copy of base that never follows HTTP
// redirects. The caller's client is not mutated, so unrelated users retain
// their own redirect policy.
func NoRedirectClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	clone := *base
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}
