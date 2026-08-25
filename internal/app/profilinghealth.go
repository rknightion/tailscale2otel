package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/credreload"
	"github.com/rknightion/tailscale2otel/v4/internal/redact"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Profile-upload error classes. A CLOSED set, declared as the single source of
// truth in internal/appcatalog (ProfilingUploadErrorClasses) because that is
// where the descriptor whose cardinality it bounds lives; these constants are the
// classifier's side of the same contract, and profilinghealth_test.go asserts
// every classifier output is a member of the appcatalog set.
//
// Closed because a Pyroscope upload failure carries the SERVER'S RESPONSE BODY,
// which is exactly where an echoed credential, a signed URL, or a tenant
// identifier would appear. Same reasoning as internal/telemetry's
// classifyExportError, which this deliberately mirrors — plus one class that path
// does not need:
//
//	tls — a custom CA / mTLS handshake failure. Folding it into "unavailable"
//	      would make the single most likely misconfiguration of the TLS controls
//	      indistinguishable from the profiles backend being down.
const (
	profileErrClassTimeout         = "timeout"
	profileErrClassCanceled        = "canceled"
	profileErrClassUnauthenticated = "unauthenticated"
	profileErrClassRateLimited     = "rate_limited"
	profileErrClassUnavailable     = "unavailable"
	profileErrClassTLS             = "tls"
	profileErrClassInvalid         = "invalid"
	profileErrClassOther           = "other"
)

// profilingUploadUnhealthyStreak is the consecutive-failure count at which
// uploads are reported unhealthy rather than blipping. Three missed uploads at
// the default 60s upload_rate is several minutes of no profiles.
//
// It intentionally does NOT feed readiness — see (*App).readyz. Profiling is a
// diagnostic side-channel, and a profiles backend outage must never stall a
// rollout. TestReadinessIndependentOfProfilingHealth is the guard.
const profilingUploadUnhealthyStreak = 3

// profilingHealth records the outcome of every completed profile-upload attempt.
//
// Like internal/telemetry's deliveryTracker it is populated regardless of
// self-observability: the admin page is an operator's LOCAL view and has to work
// on a deployment that exports no self-telemetry at all. Written from the
// pyroscope-go uploader goroutines and read from the admin handler, so every
// access is mutex-guarded.
type profilingHealth struct {
	mu                  sync.Mutex
	attempts            int64
	failures            int64
	consecutiveFailures int64
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	lastDurationSeconds float64
	lastErrorClass      string
}

func newProfilingHealth() *profilingHealth { return &profilingHealth{} }

// profilingHealthState is the process-wide upload-health tracker.
//
// Package-level rather than a field on *App because there is exactly one
// profiler per process — startProfiling is called once from app.New and the
// pyroscope-go agent is itself a singleton — and because the pyroscope.Config
// built by pyroscopeConfig (a pure function, deliberately) has to reach the same
// tracker the status page reads. Tests swap it out and restore it.
var profilingHealthState = newProfilingHealth()

// observe records one completed upload attempt. An empty class means success.
// A nil receiver is a no-op so a test seam constructing a client without a
// tracker stays valid.
func (h *profilingHealth) observe(class string, seconds float64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attempts++
	h.lastDurationSeconds = seconds
	now := time.Now()
	if class != "" {
		h.failures++
		h.consecutiveFailures++
		h.lastFailureAt = now
		h.lastErrorClass = class
		return
	}
	h.consecutiveFailures = 0
	h.lastSuccessAt = now
	// lastFailureAt and lastErrorClass are deliberately NOT cleared: "recovered
	// two minutes ago after failing for an hour" is precisely what an operator is
	// trying to establish, and clearing them erases it.
}

