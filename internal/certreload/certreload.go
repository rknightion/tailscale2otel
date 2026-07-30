// Package certreload serves TLS certificates that can be rotated underneath a
// running listener.
//
// Every listener in this repo used to pass cert/key file PATHS to
// ListenAndServeTLS, and Go's stdlib loads that keypair exactly once before
// serving — so a rotated certificate was invisible until the process
// restarted, and nothing surfaced the expiry (#316).
//
// This lives in its own leaf package because FOUR listeners need it (admin,
// prometheus, the streaming receiver and the webhook receiver) across three
// packages, and internal/app already imports internal/stream, so the type
// could not live in either without a cycle. A security-relevant reload path
// copied per package is how the copies drift apart.
package certreload

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// MinRecheckInterval bounds how often a TLS handshake triggers a stat(2) of
// the certificate/key files behind a Reloader (#316). Every listener used
// to pass file paths straight to ListenAndServeTLS, so Go's stdlib loaded the
// keypair exactly once before serving and a rotated file was invisible until
// the process restarted — with no signal handling at all, since a SIGHUP
// trigger or a reload HTTP route were both explicitly ruled out (config hot
// reload is the parked product decision #486; see deploy/CLAUDE.md).
//
// Checking on literally every handshake would mean two extra stat(2) calls per
// TLS connection forever; rate-limiting to this interval keeps the hot path a
// single mutex-guarded time comparison in the common case, while still
// noticing an atomic-rename rotation well within a time window operators
// already accept from cert-manager/certbot renewal jobs.
const rfc3339 = time.RFC3339

const MinRecheckInterval = 30 * time.Second

// certFileSig is the cheap, content-free signature Reloader compares to
// decide whether the certificate/key pair changed: mtime+size for BOTH files.
// Comparing the pair (not just the cert) also protects against observing a
// certificate whose matching key has not landed yet on a non-atomic writer —
// LoadX509KeyPair below is what actually validates they match; this signature
// only decides whether it is worth calling it again.
type certFileSig struct {
	certModTime time.Time
	certSize    int64
	keyModTime  time.Time
	keySize     int64
}

// Reloader holds the last known-good certificate for one TLS listener and
// reloads it from disk when the underlying files change, without ever serving
// nothing and without restarting the listener (#316). A broken replacement (a
// half-written file mid-rotation, or a cert/key pair that no longer matches)
// is detected and logged/counted, but the previous good certificate is kept in
// service — the alternative (dropping to no certificate, or crashing the
// listener) turns a botched rotation into an outage, which is exactly what
// this exists to prevent.
//
// GetCertificate is wired directly into tls.Config.GetCertificate: it is
// called on every TLS handshake, but the disk check inside it is rate-limited
// by MinRecheckInterval, so the hot path is a mutex-guarded time
// comparison plus returning the cached *tls.Certificate.
type Reloader struct {
	certFile, keyFile string
	// component is one of the bounded appcatalog.Component* values (admin,
	// metrics) — it labels both the log lines and the emitted metrics.
	component string
	logger    *slog.Logger
	emitter   telemetry.Emitter

	mu           sync.Mutex
	cert         *tls.Certificate
	sig          certFileSig
	lastCheck    time.Time
	notBefore    time.Time
	notAfter     time.Time
	fingerprint  string
	lastReloadAt time.Time
	lastFailAt   time.Time
	lastFailErr  string
}

// newReloader constructs a Reloader and performs its initial,
// synchronous load. Config.Validate() has already confirmed certFile/keyFile
// exist and are readable at startup, so this should not fail in practice; if
// it does anyway (a TOCTOU race between validation and this call), the failure
// is recorded exactly like a later broken rotation would be, and
// GetCertificate returns an explicit error until a subsequent reload succeeds
// — there is no "last known good" yet to fall back to for the very first load.
func New(certFile, keyFile, component string, logger *slog.Logger, emitter telemetry.Emitter) *Reloader {
	r := &Reloader{
		certFile:  certFile,
		keyFile:   keyFile,
		component: component,
		logger:    logger,
		emitter:   emitter,
	}
	r.checkAndReload(time.Now())
	return r
}

// GetCertificate implements the tls.Config.GetCertificate signature.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.checkAndReload(time.Now())
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cert == nil {
		return nil, fmt.Errorf("tlsreload: no certificate loaded for %s", r.component)
	}
	return r.cert, nil
}

