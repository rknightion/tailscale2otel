package app

import (
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/certreload"
)

// CertReloader is the shared TLS cert reloader (#316). It lives in
// internal/certreload rather than here because four listeners across three
// packages need it and internal/app already imports internal/stream, so a
// type defined here could never be reached from there.
type CertReloader = certreload.Reloader

// tlsListenerStatus maps a reloader's plain status into the admin page's DTO.
// The shared package deliberately does not import statusdata, so that a
// receiver package can reload certificates without pulling in the admin
// surface; this is the one place the two meet.
func tlsListenerStatus(s certreload.Status) statusdata.TLSListenerStatus {
	return statusdata.TLSListenerStatus{
		Name:                    s.Name,
		NotBefore:               s.NotBefore,
		NotAfter:                s.NotAfter,
		Fingerprint:             s.Fingerprint,
		LastReloadAt:            s.LastReloadAt,
		LastReloadFailureAt:     s.LastReloadFailureAt,
		LastReloadFailureReason: s.LastReloadFailureReason,
	}
}
