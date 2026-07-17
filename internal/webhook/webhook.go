// Package webhook implements an HTTP receiver for Tailscale webhook events.
//
// Tailscale posts a JSON array of events to a configured endpoint and signs the
// request with an HMAC-SHA256 signature derived from a per-webhook secret. The
// Server verifies that signature (using the scheme documented at
// https://tailscale.com/kb/1213/webhooks), then emits one OTEL log record and
// one counter increment per event via the telemetry.Emitter facade.
//
// Signature scheme (verified against Tailscale's official example consumer):
//
//	Header: Tailscale-Webhook-Signature: t=<unixSeconds>,v1=<hex>
//	  - There may be multiple v1=<hex> entries (e.g. during secret rotation);
//	    a match against any one is sufficient.
//	Signed string: <unixSeconds> + "." + <raw request body>
//	Signature:     hex(HMAC-SHA256(secret, signedString))
//	Comparison:    constant time (subtle.ConstantTimeCompare) over each v1 value.
//
// When Options.Tolerance > 0, requests are rejected as possible replays if
// their timestamp falls outside [now-Tolerance, now+Tolerance] — too old
// ("stale_timestamp") OR too far in the future ("future_timestamp"). The
// future-side check matters because a correctly-signed request timestamped
// arbitrarily far ahead would otherwise be accepted immediately and remain
// replayable until (its future timestamp + Tolerance), turning a short
// clock-skew allowance into a much longer replay window. A tolerance of 0
// disables both checks, which keeps tests using fixed timestamps
// deterministic.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

// requestDurationBucketsSeconds are the explicit histogram bucket boundaries
// for tailscale.webhook.request.duration (in seconds).
var requestDurationBucketsSeconds = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// receiverPropagator extracts W3C TraceContext from incoming request headers.
// Tailscale's sender won't send a traceparent header, so extraction yields an
// empty parent and the span becomes a root — that's correct.
var receiverPropagator = propagation.TraceContext{}

// noopWebhookTracer is a package-level cached noop tracer used when no tracer
// is configured, avoiding per-request allocations.
var noopWebhookTracer = tracenoop.NewTracerProvider().Tracer("")

const (
	// signatureHeader is the request header carrying the signed timestamp and
	// one or more HMAC signatures.
	signatureHeader = "Tailscale-Webhook-Signature"
	// signatureVersion is the only signature scheme Tailscale currently emits.
	signatureVersion = "v1"

	// MetricEvents counts received webhook events, keyed only by event type
	// (low cardinality).
	MetricEvents = "tailscale.webhook.events"
	// MetricRejected counts rejected webhook requests, keyed by rejection reason.
	MetricRejected = "tailscale.webhook.rejected"

	// defaultMaxBodyBytes caps webhook request bodies when Options.MaxBodyBytes is
	// 0. Real Tailscale webhook payloads are KB-scale (a handful of JSON events
	// per delivery, see https://tailscale.com/kb/1213/webhooks), so 1 MiB gives
	// generous headroom without buffering pre-auth requests at the 64 MiB size
	// sized for the streaming receiver's batch flow/audit logs.
	defaultMaxBodyBytes = 1 << 20 // 1 MiB

	// eventNamePrefix is prepended to the Tailscale event type to form the OTEL
	// LogRecord EventName, e.g. "tailscale.webhook.nodeCreated".
	eventNamePrefix = "tailscale.webhook."

	// attrType is the low-cardinality event-type attribute.
	attrType = "tailscale.webhook.type"
	// attrReason labels a rejection by cause.
	attrReason = "reason"

	// maxDistinctEventTypes caps how many distinct event-type values are used as a
	// metric attribute / log EventName before further new types collapse into
	// overflowType. The event type is attacker-chosen on the wire, so when the
	// receiver runs without a secret (verification skipped) an unauthenticated
	// flood of unique types would otherwise explode the events metric's series and
	// the log EventName cardinality. The cap sits well above Tailscale's documented
	// event set (~25 types, see severityByType) so real traffic — and headroom for
	// new types — passes through verbatim; only an abnormal flood overflows.
	maxDistinctEventTypes = 64
	// overflowType is the single bucket attacker/novel types collapse into once the
	// distinct-type cap is reached.
	overflowType = "other"
)

