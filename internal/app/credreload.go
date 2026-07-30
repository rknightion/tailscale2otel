package app

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/credreload"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Outbound credential/TLS rotation (#362). Kubernetes and Docker rotate a
// projected secret or an mTLS keypair in place; before this, picking up the new
// material cost a process restart and the telemetry gap that comes with it.
//
// Two deliberate shapes:
//
//   - The reloader is built whenever a watched FILE is configured, regardless of
//     credential_reload.enabled. `enabled` governs only the background poller.
//     That split matters: the last-known-good guarantee — a malformed
//     replacement never clobbers working material — is what stops a bad rotation
//     taking exporting down, and it should not be something an operator has to
//     opt into. `enabled: false` means "I will trigger reloads myself", not
//     "validate nothing".
//   - Construction failure is FATAL, matching how every other credential path in
//     this app treats an unreadable secret at startup. Failing open here would
//     start a process that exports nothing and says nothing about why.
type credReloaders struct {
	otlp      *credreload.Reloader // nil when no OTLP credential file is configured
	pyroscope *credreload.Reloader // nil when no Pyroscope credential file is configured
}

// gcTokenSentinel is the internal HeaderFiles key the Grafana Cloud token file
// is watched under.
//
// It exists because that token is not a header value: it is combined with the
// instance ID into "Authorization: Basic base64(id:token)". credreload watches
// files and hands back their trimmed contents keyed by header name, which is
// exactly the wrong shape for a value that needs transforming — so the raw
// content is carried under this sentinel and converted in the DynamicHeaders
// closure, which then DELETES the sentinel so it can never be sent as a real
// header. The name is not a valid HTTP header anyone would set, so a collision
// with an operator-supplied header is not possible.
const gcTokenSentinel = "X-Tailscale2otel-Internal-GrafanaCloud-Token" //nolint:gosec // G101: a map KEY naming where the credential lives, not a credential

// otlpCredSources derives the watched-file set from the OTLP config. Only
// FILE-bearing fields participate: an inline header value or an inline Grafana
// Cloud token has nothing to watch, so it stays on the static path.
//
// The Grafana Cloud token file is the case that matters most — it is how nearly
// every deployment authenticates, and it is exactly what a secret rotation
// replaces — so watching it is the difference between #362 being useful and
// being a TLS-only curiosity.
func otlpCredSources(cfg *config.Config) credreload.Sources {
	src := credreload.Sources{
		CAFile:             cfg.OTLP.TLS.CAFile,
		CertFile:           cfg.OTLP.TLS.CertFile,
		KeyFile:            cfg.OTLP.TLS.KeyFile,
		InsecureSkipVerify: cfg.OTLP.TLS.InsecureSkipVerify,
	}
	if f := cfg.OTLP.GrafanaCloud.TokenFile; f != "" {
		src.HeaderFiles = map[string]string{gcTokenSentinel: f}
	}
	return src
}

// pyroscopeCredSources derives the watched-file set for the profile-upload
// client.
func pyroscopeCredSources(cfg *config.Config) credreload.Sources {
	p := cfg.Profiling.Pyroscope
	return credreload.Sources{
		CAFile:             p.TLS.CAFile,
		CertFile:           p.TLS.CertFile,
		KeyFile:            p.TLS.KeyFile,
		InsecureSkipVerify: p.TLS.InsecureSkipVerify,
	}
}

// newCredReloaders builds the reloaders the configuration actually needs. A
// source set with no watched file yields a nil reloader, and every downstream
// caller treats nil as "stay on the static path" — so a deployment that
// configures no file pays nothing and behaves exactly as before.
func newCredReloaders(cfg *config.Config, logger *slog.Logger) (*credReloaders, error) {
	out := &credReloaders{}

	if src := otlpCredSources(cfg); src.WatchesAnything() {
		r, err := credreload.New(credreload.Options{
			Sources:  src,
			Interval: pollInterval(cfg.OTLP.CredentialReload),
			Logger:   logger,
		})
		if err != nil {
			return nil, fmt.Errorf("otlp credential reload: %w", err)
		}
		out.otlp = r
	}

	if src := pyroscopeCredSources(cfg); src.WatchesAnything() && cfg.Profiling.Pyroscope.Enabled {
		r, err := credreload.New(credreload.Options{
			Sources:  src,
			Interval: pollInterval(cfg.Profiling.Pyroscope.CredentialReload),
			Logger:   logger,
		})
		if err != nil {
			return nil, fmt.Errorf("profiling.pyroscope credential reload: %w", err)
		}
		out.pyroscope = r
	}
	return out, nil
}

