// Package credreload watches outbound-telemetry credential and TLS material
// on disk and hot-swaps them without a process restart (#362).
//
// It knows nothing about OTLP, Pyroscope, or telemetry emission — it is a
// dependency-free file watcher plus an in-memory last-known-good snapshot.
// The caller (the app composition root) wires the accessors below into an
// exporter's HTTP transport, gRPC dial options, or TLS config.
//
// Design invariants:
//   - A malformed or unreadable replacement file NEVER clobbers the current
//     snapshot: Headers() and TLSConfig() keep returning the last value that
//     loaded successfully, and the failure is only visible via Health().
//   - Secrets (bearer tokens, header values, private key bytes) are never
//     included in an error, in Health.LastError, or in String()/GoString().
//     Errors name the file path and the failure reason, never the content.
//   - Headers() and TLSConfig() are cheap, lock-free reads of an atomically
//     published snapshot — safe to call on every request from an exporter's
//     hot path. Files are only re-read by Reload() or the background poller,
//     never by an accessor.
package credreload

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

// Sources lists the files to watch and how to interpret them. A zero value
// watches nothing: Headers() returns an empty map and TLSConfig() returns nil.
type Sources struct {
	// BearerTokenFile, if non-empty, names a file whose trimmed content
	// becomes the value of an "Authorization: Bearer <token>" header.
	BearerTokenFile string

	// HeaderFiles maps an arbitrary header name to the path of a file whose
	// trimmed content is that header's value, e.g.
	// {"X-Scope-OrgID": "/var/run/secrets/tenant"}.
	HeaderFiles map[string]string

	// CAFile, if non-empty, is a PEM bundle of CA certificates trusted for
	// the outbound TLS connection. It may be set alone (CA-only trust) or
	// alongside CertFile/KeyFile.
	CAFile string

	// CertFile and KeyFile are an outbound client TLS keypair. Both must be
	// set or both empty — a lone cert or key is rejected.
	CertFile string
	KeyFile  string

	// InsecureSkipVerify is copied verbatim into every TLSConfig() snapshot.
	// credreload does not police this; it is the caller's own opt-in choice
	// (already validated at config load, mirroring otlp.tls.insecure_skip_verify).
	InsecureSkipVerify bool
}

// WatchesAnything reports whether this Sources configuration names at least one
// file to watch. A caller uses it to decide whether to build a Reloader at all:
// with nothing to watch there is nothing to rotate, so the static path is
// correct and cheaper. InsecureSkipVerify alone does NOT count — it is a policy
// flag, not a file.
func (s Sources) WatchesAnything() bool { return len(s.files()) > 0 }

// watchesTLS reports whether any TLS-relevant source is configured.
func (s Sources) watchesTLS() bool {
	return s.CAFile != "" || s.CertFile != "" || s.KeyFile != ""
}

// files returns every path this Sources configuration reads, for stat/hash
// bookkeeping. Order is stable so tests can assert on it.
func (s Sources) files() []string {
	var out []string
	if s.BearerTokenFile != "" {
		out = append(out, s.BearerTokenFile)
	}
	out = append(out, sortedValues(s.HeaderFiles)...)
	if s.CAFile != "" {
		out = append(out, s.CAFile)
	}
	if s.CertFile != "" {
		out = append(out, s.CertFile)
	}
	if s.KeyFile != "" {
		out = append(out, s.KeyFile)
	}
	return out
}

// Options configures a Reloader.
type Options struct {
	// Sources is the set of files to watch and how to interpret them.
	Sources Sources

	// Interval is the bounded-poller period. <=0 disables the background
	// poller entirely — Reload() must then be called explicitly (e.g. from a
	// SIGHUP handler). The poller always compares mtime+size before hashing
	// content, so an idle poll costs one os.Stat per watched file.
	Interval time.Duration

	// Logger, if non-nil, receives one Info line on a successful reload that
	// actually changed the snapshot, and one Warn line per failed reload.
	// Neither line ever includes file content — only paths and error text
	// that has already been through the same secret-free construction as
	// Health.LastError.
	Logger *slog.Logger
}