// severityByType is the explicit, per-type log-severity classification for
// webhook events. It replaces an earlier substring heuristic ({Expir, Suspend,
// NeedsApproval, Deleted}) that MISSED nodeNeedsSignature and the deprecated
// nodeNeedsAuthorization (neither contains a matched substring), emitting both
// at INFO when they warrant WARN. Only types whose severity is NOT the default
// INFO are listed; severityForType returns INFO for everything else. The
// authoritative event catalog is https://tailscale.com/kb/1213/webhooks#events
// (see todos.txt S4-11(a)).
//
// Deliberately INFO (not listed): the client-misconfiguration health events
// exitNodeIPForwardingNotEnabled and subnetIPForwardingNotEnabled — they are
// surfaced via the events counter and a dedicated Prometheus alert (see
// deploy/alerts/), not by elevating log severity.
var severityByType = map[string]telemetry.Severity{
	// Node key expiry — the device drops off the tailnet when the key expires.
	"nodeKeyExpired":          telemetry.SeverityWarn,
	"nodeKeyExpiringInOneDay": telemetry.SeverityWarn,
	// Pending approvals — a node/user is blocked until an admin acts.
	"nodeNeedsApproval": telemetry.SeverityWarn,
	"userNeedsApproval": telemetry.SeverityWarn,
	// Deprecated alias of nodeNeedsApproval (still delivered until disabled).
	"nodeNeedsAuthorization": telemetry.SeverityWarn,
	// Tailnet Lock — a node is blocked from the tailnet until a trusted node signs it.
	"nodeNeedsSignature": telemetry.SeverityWarn,
	// Deletions are notable, irreversible config changes.
	"nodeDeleted":    telemetry.SeverityWarn,
	"webhookDeleted": telemetry.SeverityWarn,
	// Undocumented in the catalog above but historically observed; kept at WARN
	// (matching prior substring behavior) pending live verification — remove if
	// invalid (todos.txt S4-11(c), gated on the S4-10 capture).
	"userSuspended": telemetry.SeverityWarn,
	"userDeleted":   telemetry.SeverityWarn,
}

// Options configures a Server.
type Options struct {
	// Listen is the TCP address ListenAndServe binds (e.g. ":9099"). Only used
	// by Run; tests drive Handler directly.
	Listen string
	// Path is the single route that accepts webhook POSTs (e.g. "/webhook").
	Path string
	// Secret is the per-webhook signing secret. When empty, signature
	// verification is skipped (useful for local testing behind a trusted proxy).
	Secret string
	// Tolerance is the maximum age of a request timestamp before it is rejected
	// as a replay. Zero disables the check.
	Tolerance time.Duration
	// MaxBodyBytes caps the raw request body size before signature verification,
	// bounding unauthenticated memory use. 0 selects a 1 MiB default (real
	// Tailscale webhook payloads are KB-scale); a negative value disables the cap.
	MaxBodyBytes int64
	// OnIngest, when non-nil, is called once after a successful parse with
	// (IngestSourceWebhook, IngestSignalWebhook, len(events), len(body)).
	// Supplied by the app, gated on self-observability.
	OnIngest func(source, signal string, records, bytes int)
}

// Server receives and verifies Tailscale webhook POSTs and emits telemetry.
type Server struct {
	opts     Options
	e        telemetry.Emitter
	logger   *slog.Logger
	now      func() time.Time // injectable clock; defaults to time.Now
	dedup    *dedup.Set       // optional cross-source de-dup set (see WithDedup)
	onIngest func(source, signal string, records, bytes int)
	tracer   trace.Tracer

	// typesMu guards seenTypes, the bounded set of distinct event types already
	// admitted as a telemetry dimension. handle (and thus emit) runs concurrently
	// per request, so access is mutex-guarded. See boundType / maxDistinctEventTypes.
	typesMu   sync.Mutex
	seenTypes map[string]struct{}
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithDedup attaches a cross-SOURCE de-duplication set shared with the audit
// Processor (see audit.WithCrossDedup). When set is non-nil, a webhook event
// that maps to a change already recorded in set — by the audit poller/stream or
// a prior webhook — is suppressed (no log record, no counter increment) so
// enabling both webhooks and audit-log polling does not double-count. This is
// BEST-EFFORT (see crossKey); a nil set is a no-op.
func WithDedup(set *dedup.Set) Option {
	return func(s *Server) { s.dedup = set }
}

// WithTracer sets the tracer for one span per received webhook request. A nil
// tracer disables span emission (the server falls back to the noop tracer).
func WithTracer(tr trace.Tracer) Option { return func(s *Server) { s.tracer = tr } }

// event mirrors a single Tailscale webhook event. Field names and types match
// Tailscale's documented payload and official example consumer.
//
// Data values are kept as raw JSON because they are NOT uniformly flat strings:
// userRoleUpdated carries array-valued oldRoles/newRoles, and policyUpdate carries
// large oldPolicy/newPolicy strings (kb/1213). A map[string]string here would make
// json.Unmarshal fail on the array values and reject the WHOLE delivery (S4-11e).
type event struct {
	Timestamp string                     `json:"timestamp"` // RFC3339
	Version   int                        `json:"version"`
	Type      string                     `json:"type"`
	Tailnet   string                     `json:"tailnet"`
	Message   string                     `json:"message"`
	Data      map[string]json.RawMessage `json:"data"`
}

// New returns a Server that verifies against opts.Secret and emits via e.
// A nil logger is replaced with a no-op logger. Optional Options (e.g. WithDedup)
// are applied after construction.
func New(opts Options, e telemetry.Emitter, logger *slog.Logger, options ...Option) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Path == "" {
		opts.Path = "/webhook"
	}
	s := &Server{
		opts:     opts,
		e:        e,
		logger:   logger,
		now:      time.Now,
		onIngest: opts.OnIngest,
	}
	for _, o := range options {
		o(s)
	}
	return s
}