// snapshot returns the tracker's state as the status-page DTO. Timestamps render
// as RFC3339 and are omitted while zero, so "has never uploaded" is visibly
// different from "uploaded at the epoch".
func (h *profilingHealth) snapshot() statusdata.ProfilingUploadHealth {
	if h == nil {
		return statusdata.ProfilingUploadHealth{Healthy: true}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := statusdata.ProfilingUploadHealth{
		Attempts:            h.attempts,
		Failures:            h.failures,
		ConsecutiveFailures: h.consecutiveFailures,
		LastDurationSeconds: h.lastDurationSeconds,
		LastErrorClass:      h.lastErrorClass,
		Healthy:             h.consecutiveFailures < profilingUploadUnhealthyStreak,
	}
	if !h.lastSuccessAt.IsZero() {
		out.LastSuccessAt = h.lastSuccessAt.UTC().Format(time.RFC3339)
	}
	if !h.lastFailureAt.IsZero() {
		out.LastFailureAt = h.lastFailureAt.UTC().Format(time.RFC3339)
	}
	return out
}

// emit writes the tracker's current state to the emitter. Called once per
// completed attempt from the upload goroutine, which is also the only cadence at
// which any of these values can change.
//
// The two gauges are read under the lock but emitted outside it, so with
// concurrent uploads in flight (the SDK runs one upload thread per profile type)
// two emissions can land out of order. That is harmless: both gauges are
// last-value, and the next attempt — at most one upload period away — converges
// them. Holding the lock across emission to avoid it would put an exporter call
// inside the tracker's critical section, which the admin handler also takes.
func (h *profilingHealth) emit(e telemetry.Emitter, class string, seconds float64) {
	if e == nil || h == nil {
		return
	}
	e.Counter(appcatalog.DocProfilingUploadAttempts.Name, appcatalog.DocProfilingUploadAttempts.Unit,
		appcatalog.DocProfilingUploadAttempts.Description, 1, nil)
	e.Histogram(appcatalog.DocProfilingUploadDuration.Name, appcatalog.DocProfilingUploadDuration.Unit,
		appcatalog.DocProfilingUploadDuration.Description, seconds, profileUploadDurationBounds, nil)
	if class != "" {
		e.Counter(appcatalog.DocProfilingUploadFailures.Name, appcatalog.DocProfilingUploadFailures.Unit,
			appcatalog.DocProfilingUploadFailures.Description, 1,
			telemetry.Attrs{semconv.AttrErrorType: class})
	}
	h.mu.Lock()
	streak := float64(h.consecutiveFailures)
	var lastSuccess float64
	if !h.lastSuccessAt.IsZero() {
		lastSuccess = float64(h.lastSuccessAt.Unix())
	}
	h.mu.Unlock()
	e.Gauge(appcatalog.DocProfilingUploadConsecutiveFailures.Name, appcatalog.DocProfilingUploadConsecutiveFailures.Unit,
		appcatalog.DocProfilingUploadConsecutiveFailures.Description, streak, nil)
	e.Gauge(appcatalog.DocProfilingUploadLastSuccess.Name, appcatalog.DocProfilingUploadLastSuccess.Unit,
		appcatalog.DocProfilingUploadLastSuccess.Description, lastSuccess, nil)
}

// profileUploadDurationBounds are seconds-scale buckets sized for a multipart
// profile POST: sub-second on a healthy link, out to the SDK's 30s timeout.
var profileUploadDurationBounds = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// maxDiscardedErrorBody caps how much of a rejected upload's body is drained
// before the connection is released. The content is discarded either way; the
// cap just stops a hostile/broken endpoint streaming indefinitely into
// io.Discard on every upload.
const maxDiscardedErrorBody = 4 << 10

// profilingUploadClient is the pyroscope-go remote.HTTPClient the push agent
// uploads through. It exists to close three gaps at once, on ONE http.Client:
//
//  1. health (#374) — record every attempt's outcome, duration and error class;
//  2. secret hygiene (#374) — the upstream uploader formats a non-200 response
//     into "failed to upload: (%d) '%s'" with the body VERBATIM and hands it to
//     its Errorf logger. Draining the body and substituting http.NoBody means the
//     status code is all the SDK can possibly log;
//  3. transport policy (#375) — the custom CA, client certificate and
//     insecure_skip_verify all configure the base client's TLS.
//
// It is one type rather than a chain of RoundTrippers because the SDK's seam is
// Do, not RoundTrip, and because the body substitution has to happen after the
// response is complete.
type profilingUploadClient struct {
	base    *http.Client
	health  *profilingHealth
	emitter telemetry.Emitter // nil disables metric emission; the tracker still records
}

// newProfilingUploadClient builds the upload client from the transport options.
// It returns an error only for unusable TLS material (an unreadable CA bundle or
// keypair), which the caller reports rather than starting a profiler that can
// never upload.
func newProfilingUploadClient(opts pyroscopeTransportOptions, health *profilingHealth, emitter telemetry.Emitter, reload *credreload.Reloader) (*profilingUploadClient, error) {
	tlsCfg, err := opts.tlsConfig()
	if err != nil {
		return nil, err
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok { // defensive: stdlib always provides *http.Transport
		transport = &http.Transport{}
	}
	tr := transport.Clone()
	// Mirror the upstream uploader's own connection cap (remote.NewRemote uses
	// MaxConnsPerHost = Threads = 5, one per profile type in flight).
	tr.MaxConnsPerHost = pyroscopeUploadThreads
	// DELIBERATE difference from upstream: cloning http.DefaultTransport means the
	// upload path honors HTTP_PROXY/HTTPS_PROXY/NO_PROXY, which the SDK's own
	// bare &http.Transport{} does not. An exporter that reaches its OTLP backend
	// through a corporate proxy but silently cannot reach the profiles backend is
	// a confusing failure, and the stdlib default is what an operator expects.
	// This is proxy DISCOVERY from the standard environment, not the arbitrary
	// proxy scripting #375 put out of scope.
	if tlsCfg != nil {
		tr.TLSClientConfig = tlsCfg
	}
	// Credential/TLS rotation (#362): when a reloader is watching this client's
	// files, dial with the CURRENT material per connection instead of the copy
	// read above. Client-certificate rotation is immediate (the handshake calls
	// GetClientCertificate); a rotated CA applies to the next new connection,
	// which the transport's own idle-connection recycling bounds.
	if reload != nil {
		tr.DialTLSContext = reloadingDialTLS(reload)
	}
	return &profilingUploadClient{
		base: &http.Client{
			Transport: tr,
			Timeout:   pyroscopeUploadTimeout,
			// Do NOT follow redirects — copied from remote.NewRemote for the reason
			// stated there: net/http strips the Authorization header across a
			// redirect (e.g. http -> https), so an authorized server would answer
			// 401 and look like a credential problem.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		health:  health,
		emitter: emitter,
	}, nil
}

// pyroscopeUploadThreads and pyroscopeUploadTimeout mirror the values
// pyroscope-go's remote uploader sets on the client it would have built itself.
// Supplying a custom HTTPClient replaces that client wholesale, so these have to
// be restated here or the upload path would silently lose its timeout.
const (
	pyroscopeUploadThreads = 5
	pyroscopeUploadTimeout = 30 * time.Second
)

// Do performs one upload, records its outcome, and sanitizes what the caller can
// observe. It satisfies pyroscope-go's remote.HTTPClient.
func (c *profilingUploadClient) Do(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := c.base.Do(req)
	seconds := time.Since(started).Seconds()

	switch {
	case err != nil:
		class := classifyProfileUploadError(err)
		c.record(class, seconds)
		// Replace the error rather than wrapping it. A transport error is a
		// *url.Error carrying the full request URL, and the SDK logs it verbatim;
		// only the class leaves this function.
		return nil, fmt.Errorf("pyroscope upload failed: %s", class) //nolint:err113 // a closed class string, deliberately not a sentinel
	case resp.StatusCode != http.StatusOK:
		class := classifyProfileUploadStatus(resp.StatusCode)
		c.record(class, seconds)
		// Drain a bounded prefix so the connection can be reused, then hand the
		// SDK an EMPTY body: it reads the body into its error message, which goes
		// straight to the logger. The status code is preserved so the SDK's own
		// message still says what happened.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDiscardedErrorBody))
		_ = resp.Body.Close()
		resp.Body = http.NoBody
		resp.ContentLength = 0
		return resp, nil
	default:
		c.record("", seconds)
		return resp, nil
	}
}

func (c *profilingUploadClient) record(class string, seconds float64) {
	c.health.observe(class, seconds)
	c.health.emit(c.emitter, class, seconds)
}

// profilingInfo builds the status page's profiling section: the configuration,
// plus the push agent's live upload health when it is enabled.
//
// Nothing here can carry a credential. The server address goes through
// redact.URL (a push target can carry userinfo or a signed query — the dedicated
// basic_auth_* fields express the same thing, GHSA-jp5c-3282-6882), extra headers
// are reported by NAME only, the TLS customizations are reported as flags rather
// than paths, and the upload health carries a closed error class rather than the
// server's text.
func (a *App) profilingInfo() statusdata.ProfilingInfo {
	p := a.cfg.Profiling.Pyroscope
	topts := pyroscopeTransportOptionsFromConfig(p)
	info := statusdata.ProfilingInfo{
		PprofEnabled:           a.cfg.Profiling.Pprof.Enabled,
		PyroscopeEnabled:       p.Enabled,
		PyroscopeServer:        redact.URLOrigin(p.ServerAddress),
		PyroscopeHeaderNames:   topts.headerNames(),
		PyroscopeTLSCustomCA:   topts.CAFile != "",
		PyroscopeTLSClientCert: topts.CertFile != "" && topts.KeyFile != "",
		PyroscopeTLSSkipVerify: topts.InsecureSkipVerify,
	}
	if p.Enabled {
		snap := profilingHealthState.snapshot()
		info.PyroscopeUpload = &snap
	}
	return info
}

// classifyProfileUploadStatus maps an HTTP status onto the closed class set.
// pyroscope-go treats anything other than 200 as a failure, so every other code
// — including 2xx siblings and redirects — has to land somewhere.
func classifyProfileUploadStatus(code int) string {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired:
		return profileErrClassUnauthenticated
	case http.StatusTooManyRequests:
		return profileErrClassRateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return profileErrClassTimeout
	}
	switch {
	case code >= 500:
		return profileErrClassUnavailable
	case code == http.StatusBadRequest || code == http.StatusNotFound ||
		code == http.StatusRequestEntityTooLarge || code == http.StatusUnsupportedMediaType ||
		code == http.StatusUnprocessableEntity:
		return profileErrClassInvalid
	default:
		return profileErrClassOther
	}
}

// classifyProfileUploadError maps a transport error onto the closed class set.
//
// It reads the error's TEXT after the sentinel and typed checks for the same
// reason classifyExportError does: net/http surfaces DNS, dial, and TLS failures
// as differently-typed wrapped errors, and matching text here avoids depending on
// unexported stdlib types. It returns ONLY a constant — never any part of err.
func classifyProfileUploadError(err error) string {
	if err == nil {
		return ""
	}
	// Typed and sentinel checks first: exact, and immune to a server that happens
	// to mention "timeout" in its response.
	var verify *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return profileErrClassTimeout
	case errors.Is(err, context.Canceled):
		return profileErrClassCanceled
	case errors.As(err, &verify), errors.As(err, &recordErr):
		return profileErrClassTLS
	}
	msg := strings.ToLower(err.Error())
	switch {
	// TLS before the generic transport classes: a handshake failure often also
	// mentions the connection, and "your CA is wrong" must not read as "the
	// backend is down".
	case strings.Contains(msg, "tls:"), strings.Contains(msg, "x509"),
		strings.Contains(msg, "certificate"):
		return profileErrClassTLS
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"),
		strings.Contains(msg, "timed out"):
		return profileErrClassTimeout
	case strings.Contains(msg, "context canceled"):
		return profileErrClassCanceled
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection reset"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "eof"):
		return profileErrClassUnavailable
	case strings.Contains(msg, "unsupported protocol scheme"), strings.Contains(msg, "invalid"):
		return profileErrClassInvalid
	default:
		return profileErrClassOther
	}
}
