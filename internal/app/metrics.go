package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/certreload"
	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v4/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// buildMetricsServer builds the dedicated Prometheus pull-endpoint server. Only
// /metrics is served; it is bearer/Basic-gated when prometheus.auth.token is set.
// Separate from the admin server so pull works without the status page/pprof.
func (a *App) buildMetricsServer(g prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	// ContinueOnError (not the promhttp default HTTPErrorOnError): when
	// pii_filter.tailnet_name=false drops the tailscale.tailnet distinguisher, the
	// per-provider registries can produce byte-identical series (per-tailnet series
	// in multi mode; process+tailnet self-obs in single mode). The default turns
	// that Gather collision into a permanent HTTP 500 on every scrape; first-wins
	// keeps /metrics returning 200 instead of taking the whole pull path down (#103).
	//
	// EnableOpenMetrics is pure content negotiation, which is why it is not a
	// config knob (#368): a scraper that sends `Accept:
	// application/openmetrics-text` gets OpenMetrics, and anything that does not
	// ask keeps the byte-identical classic `text/plain; version=0.0.4`
	// exposition. It matters because OpenMetrics is the ONLY exposition format
	// that can carry exemplars — with it off, every trace exemplar the SDK
	// attached was silently dropped on the pull path while the OTLP push path
	// delivered them fine.
	//
	// EnableOpenMetricsTextCreatedSamples is deliberately left off: OpenMetrics
	// 1.0 models created timestamps as extra `_created` series, doubling the
	// series count of every counter and histogram, and a scraper without
	// created-timestamp support ingests them as ordinary metrics rather than
	// folding them into the reset detection they exist for.
	//
	// MaxRequestsInFlight / Timeout / CoalesceGather are the load guards (#377).
	// Gathering every series this exporter produces is not free, and nothing
	// upstream of here bounds how fast a scraper (or several) may ask: without a
	// cap, a scrape interval shorter than the gather time piles goroutines up,
	// each holding its own full snapshot, until the process is spending its
	// memory and CPU answering /metrics rather than collecting. The shipped
	// defaults keep the gather count finite, coalesce overlap, and time out slow
	// scrapes; effectiveMetricsMaxRequests also fails safe for a Config assembled
	// without the normal validation path.
	//
	// ErrorLog is the ONLY way a Gather error becomes visible, because
	// ContinueOnError deliberately keeps the HTTP status at 200 — see
	// gatherErrorLogger.
	mux.Handle("/metrics", a.requireMetricsAuth(a.instrumentScrape(promhttp.HandlerFor(g, promhttp.HandlerOpts{
		ErrorHandling:       promhttp.ContinueOnError,
		EnableOpenMetrics:   true,
		ErrorLog:            a.gatherErrorLogger(),
		MaxRequestsInFlight: effectiveMetricsMaxRequests(a.cfg.Prometheus.MaxRequestsInFlight),
		Timeout:             a.cfg.Prometheus.Timeout.D(),
		CoalesceGather:      a.cfg.Prometheus.CoalesceGather,
	}))))
	srv := &http.Server{
		Addr:              a.cfg.Prometheus.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Same GetCertificate-backed reload as the admin listener (#316); see
	// buildAdminServer's comment and CertReloader in tlsreload.go.
	if a.cfg.Prometheus.TLS.CertFile != "" && a.cfg.Prometheus.TLS.KeyFile != "" {
		reloader := certreload.New(a.cfg.Prometheus.TLS.CertFile, a.cfg.Prometheus.TLS.KeyFile,
			appcatalog.ComponentMetrics, a.logger, a.procEmitter)
		tlsCfg := &tls.Config{GetCertificate: reloader.GetCertificate}
		a.applyMetricsClientAuth(tlsCfg)
		srv.TLSConfig = tlsCfg
		a.metricsCerts = reloader
	}
	return srv
}

func effectiveMetricsMaxRequests(configured int) int {
	if configured <= 0 {
		return 4
	}
	return configured
}

// applyMetricsClientAuth adds mutual-TLS client-certificate verification to the
// Prometheus listener's TLS config when prometheus.tls.client_ca_file is set
// (#378). A no-op otherwise, so a listener without a client CA is byte-identical
// to before.
//
// This is deliberately scoped to the Prometheus listener: it is the surface a
// machine scrapes, so a certificate is a natural credential for it, whereas the
// admin surface is reached by a human in a browser.
//
// Unlike the SERVER keypair, the client CA bundle is read ONCE at build time and
// is not hot-reloaded — internal/certreload rotates the certificate a listener
// presents, not the trust store it verifies against. Rotating a CA is a
// years-scale event and needs a restart.
func (a *App) applyMetricsClientAuth(tlsCfg *tls.Config) {
	caFile := a.cfg.Prometheus.TLS.ClientCAFile
	if caFile == "" {
		return
	}
	// Validate has already confirmed this file exists, is readable, and parses to
	// at least one certificate — so reaching either failure branch means the file
	// changed underneath us between startup validation and bind. Both branches
	// fail CLOSED: an empty pool plus RequireAndVerifyClientCert rejects every
	// handshake, whatever mode was configured. The alternative — honoring a
	// non-verifying mode with an empty trust store — is a listener that stops
	// checking certificates and reports nothing but a log line, which is exactly
	// the silent downgrade mutual TLS was configured to prevent.
	pool := x509.NewCertPool()
	pem, err := safefile.ReadRegular(caFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
	switch {
	case err != nil:
		a.logger.With(semconv.AttrComponent, appcatalog.ComponentMetrics).
			Error("prometheus client CA bundle could not be read; the listener will reject every scraper",
				"client_ca_file", caFile, "error", err)
		a.componentError(appcatalog.ComponentMetrics)
		tlsCfg.ClientCAs, tlsCfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		return
	case !pool.AppendCertsFromPEM(pem):
		a.logger.With(semconv.AttrComponent, appcatalog.ComponentMetrics).
			Error("prometheus client CA bundle contains no parseable certificate; the listener will reject every scraper",
				"client_ca_file", caFile)
		a.componentError(appcatalog.ComponentMetrics)
		tlsCfg.ClientCAs, tlsCfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		return
	}
	tlsCfg.ClientCAs = pool
	tlsCfg.ClientAuth = metricsClientAuthType(a.cfg.Prometheus.TLS.ClientAuth, caFile)
}

// metricsClientAuthType maps prometheus.tls.client_auth onto crypto/tls's
// ClientAuthType. Validate has already restricted the value to one of these
// five strings or empty, so the default arm only ever sees empty.
//
// EMPTY is not "off": with a client CA configured it means require_and_verify,
// the documented default. Configuring a trust store and then not verifying
// against it is never what an operator meant, and defaulting to a permissive
// mode would make mutual TLS look enabled while accepting anyone. With no client
// CA there is nothing to verify against, so it is NoClientCert.
func metricsClientAuthType(mode, clientCAFile string) tls.ClientAuthType {
	switch mode {
	case "require_and_verify":
		return tls.RequireAndVerifyClientCert
	case "verify_if_given":
		// Migration mode: a scraper without a certificate is served, but one that
		// offers a certificate must still present a trusted one.
		return tls.VerifyClientCertIfGiven
	case "require":
		// Any certificate, unverified. Proves possession of *a* certificate only —
		// useful behind a proxy that already did the verifying.
		return tls.RequireAnyClientCert
	case "request":
		// Asks but does not require; the certificate is available to the handler
		// without gating access. Not an access control.
		return tls.RequestClientCert
	case "none":
		return tls.NoClientCert
	default:
		if clientCAFile != "" {
			return tls.RequireAndVerifyClientCert
		}
		return tls.NoClientCert
	}
}

// Classified outcomes for a /metrics scrape. A CLOSED set of three values, so
// scrape traffic can never grow the series count — the same discipline as the
// admin auth-rejection reasons.
const (
	attrOutcome = "outcome"
	// scrapeOutcomeSuccess is any 2xx.
	scrapeOutcomeSuccess = "success"
	// scrapeOutcomeUnavailable is the 503 promhttp returns when a request is shed
	// by MaxRequestsInFlight or exceeds Timeout — load shedding, not a fault.
	scrapeOutcomeUnavailable = "unavailable"
	// scrapeOutcomeError is anything else (a 500 from a non-ContinueOnError
	// encode failure, say). Deliberately not per-status-code: a status label on a
	// public-ish endpoint is an unbounded set in practice.
	scrapeOutcomeError = "error"
)

// metricsScrapeBucketsSeconds are the /metrics serving-latency buckets. They run
// finer at the bottom than the API histogram (a gather is local work measured in
// single-digit milliseconds when healthy) and top out at 10s, above a typical
// scrape timeout, so a request racing the scraper's own deadline is visible.
var metricsScrapeBucketsSeconds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// scrapeStatusRecorder captures the status code promhttp settles on, which is
// the only place the load-shedding 503s become observable — they are produced
// inside the promhttp handler, not by anything this package can see directly.
type scrapeStatusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *scrapeStatusRecorder) WriteHeader(code int) {
	if !w.written {
		w.status, w.written = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *scrapeStatusRecorder) Write(b []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the wrapper's target when it supports flushing, so wrapping
// cannot silently disable streaming of a large exposition.
func (w *scrapeStatusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// scrapeOutcome maps an HTTP status onto the closed outcome set.
func scrapeOutcome(status int) string {
	switch {
	case status >= 200 && status < 300:
		return scrapeOutcomeSuccess
	case status == http.StatusServiceUnavailable:
		return scrapeOutcomeUnavailable
	default:
		return scrapeOutcomeError
	}
}

// instrumentScrape records request count, in-flight depth and serving latency
// for each /metrics request that got past the auth gate (#377). Rejected
// requests are counted by requireMetricsAuth instead, so a wall of 401s never
// looks like successful scrape traffic here.
//
// It wraps INSIDE the auth gate and OUTSIDE promhttp on purpose: the 503s that
// MaxRequestsInFlight and Timeout produce come from promhttp itself, so only a
// wrapper on that side of it can see them.
//
// metricsScrapeHealthState is updated on EVERY request, self-observability on
// or off — like profilingHealth and telemetry's deliveryTracker, the admin
// status page is an operator's local view and must work on a deployment that
// exports no self-telemetry at all. The OTEL emission itself stays gated
// behind self-obs, so a disabled process pays no exporter cost; it is the same
// single recording site that drives both, which is what keeps the status page
// and the emitted metrics from ever disagreeing.
func (a *App) instrumentScrape(next http.Handler) http.Handler {
	selfObs := a.cfg.SelfObservability.Enabled && a.procEmitter != nil
	var e telemetry.Emitter
	if selfObs {
		e = a.procEmitter
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metricsScrapeHealthState.inFlightDelta(1)
		if selfObs {
			e.UpDownCounter(appcatalog.DocMetricsScrapeInFlight.Name, appcatalog.DocMetricsScrapeInFlight.Unit,
				appcatalog.DocMetricsScrapeInFlight.Description, 1, nil)
		}
		start := time.Now()
		rec := &scrapeStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		// Deferred so a panicking collector still decrements the gauge; leaving it
		// stuck high would make the in-flight metric permanently wrong.
		defer func() {
			metricsScrapeHealthState.inFlightDelta(-1)
			dur := time.Since(start)
			outcome := scrapeOutcome(rec.status)
			metricsScrapeHealthState.recordScrape(outcome, dur)
			if selfObs {
				e.UpDownCounter(appcatalog.DocMetricsScrapeInFlight.Name, appcatalog.DocMetricsScrapeInFlight.Unit,
					appcatalog.DocMetricsScrapeInFlight.Description, -1, nil)
				attrs := telemetry.Attrs{attrOutcome: outcome}
				e.Counter(appcatalog.DocMetricsScrapeRequests.Name, appcatalog.DocMetricsScrapeRequests.Unit,
					appcatalog.DocMetricsScrapeRequests.Description, 1, attrs)
				e.Histogram(appcatalog.DocMetricsScrapeDuration.Name, appcatalog.DocMetricsScrapeDuration.Unit,
					appcatalog.DocMetricsScrapeDuration.Description, dur.Seconds(),
					metricsScrapeBucketsSeconds, attrs)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// maxDistinctGatherErrors bounds the set of gather-error messages remembered for
// first-occurrence WARN logging. A collision is usually one permanent message,
// but the messages are derived from series identity, so an unbounded set would
// be an unbounded map keyed by attacker-influenceable-ish content.
const maxDistinctGatherErrors = 32

// gatherErrorLogger returns the promhttp.Logger that turns a Gather error into
// exactly one log line plus one counter increment (#377).
//
// It exists because ContinueOnError (#103) is deliberately silent: the scrape
// returns HTTP 200 with the surviving series, so a permanent duplicate-series
// collision drops metrics from every single scrape with nothing in the response
// to say so. promhttp's ErrorLog hook is the only notification.
//
// Two shapes of noise are handled. prometheus.MultiError renders multi-line, so
// the text is flattened — one gather error must be one log line or every
// downstream log pipeline splits it into orphaned fragments. And because the
// common cause is permanent, each DISTINCT message is logged at WARN once and at
// DEBUG thereafter: the counter carries the rate, so re-logging at WARN on every
// scrape would only bury the next real problem.
func (a *App) gatherErrorLogger() promhttp.Logger {
	return &gatherErrorLogger{app: a, seen: map[string]struct{}{}}
}

type gatherErrorLogger struct {
	app  *App
	mu   sync.Mutex
	seen map[string]struct{}
}

func (l *gatherErrorLogger) Println(v ...any) {
	msg := strings.Join(strings.Fields(strings.TrimSpace(fmt.Sprintln(v...))), " ")

	l.mu.Lock()
	_, repeat := l.seen[msg]
	if !repeat && len(l.seen) < maxDistinctGatherErrors {
		l.seen[msg] = struct{}{}
	}
	l.mu.Unlock()

	log := l.app.logger.Warn
	if repeat {
		log = l.app.logger.Debug
	}
	log("prometheus gather error while serving /metrics",
		"error", msg,
		"effect", "the affected series are omitted from this scrape; the response is still HTTP 200 (ContinueOnError)")

	// metricsScrapeHealthState is updated unconditionally (see instrumentScrape):
	// the status page must show a gather error even with self-obs off. The
	// classification, never msg itself, is what reaches the DTO (#377) — see
	// classifyGatherError.
	metricsScrapeHealthState.recordGatherError(classifyGatherError(msg))

	if l.app.cfg.SelfObservability.Enabled && l.app.procEmitter != nil {
		l.app.procEmitter.Counter(appcatalog.DocMetricsScrapeGatherErrors.Name,
			appcatalog.DocMetricsScrapeGatherErrors.Unit,
			appcatalog.DocMetricsScrapeGatherErrors.Description, 1, nil)
	}
}

// Gather-error classification (#377). A CLOSED set: whatever gatherErrorLogger
// observes is reduced to one of these five fixed strings and NEVER the raw
// message, because prometheus/client_golang's registry.go wraps a Collect()
// failure verbatim ("error collecting metric %v: %w") — the %w can carry
// arbitrary text from a custom collector, which is exactly where a backend
// error, a credential, or other internal detail could appear. Every branch of
// classifyGatherError below returns one of these fixed constants alone, so the
// safety property holds regardless of how accurately a given message is
// matched — a message that matches nothing still lands on gatherErrClassOther,
// never on its own text.
const (
	// gatherErrClassDuplicateSeries covers the two "was collected before" /
	// "collides with previously collected" shapes registry.go produces for a
	// duplicate-series collision (the #103 scenario this file already handles
	// by keeping the scrape at 200).
	gatherErrClassDuplicateSeries = "duplicate_series"
	// gatherErrClassLabelInconsistent covers a metric's own label set being
	// malformed: repeated label names, an invalid label name, a non-UTF8
	// value, or an explicit reserved label.
	gatherErrClassLabelInconsistent = "label_inconsistent"
	// gatherErrClassTypeMismatch covers a collected metric disagreeing with
	// its registered Desc: wrong type, unregistered descriptor, or
	// inconsistent help/label metadata across calls.
	gatherErrClassTypeMismatch = "type_mismatch"
	// gatherErrClassCollectorError is registry.go's "error collecting metric"
	// wrapper — the one shape that can carry ARBITRARY collector-supplied
	// text, which is exactly why every class here is a fixed string rather
	// than anything derived from msg.
	gatherErrClassCollectorError = "collector_error"
	// gatherErrClassOther is every message that matches none of the above —
	// including one crafted or coincidental enough to look like it should,
	// since matching is substring-based and best-effort, not exhaustive.
	gatherErrClassOther = "other"
)

// classifyGatherError reduces a promhttp gather-error message to one of the
// gatherErrClass* constants above, matching the fixed message shapes
// prometheus/client_golang's registry.go Gather() produces. It never returns
// or derives anything from msg's actual content.
func classifyGatherError(msg string) string {
	switch {
	case strings.Contains(msg, "was collected before with the same name and label values"),
		strings.Contains(msg, "collides with previously collected"):
		return gatherErrClassDuplicateSeries
	case strings.Contains(msg, "has two or more labels with the same name"),
		strings.Contains(msg, "has a label with an invalid name"),
		strings.Contains(msg, "is not utf8"),
		strings.Contains(msg, "must not have an explicit"):
		return gatherErrClassLabelInconsistent
	case strings.Contains(msg, "should be a Counter"),
		strings.Contains(msg, "should be a Gauge"),
		strings.Contains(msg, "should be a Summary"),
		strings.Contains(msg, "should be Untyped"),
		strings.Contains(msg, "should be a Histogram"),
		strings.Contains(msg, "with unregistered descriptor"),
		strings.Contains(msg, "inconsistent label names or help strings"),
		strings.Contains(msg, "are inconsistent with descriptor"),
		strings.Contains(msg, "has help"):
		return gatherErrClassTypeMismatch
	case strings.Contains(msg, "error collecting metric"):
		return gatherErrClassCollectorError
	default:
		return gatherErrClassOther
	}
}

// metricsAuthRejected records one /metrics auth-gate rejection, classified by a
// value from the CLOSED reason set shared with the admin gate.
//
// metricsScrapeHealthState.recordAuthRejected is unconditional (#377): the
// status page's total must reflect rejections even with self-obs off.
func (a *App) metricsAuthRejected(reason string) {
	metricsScrapeHealthState.recordAuthRejected()
	if a.cfg.SelfObservability.Enabled && a.procEmitter != nil {
		a.procEmitter.Counter(appcatalog.DocMetricsAuthRejected.Name, appcatalog.DocMetricsAuthRejected.Unit,
			appcatalog.DocMetricsAuthRejected.Description, 1, telemetry.Attrs{attrReason: reason})
	}
}

// requireMetricsAuth gates next behind prometheus.auth.token when set, reusing the
// constant-time Bearer/Basic check shared with the admin surface (adminAuthorized).
//
// With no token it fails CLOSED on a network-reachable bind, matching
// requireAdminAuth (#315). /metrics carries every series this exporter produces —
// device names, flow endpoints, audit identities — so a default wildcard listener
// with no credential disclosed the whole inventory to anything that could reach
// the port, guarded by nothing but a startup WARN.
//
// Two deliberate escape hatches, because a flat refusal would break real
// deployments:
//   - a loopback bind stays open, as on the admin surface: only this host can
//     reach it, which is what makes local development workable.
//   - prometheus.auth.allow_unauthenticated re-opens a network bind explicitly.
//     An in-cluster scrape behind a NetworkPolicy is legitimate and has
//     network-level control this process cannot observe; the point is that the
//     operator states it rather than inheriting it from a default.
//
// The acknowledgement covers only the no-token case. A configured token is
// enforced on every bind regardless.
//
// The refusal is 403, not 401, and carries no WWW-Authenticate: this is
// misconfiguration rather than a missing credential, and a challenge would make a
// browser prompt for a password that does not exist.
//
// Every rejection increments tailscale2otel.metrics.auth.rejected, keyed by the
// same CLOSED reason set the admin gate uses (#377). The counter — not a log
// line — is the per-request signal: a scraper pointed at the wrong credential
// retries on its scrape interval forever, so a WARN per rejected request buried
// the logs at a rate the operator does not control. The misconfiguration branch
// still logs, but exactly ONCE per process: it names a knob the operator has to
// go and set, and repeating that on every scrape adds nothing.
func (a *App) requireMetricsAuth(next http.Handler) http.Handler {
	token := a.cfg.Prometheus.Auth.Token.Reveal()
	if token == "" {
		if a.cfg.Prometheus.Auth.AllowUnauthenticated {
			return next
		}
		if listenaddr.IsLoopback(a.cfg.Prometheus.Listen) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reason := ""
				switch {
				case !httpguard.IsLoopbackHost(r.Host):
					reason = reasonUntrustedHost
				case !httpguard.SameOrigin(r):
					reason = reasonCrossSiteRequest
				}
				if reason != "" {
					a.metricsAuthRejected(reason)
					http.Error(w, "metrics access refused: tokenless loopback mode requires a loopback Host and same-origin browser request", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
		var warnOnce sync.Once
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.metricsAuthRejected(reasonAuthRequired)
			warnOnce.Do(func() {
				a.logger.Warn("metrics request rejected: no prometheus.auth.token configured on a network-reachable bind",
					"reason", reasonAuthRequired, "path", r.URL.Path, "listen", a.cfg.Prometheus.Listen,
					"logged", "once per process; every rejection is counted by tailscale2otel.metrics.auth.rejected",
					"remedy", "set prometheus.auth.token, bind prometheus.listen to loopback, or set prometheus.auth.allow_unauthenticated")
			})
			http.Error(w,
				"metrics access refused: /metrics exposes every series (device names, flow endpoints, "+
					"audit identities). Set prometheus.auth.token, bind prometheus.listen to loopback "+
					"(127.0.0.1), or acknowledge the exposure with prometheus.auth.allow_unauthenticated=true",
				http.StatusForbidden)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !adminAuthorized(r, token) {
			reason := reasonBadCredentials
			if r.Header.Get("Authorization") == "" {
				reason = reasonMissingCredentials
			}
			a.metricsAuthRejected(reason)
			w.Header().Set("WWW-Authenticate", `Basic realm="tailscale2otel metrics"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runMetrics serves the Prometheus endpoint until ctx is canceled, then shuts
// down gracefully. Mirrors runAdmin, including HTTPS when both prometheus.tls
// files are configured (Validate has already confirmed they exist and are
// readable); otherwise serves plain HTTP, byte-identical to before TLS support
// existed.
func (a *App) runMetrics(ctx context.Context) {
	tlsEnabled := a.cfg.Prometheus.TLS.CertFile != "" && a.cfg.Prometheus.TLS.KeyFile != ""
	errCh := make(chan error, 1)
	go func() {
		if tlsEnabled {
			// Empty paths: see runAdmin's identical comment (#316).
			errCh <- a.metricsSrv.ListenAndServeTLS("", "")
		} else {
			errCh <- a.metricsSrv.ListenAndServe()
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.metricsSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		// Same one readiness source as the admin server and the receivers (#306):
		// a Prometheus listener that never bound must not leave the process
		// reporting itself ready.
		a.recordComponentStop(appcatalog.ComponentMetrics, err)
	}
}

// metricsScrapeHealth records the outcome of every /metrics request that
// passed the auth gate, plus gather-error and auth-rejection health, for the
// admin status page (#377).
//
// Like profilingHealth (profilinghealth.go) and internal/telemetry's
// deliveryTracker, it is populated regardless of self-observability — the
// admin page is an operator's LOCAL view and has to work on a deployment that
// exports no self-telemetry at all. Written from the HTTP handler goroutines
// (instrumentScrape, gatherErrorLogger, requireMetricsAuth) and read from the
// admin handler, so every access is mutex-guarded.
type metricsScrapeHealth struct {
	mu sync.Mutex

	scrapesTotal  int64
	scrapesShed   int64
	scrapesFailed int64
	inFlight      int64

	lastScrapeAt         time.Time
	lastScrapeDurationMs int64

	gatherErrors      int64
	lastGatherError   string
	lastGatherErrorAt time.Time

	authRejected int64
}

func newMetricsScrapeHealth() *metricsScrapeHealth { return &metricsScrapeHealth{} }

// metricsScrapeHealthState is the process-wide /metrics-serving health
// tracker. Package-level rather than an *App field for the same reason as
// profilingHealthState: there is exactly one Prometheus pull-endpoint listener
// per process (buildMetricsServer is called once from app.New), so a
// package-level singleton avoids adding a field to the composition root just
// to thread a pointer through. Tests swap it out and restore it (see
// resetMetricsScrapeHealth in metrics_status_test.go).
var metricsScrapeHealthState = newMetricsScrapeHealth()

// inFlightDelta adjusts the current in-flight request count. A nil receiver is
// a no-op so a zero-value App in an older test stays valid.
func (h *metricsScrapeHealth) inFlightDelta(delta int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.inFlight += delta
	h.mu.Unlock()
}

// recordScrape records one completed request that passed the auth gate.
// outcome is one of the scrapeOutcome* constants.
func (h *metricsScrapeHealth) recordScrape(outcome string, dur time.Duration) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scrapesTotal++
	switch outcome {
	case scrapeOutcomeUnavailable:
		h.scrapesShed++
	case scrapeOutcomeError:
		h.scrapesFailed++
	}
	h.lastScrapeAt = time.Now()
	h.lastScrapeDurationMs = dur.Milliseconds()
}

// recordGatherError records one Gather() call that returned at least one
// error. class must already be one of the gatherErrClass* constants —
// classifyGatherError's whole job is to guarantee that before this is ever
// called with anything derived from the actual error text.
func (h *metricsScrapeHealth) recordGatherError(class string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gatherErrors++
	h.lastGatherError = class
	h.lastGatherErrorAt = time.Now()
}

// recordAuthRejected records one auth-gate rejection, any reason.
func (h *metricsScrapeHealth) recordAuthRejected() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.authRejected++
	h.mu.Unlock()
}

// snapshot returns the tracker's state as the status-page DTO, leaving
// Enabled and Config zero-valued — metricsScrapeInfo fills those in from the
// live config, which the tracker itself does not have access to. Timestamps
// render as RFC3339 and are omitted while zero, matching
// profilingHealth.snapshot and every other *At field on this page.
func (h *metricsScrapeHealth) snapshot() statusdata.MetricsServingInfo {
	if h == nil {
		return statusdata.MetricsServingInfo{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := statusdata.MetricsServingInfo{
		ScrapesTotal:         h.scrapesTotal,
		ScrapesShed:          h.scrapesShed,
		ScrapesFailed:        h.scrapesFailed,
		InFlight:             h.inFlight,
		LastScrapeDurationMs: h.lastScrapeDurationMs,
		GatherErrors:         h.gatherErrors,
		LastGatherError:      h.lastGatherError,
		AuthRejected:         h.authRejected,
	}
	if !h.lastScrapeAt.IsZero() {
		out.LastScrapeAt = h.lastScrapeAt.UTC().Format(time.RFC3339)
	}
	if !h.lastGatherErrorAt.IsZero() {
		out.LastGatherErrorAt = h.lastGatherErrorAt.UTC().Format(time.RFC3339)
	}
	return out
}

// metricsScrapeInfo assembles the full status-page DTO: the live tracker
// snapshot plus the effective, non-secret prometheus.* configuration actually
// in force (#377). Called from buildStatus (status.go).
func (a *App) metricsScrapeInfo() statusdata.MetricsServingInfo {
	info := metricsScrapeHealthState.snapshot()
	info.Enabled = a.cfg.Prometheus.Enabled
	info.Config = statusdata.MetricsServingConfig{
		Listen:               a.cfg.Prometheus.Listen,
		MaxRequestsInFlight:  effectiveMetricsMaxRequests(a.cfg.Prometheus.MaxRequestsInFlight),
		TimeoutMs:            a.cfg.Prometheus.Timeout.D().Milliseconds(),
		CoalesceGather:       a.cfg.Prometheus.CoalesceGather,
		AuthTokenSet:         a.cfg.Prometheus.Auth.Token.Reveal() != "",
		AllowUnauthenticated: a.cfg.Prometheus.Auth.AllowUnauthenticated,
		TLSEnabled:           a.cfg.Prometheus.TLS.CertFile != "" && a.cfg.Prometheus.TLS.KeyFile != "",
		ClientCAConfigured:   a.cfg.Prometheus.TLS.ClientCAFile != "",
		ClientAuthMode:       a.cfg.Prometheus.TLS.ClientAuth,
	}
	return info
}