// Handler returns the http.Handler serving the configured Path. It is the unit
// of behavior exercised by tests via httptest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handle)
	return mux
}

// Run binds opts.Listen, serves Handler at opts.Path, and shuts down gracefully
// when ctx is canceled. It returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// handle is the core request handler: it accepts only POST, verifies the
// signature, parses the event array, and emits telemetry.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// Record in-flight count and request duration unconditionally, balanced even
	// on panic or early return. +1 immediately, -1 in defer (balanced pair).
	start := time.Now()
	s.e.UpDownCounter(docWebhookInflight.Name, docWebhookInflight.Unit, docWebhookInflight.Description, 1, nil)
	defer func() {
		s.e.UpDownCounter(docWebhookInflight.Name, docWebhookInflight.Unit, docWebhookInflight.Description, -1, nil)
		s.e.Histogram(docWebhookRequestDuration.Name, docWebhookRequestDuration.Unit, docWebhookRequestDuration.Description,
			time.Since(start).Seconds(), requestDurationBucketsSeconds, nil)
	}()

	// Start a server span for this request. W3C trace-context is extracted from
	// headers; Tailscale's sender won't send a traceparent, so the span becomes a
	// root — that's correct. The span ends via defer regardless of exit path.
	tr := s.tracer
	if tr == nil {
		tr = noopWebhookTracer
	}
	ctx := receiverPropagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := tr.Start(ctx, "webhook.receive", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	r = r.WithContext(ctx)

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		span.SetStatus(codes.Error, "method not allowed")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	body, err := s.readBody(w, r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			span.SetStatus(codes.Error, "request body exceeds max size")
			s.rejectStatus(w, http.StatusRequestEntityTooLarge, "too_large", "request body exceeds max size", err)
			return
		}
		span.SetStatus(codes.Error, "failed to read request body")
		s.reject(w, "read_error", "failed to read request body", err)
		return
	}

	if s.opts.Secret != "" {
		if reason, err := s.verify(r.Header.Get(signatureHeader), body); err != nil {
			span.SetStatus(codes.Error, reason)
			s.reject(w, reason, "signature verification failed", err)
			return
		}
	}

	var events []event
	if err := json.Unmarshal(body, &events); err != nil {
		span.SetStatus(codes.Error, "failed to parse webhook body")
		s.reject(w, "invalid_body", "failed to parse webhook body", err)
		return
	}

	if s.onIngest != nil {
		s.onIngest(semconv.IngestSourceWebhook, semconv.IngestSignalWebhook, len(events), len(body))
	}

	for _, ev := range events {
		s.emit(ev)
	}

	// Record aggregate counts and body size on the span before the success response.
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Int("tailscale.webhook.events", len(events)),
			attribute.Int("http.request.body.size", len(body)),
		)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	limit := s.opts.MaxBodyBytes
	if limit == 0 {
		limit = defaultMaxBodyBytes
	}
	reader := r.Body
	if limit >= 0 {
		reader = http.MaxBytesReader(w, r.Body, limit)
	}
	return io.ReadAll(reader)
}

// reject records the rejection counter, logs at Warn, and writes a 401. A
// "read_error" or "invalid_body" reason still uses 401 here for simplicity:
// the body could not be authenticated as a well-formed signed payload.
func (s *Server) reject(w http.ResponseWriter, reason, msg string, err error) {
	s.rejectStatus(w, http.StatusUnauthorized, reason, msg, err)
}

func (s *Server) rejectStatus(w http.ResponseWriter, status int, reason, msg string, err error) {
	s.logger.Warn(msg, "reason", reason, "error", err)
	s.e.Counter(docWebhookRejected.Name, docWebhookRejected.Unit, docWebhookRejected.Description, 1, telemetry.Attrs{
		attrReason: reason,
	})
	http.Error(w, http.StatusText(status), status)
}