// Health is a point-in-time, alertable summary of the reloader's state. It
// never contains secret content — only paths and static reason strings.
type Health struct {
	// Healthy is true once at least one load has succeeded and the most
	// recent Reload() attempt (if any) did not regress that state — i.e. the
	// currently served snapshot is not stale-and-known-bad. It is false only
	// before the very first successful load.
	Healthy bool

	// LastAttempt is when Reload() (poller-driven or explicit) last ran.
	// Zero if Reload() has never run.
	LastAttempt time.Time

	// LastSuccess is when the served snapshot was last (re)built. Zero if no
	// load has ever succeeded.
	LastSuccess time.Time

	// LastError is the reason the most recent Reload() attempt failed, or
	// empty if the most recent attempt succeeded or none has run. Names the
	// offending path and the failure class; never file content.
	LastError string

	// ConsecutiveFailures counts Reload() attempts since the last success.
	// Reset to 0 by any successful reload.
	ConsecutiveFailures int
}

// snapshot is the immutable, atomically-published result of a successful
// load. A *snapshot is never mutated after construction — Reload() builds a
// new one and swaps the pointer.
type snapshot struct {
	headers   map[string]string
	tlsConfig *tls.Config
	cert      *tls.Certificate // nil if no client keypair configured
}

// fileState is the last-applied mtime+size+hash for one watched path, used
// to decide whether a poll needs to re-read (and re-hash) the file at all.
type fileState struct {
	modTime int64
	size    int64
	hash    [sha256.Size]byte
	hashSet bool
}

// Reloader watches a fixed set of credential/TLS files and serves the last
// successfully loaded value. See the package doc for the invariants.
type Reloader struct {
	sources Sources
	logger  *slog.Logger

	snap atomic.Pointer[snapshot]

	mu        sync.Mutex // serializes Reload(); Start/Stop lifecycle
	states    map[string]fileState
	lastErr   string
	lastGood  time.Time
	lastTry   time.Time
	failCount int
	healthy   bool

	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	started  bool
}

// New builds a Reloader and performs the initial load synchronously: startup
// failure beats a first-poll failure, matching this repo's existing
// config-validation convention (see internal/config/validate.go). New
// returns an error if the initial load fails; the caller should treat that as
// a fatal startup error, exactly as an unloadable static credential would be
// today.
//
// New does not start the background poller — call Start() for that.
func New(opts Options) (*Reloader, error) {
	r := &Reloader{
		sources:  opts.Sources,
		logger:   opts.Logger,
		states:   make(map[string]fileState),
		interval: opts.Interval,
	}
	if err := r.reloadLocked(); err != nil {
		return nil, fmt.Errorf("credreload: initial load: %w", err)
	}
	return r, nil
}

// Start launches the background poller if Interval > 0. It is a no-op if
// Interval <= 0 (explicit Reload()-only mode) or if already started. Safe to
// call at most once per Reloader; call Stop() for clean shutdown.
func (r *Reloader) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.interval <= 0 {
		return
	}
	r.started = true
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	go r.pollLoop(r.stopCh, r.doneCh)
}

// Stop shuts the background poller down and waits for its goroutine to exit.
// A no-op if Start() was never called or the poller was never running
// (Interval <= 0). Safe to call more than once.
func (r *Reloader) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	r.started = false
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.mu.Unlock()

	close(stopCh)
	<-doneCh
}

func (r *Reloader) pollLoop(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-t.C:
			_ = r.Reload()
		}
	}
}

// Reload re-checks every watched file and, only if at least one changed
// (by mtime+size, then confirmed by content hash), rebuilds the snapshot. A
// malformed or unreadable file leaves the previously served snapshot fully
// intact — Reload returns the error but Headers()/TLSConfig() are unchanged.
// Safe to call concurrently with itself and with the background poller; the
// last invocation to acquire the lock wins.
func (r *Reloader) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadLocked()
}

func (r *Reloader) reloadLocked() error {
	r.lastTry = time.Now()

	changed, err := r.statChanges()
	if err != nil {
		r.recordFailureLocked(err)
		return err
	}
	if !changed && r.snap.Load() != nil {
		// Nothing changed and we already have a served snapshot: a healthy
		// no-op poll. Still counts as a successful "attempt" for Health.
		r.recordSuccessLocked(nil)
		return nil
	}

	newSnap, states, err := r.buildSnapshot()
	if err != nil {
		r.recordFailureLocked(err)
		return err
	}

	// mtime/size flagged a possible change, but content may still be
	// identical (e.g. a touch, or an atomic rewrite of the same bytes). Only
	// swap the served snapshot — and only log — when a content hash actually
	// differs from what is currently applied; otherwise just refresh the
	// stat bookkeeping so the next poll's cheap comparison stays accurate.
	contentChanged := r.snap.Load() == nil || !sameContent(r.states, states)
	if contentChanged {
		r.snap.Store(newSnap)
	}
	r.states = states
	r.recordSuccessLocked(nil)
	if r.logger != nil && contentChanged {
		r.logger.Info("credreload: reloaded outbound telemetry credentials")
	}
	return nil
}