// pollInterval returns the poller period, or 0 to disable the poller. Validate()
// already rejects an enabled poller with a non-positive or sub-floor interval,
// so this only has to honor the enabled flag.
func pollInterval(c config.CredentialReloadConfig) time.Duration {
	if !c.Enabled {
		return 0
	}
	return c.Interval.D()
}

// Start starts every configured poller. Safe on a nil receiver and on a
// reloader whose interval is 0 (Start is a documented no-op there).
func (c *credReloaders) Start() {
	if c == nil {
		return
	}
	for _, r := range []*credreload.Reloader{c.otlp, c.pyroscope} {
		if r != nil {
			r.Start()
		}
	}
}

// Stop stops every configured poller and waits for its goroutine to exit.
func (c *credReloaders) Stop() {
	if c == nil {
		return
	}
	for _, r := range []*credreload.Reloader{c.otlp, c.pyroscope} {
		if r != nil {
			r.Stop()
		}
	}
}

// applyDynamicOTLPCredentials attaches the reloader to telemetry.Options so the
// exporters read the current material per request/handshake instead of the copy
// baked in at construction. A nil reloader leaves opts untouched.
//
// static is the header map already assembled by telemetryOptions (Grafana Cloud
// Basic auth plus any inline headers). The reloader's own header set is layered
// ON TOP of it rather than replacing it, because the two carry different things:
// static holds inline values that cannot rotate, the reloader holds file-backed
// ones that can.
func applyDynamicOTLPCredentials(opts *telemetry.Options, r *credreload.Reloader, static map[string]string, gcInstanceID string) {
	if r == nil {
		return
	}
	if len(r.Headers()) > 0 {
		opts.DynamicHeaders = func() map[string]string {
			cur := r.Headers()
			merged := make(map[string]string, len(static)+len(cur))
			for k, v := range static {
				merged[k] = v
			}
			// File-backed values win: they are the ones that rotate, so on a
			// collision the freshly-read value is the correct one.
			for k, v := range cur {
				merged[k] = v
			}
			// Transform the Grafana Cloud token into the Basic header it actually
			// is, and drop the sentinel so it never goes out on the wire.
			if tok, ok := merged[gcTokenSentinel]; ok {
				delete(merged, gcTokenSentinel)
				if gcInstanceID != "" {
					merged["Authorization"] = "Basic " +
						base64.StdEncoding.EncodeToString([]byte(gcInstanceID+":"+tok))
				}
			}
			return merged
		}
	}
	if tc := r.TLSConfig(); tc != nil {
		opts.DynamicTLSConfig = func() *tls.Config { return r.TLSConfig() }
	}
}

// reloadingDialTLS returns a DialTLSContext that reads the reloader's current
// *tls.Config at dial time (#362). It is used by the Pyroscope upload client;
// the OTLP exporters get the equivalent from internal/telemetry, which owns
// their transports.
//
// ServerName is filled from the dial address when the config leaves it empty,
// because an empty ServerName disables hostname verification — silently turning
// a rotation feature into a downgrade.
func reloadingDialTLS(r *credreload.Reloader) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		cfg := r.TLSConfig()
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			cfg = cfg.Clone()
		}
		if cfg.ServerName == "" {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			cfg.ServerName = host
		}
		d := &net.Dialer{Timeout: 30 * time.Second}
		raw, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		conn := tls.Client(raw, cfg)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return conn, nil
	}
}

// pyroscopeReloader returns the Pyroscope credential reloader, or nil. Nil-safe
// so call sites need no guard.
func (c *credReloaders) pyroscopeReloader() *credreload.Reloader {
	if c == nil {
		return nil
	}
	return c.pyroscope
}