// verify checks the signature header against the body using opts.Secret. It
// returns a short rejection reason and an error on failure, or ("", nil) on
// success.
func (s *Server) verify(header string, body []byte) (string, error) {
	if header == "" {
		return "missing_signature", errors.New("missing signature header")
	}

	ts, sigs, err := parseSignatureHeader(header)
	if err != nil {
		return "malformed_signature", err
	}

	if s.opts.Tolerance > 0 {
		now := s.now()
		if ts.Before(now.Add(-s.opts.Tolerance)) {
			return "stale_timestamp", errors.New("timestamp older than tolerance")
		}
		if ts.After(now.Add(s.opts.Tolerance)) {
			return "future_timestamp", errors.New("timestamp newer than tolerance")
		}
	}

	want := s.expectedSignature(ts, body)
	for _, got := range sigs {
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return "", nil
		}
	}
	return "bad_signature", errors.New("no matching signature")
}

// expectedSignature computes hex(HMAC-SHA256(secret, <unixSeconds>.<body>)).
func (s *Server) expectedSignature(ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.opts.Secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// emit converts one event into an OTEL log record plus a counter increment.
// The counter carries only the low-cardinality event type.
func (s *Server) emit(ev event) {
	if s.dedup != nil {
		if key, ok := crossKey(ev); ok && !s.dedup.Add(key) {
			// Same change already emitted via the audit logs (or a prior webhook):
			// suppress to avoid double-counting.
			return
		}
	}

	// The event type is attacker-chosen on the wire; bound its distinct values
	// before using it as a metric attribute or log EventName. Severity is still
	// derived from the ORIGINAL type (its lookup table is itself bounded), so a
	// legitimately new WARN type is classified correctly even if it overflows.
	dim := s.boundType(ev.Type)

	s.e.LogEvent(telemetry.Event{
		Name:      eventNamePrefix + dim,
		Body:      ev.Message,
		Severity:  severityForType(ev.Type),
		Timestamp: parseTimestamp(ev.Timestamp),
		// The body is the attacker/upstream-supplied free-text message; classify it
		// so a disabled free_text_details drops it from the body, not just attrs (#197).
		BodyPII: []pii.Category{pii.CatFreeTextDetails},
		Attrs: telemetry.Attrs{
			attrType:            dim,
			semconv.AttrTailnet: ev.Tailnet,
		},
	})

	s.e.Counter(docWebhookEvents.Name, docWebhookEvents.Unit, docWebhookEvents.Description, 1, telemetry.Attrs{
		attrType: dim,
	})
}

// boundType maps an event type to the value used as a telemetry dimension,
// collapsing types beyond maxDistinctEventTypes distinct values into overflowType.
// Already-admitted types (and overflowType itself) always pass through, so the
// dimension's cardinality is capped at maxDistinctEventTypes+1 for the process
// lifetime. Safe for concurrent use.
func (s *Server) boundType(t string) string {
	s.typesMu.Lock()
	defer s.typesMu.Unlock()
	if _, ok := s.seenTypes[t]; ok {
		return t
	}
	if len(s.seenTypes) >= maxDistinctEventTypes {
		return overflowType
	}
	if s.seenTypes == nil {
		s.seenTypes = make(map[string]struct{}, maxDistinctEventTypes)
	}
	s.seenTypes[t] = struct{}{}
	return t
}

// parseSignatureHeader splits the header into its timestamp and the list of v1
// signatures. Unknown keys are ignored. An empty or malformed header is an error.
func parseSignatureHeader(header string) (time.Time, []string, error) {
	var (
		ts     time.Time
		haveTS bool
		sigs   []string
	)
	for pair := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return time.Time{}, nil, errors.New("malformed signature element")
		}
		switch strings.TrimSpace(k) {
		case "t":
			secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, nil, errors.New("invalid timestamp in signature header")
			}
			ts = time.Unix(secs, 0)
			haveTS = true
		case signatureVersion:
			sigs = append(sigs, strings.TrimSpace(v))
		default:
			// Ignore unknown elements for forward compatibility.
		}
	}
	if !haveTS || len(sigs) == 0 {
		return time.Time{}, nil, errors.New("signature header missing timestamp or signature")
	}
	return ts, sigs, nil
}

// severityForType returns the log severity for a webhook event type, defaulting
// to INFO for any type not enumerated in severityByType.
func severityForType(eventType string) telemetry.Severity {
	if sev, ok := severityByType[eventType]; ok {
		return sev
	}
	return telemetry.SeverityInfo
}

// parseTimestamp parses an RFC3339 event timestamp, returning the zero time
// (which the emitter treats as "now") when the value is absent or unparseable.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