// sameContent reports whether every path present in b has an identical
// content hash in a (a may have extra stale entries; that's fine — a source
// set never shrinks across the lifetime of one Reloader).
func sameContent(a, b map[string]fileState) bool {
	for path, bs := range b {
		as, ok := a[path]
		if !ok || !as.hashSet || !bs.hashSet || as.hash != bs.hash {
			return false
		}
	}
	return true
}

func (r *Reloader) recordFailureLocked(err error) {
	r.lastErr = err.Error()
	r.failCount++
	// Healthy stays true if we already have a served snapshot from an
	// earlier success — that snapshot is still being served correctly, it is
	// simply stale relative to what's on disk now.
	if r.logger != nil {
		r.logger.Warn("credreload: reload failed, retaining last-known-good state", "error", err)
	}
}

func (r *Reloader) recordSuccessLocked(_ error) {
	r.lastErr = ""
	r.failCount = 0
	r.lastGood = r.lastTry
	r.healthy = true
}

// statChanges reports whether any watched file's mtime or size differs from
// what is recorded in r.states, without reading file content. Returns an
// error (naming only the path) if a required file cannot be stat'd — a
// missing file is itself a reportable failure, not silently "unchanged".
func (r *Reloader) statChanges() (bool, error) {
	changed := false
	for _, path := range r.sources.files() {
		fi, err := os.Stat(path)
		if err != nil {
			return false, fmt.Errorf("stat %s: %w", path, err)
		}
		prev, ok := r.states[path]
		if !ok || prev.modTime != fi.ModTime().UnixNano() || prev.size != fi.Size() {
			changed = true
		}
	}
	return changed, nil
}

// buildSnapshot reads and validates every configured source fresh and, only
// if all of them are individually valid, returns the new snapshot plus the
// mtime/size/hash bookkeeping to apply alongside it. It never partially
// applies — either every source is valid and the whole snapshot is replaced,
// or an error is returned and nothing is touched.
func (r *Reloader) buildSnapshot() (*snapshot, map[string]fileState, error) {
	states := make(map[string]fileState, len(r.sources.files()))
	readHashed := func(path string, limit int64) ([]byte, error) {
		data, fi, err := safefile.ReadRegularInfo(path, limit, safefile.AllowSymlink)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		states[path] = fileState{
			modTime: fi.ModTime().UnixNano(),
			size:    fi.Size(),
			hash:    sha256.Sum256(data),
			hashSet: true,
		}
		return data, nil
	}

	headers := make(map[string]string, len(r.sources.HeaderFiles)+1)

	if r.sources.BearerTokenFile != "" {
		data, err := readHashed(r.sources.BearerTokenFile, safefile.MaxSecretBytes)
		if err != nil {
			return nil, nil, err
		}
		tok := strings.TrimSpace(string(data))
		if tok == "" {
			return nil, nil, fmt.Errorf("bearer token file %s is empty", r.sources.BearerTokenFile)
		}
		headers["Authorization"] = "Bearer " + tok
	}

	for _, name := range sortedKeys(r.sources.HeaderFiles) {
		path := r.sources.HeaderFiles[name]
		data, err := readHashed(path, safefile.MaxSecretBytes)
		if err != nil {
			return nil, nil, err
		}
		val := strings.TrimSpace(string(data))
		if val == "" {
			return nil, nil, fmt.Errorf("header file %s (header %q) is empty", path, name)
		}
		headers[name] = val
	}

	var tlsCfg *tls.Config
	var cert *tls.Certificate
	if r.sources.watchesTLS() {
		tlsCfg = &tls.Config{InsecureSkipVerify: r.sources.InsecureSkipVerify} //nolint:gosec // caller-chosen, validated at config load

		if r.sources.CAFile != "" {
			data, err := readHashed(r.sources.CAFile, safefile.MaxPEMBytes)
			if err != nil {
				return nil, nil, err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(data) {
				return nil, nil, fmt.Errorf(
					"CA file %s contains no usable PEM certificate: a CA bundle that parses to an "+
						"empty pool trusts nothing, which fails every handshake rather than erroring here",
					r.sources.CAFile)
			}
			tlsCfg.RootCAs = pool
		}

		if (r.sources.CertFile == "") != (r.sources.KeyFile == "") {
			return nil, nil, fmt.Errorf(
				"cert_file and key_file must both be set or both be empty (got cert_file=%q, key_file=%q)",
				r.sources.CertFile, r.sources.KeyFile)
		}
		if r.sources.CertFile != "" {
			certData, err := readHashed(r.sources.CertFile, safefile.MaxPEMBytes)
			if err != nil {
				return nil, nil, err
			}
			keyData, err := readHashed(r.sources.KeyFile, safefile.MaxPEMBytes)
			if err != nil {
				return nil, nil, err
			}
			kp, err := tls.X509KeyPair(certData, keyData)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"cert_file %q + key_file %q do not form a usable keypair: %w",
					r.sources.CertFile, r.sources.KeyFile, err)
			}
			cert = &kp
			tlsCfg.Certificates = []tls.Certificate{kp}
			tlsCfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return cert, nil
			}
		}
	}

	return &snapshot{headers: headers, tlsConfig: tlsCfg, cert: cert}, states, nil
}