// checkAndReload rate-limits reload to at most once per MinRecheckInterval
// (the very first call, with a zero lastCheck, always proceeds).
func (r *Reloader) checkAndReload(now time.Time) {
	r.mu.Lock()
	if !r.lastCheck.IsZero() && now.Sub(r.lastCheck) < MinRecheckInterval {
		r.mu.Unlock()
		return
	}
	r.lastCheck = now
	r.mu.Unlock()
	r.reload(now)
}

// reload stats both files, skips the (comparatively expensive) parse when
// neither has changed, and otherwise loads+validates the new pair before
// swapping it in. Any failure along the way calls recordFailure and returns
// with the previous r.cert (if any) left untouched.
func (r *Reloader) reload(now time.Time) {
	certStat, err := os.Stat(r.certFile)
	if err != nil {
		r.recordFailure(now, err)
		return
	}
	keyStat, err := os.Stat(r.keyFile)
	if err != nil {
		r.recordFailure(now, err)
		return
	}
	sig := certFileSig{
		certModTime: certStat.ModTime(),
		certSize:    certStat.Size(),
		keyModTime:  keyStat.ModTime(),
		keySize:     keyStat.Size(),
	}

	r.mu.Lock()
	unchanged := r.cert != nil && sig == r.sig
	r.mu.Unlock()
	if unchanged {
		return
	}

	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		r.recordFailure(now, err)
		return
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		r.recordFailure(now, err)
		return
	}
	// SHA-256 of the DER leaf certificate only — never the private key. This is
	// the one identifier surfaced on the status page/metrics, so an operator can
	// confirm "which certificate is actually being served" without it ever being
	// possible to recover key material from what is exposed.
	sum := sha256.Sum256(cert.Certificate[0])

	r.mu.Lock()
	r.cert = &cert
	r.sig = sig
	r.notBefore = leaf.NotBefore
	r.notAfter = leaf.NotAfter
	r.fingerprint = hex.EncodeToString(sum[:])
	r.lastReloadAt = now
	r.mu.Unlock()

	if r.logger != nil {
		r.logger.Info("tls certificate (re)loaded",
			"component", r.component, "not_before", leaf.NotBefore, "not_after", leaf.NotAfter)
	}
	if r.emitter != nil {
		appcatalog.EmitTLSCertLoaded(r.emitter, r.component, leaf.NotBefore, leaf.NotAfter, now)
	}
}

// recordFailure logs and counts a broken replacement. It never touches r.cert,
// so a listener keeps serving the last known-good certificate.
func (r *Reloader) recordFailure(now time.Time, err error) {
	r.mu.Lock()
	r.lastFailAt = now
	r.lastFailErr = err.Error()
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Error("tls certificate reload failed; continuing to serve the previous certificate",
			"component", r.component, "error", err)
	}
	if r.emitter != nil {
		appcatalog.EmitTLSCertReloadFailure(r.emitter, r.component)
	}
}

// Status renders the current reload state for the admin status page (#316).
// Times are empty until the first successful/failed check; Fingerprint is
// empty until the first successful load. No field can ever carry key material
// — see the no-key-material test in tlsreload_test.go.
func (r *Reloader) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{
		Name:        r.component,
		Fingerprint: r.fingerprint,
	}
	if !r.notBefore.IsZero() {
		st.NotBefore = r.notBefore.UTC().Format(rfc3339)
	}
	if !r.notAfter.IsZero() {
		st.NotAfter = r.notAfter.UTC().Format(rfc3339)
	}
	if !r.lastReloadAt.IsZero() {
		st.LastReloadAt = r.lastReloadAt.UTC().Format(rfc3339)
	}
	if !r.lastFailAt.IsZero() {
		st.LastReloadFailureAt = r.lastFailAt.UTC().Format(rfc3339)
		st.LastReloadFailureReason = r.lastFailErr
	}
	return st
}

// Status is one listener's certificate health, as plain values. The admin
// status page maps it into its own DTO; this package deliberately does not
// import that DTO, so a receiver package can use the reloader without pulling
// in the admin surface.
//
// Fingerprint is a SHA-256 of the leaf DER — never key material.
type Status struct {
	Name                    string
	NotBefore               string
	NotAfter                string
	Fingerprint             string
	LastReloadAt            string
	LastReloadFailureAt     string
	LastReloadFailureReason string
}