// Headers returns a fresh copy of the currently served header set (bearer
// token plus any header files). Safe for concurrent use from an exporter's
// request path; never re-reads files. Returns an empty, non-nil map if no
// header sources are configured or no load has ever succeeded.
func (r *Reloader) Headers() map[string]string {
	s := r.snap.Load()
	if s == nil {
		return map[string]string{}
	}
	return maps.Clone(s.headers)
}

// TLSConfig returns a snapshot of the currently served TLS configuration —
// RootCAs and Certificates already resolved, GetClientCertificate wired for
// per-handshake dynamic client-cert rotation without rebuilding the caller's
// connection. Returns nil if no TLS sources are configured. Safe for
// concurrent use; never re-reads files. Callers that hold a long-lived
// tls.Config (e.g. a gRPC ClientConn dialed once) should re-fetch TLSConfig()
// after a Reload() to pick up CA/cert changes on new connections — see the
// package's GetClientCertificate note for the one hook that updates without
// a reconnect.
func (r *Reloader) TLSConfig() *tls.Config {
	s := r.snap.Load()
	if s == nil || s.tlsConfig == nil {
		return nil
	}
	return s.tlsConfig.Clone()
}

// ClientCertificate returns the currently loaded client keypair, suitable for
// direct use as a tls.Config.GetClientCertificate callback (bind it as
// `cfg.GetClientCertificate = reloader.ClientCertificate`) so a long-lived
// TLS-terminating connection (e.g. a single dialed gRPC ClientConn) picks up
// a rotated certificate on its next handshake without being redialed. Returns
// an error if no client keypair is currently loaded — callers that always
// wire this must have configured CertFile/KeyFile.
func (r *Reloader) ClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	s := r.snap.Load()
	if s == nil || s.cert == nil {
		return nil, errors.New("credreload: no client certificate configured")
	}
	return s.cert, nil
}

// Health returns a point-in-time, alertable summary. Never contains secret
// content.
func (r *Reloader) Health() Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Health{
		Healthy:             r.healthy,
		LastAttempt:         r.lastTry,
		LastSuccess:         r.lastGood,
		LastError:           r.lastErr,
		ConsecutiveFailures: r.failCount,
	}
}

// String never includes header values, token content, or key material — only
// enough shape to identify the instance in logs.
func (r *Reloader) String() string {
	h := r.Health()
	return fmt.Sprintf("credreload.Reloader{healthy=%v consecutiveFailures=%d}", h.Healthy, h.ConsecutiveFailures)
}

// GoString mirrors String(): fmt's "%#v" must not fall back to printing
// unexported fields (which would include cached header/secret values).
func (r *Reloader) GoString() string {
	return r.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortedValues(m map[string]string) []string {
	keys := sortedKeys(m)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// sortStrings is a tiny insertion sort to avoid importing "sort" for a
// handful of header names; kept local so the package's only imports are the
// ones doing real work.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
