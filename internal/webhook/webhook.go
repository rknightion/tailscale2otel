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
// When Options.Tolerance > 0, requests whose timestamp is older than the
// tolerance are rejected as possible replays. A tolerance of 0 disables the
// staleness check, which keeps tests using fixed timestamps deterministic.
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
	"time"

	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

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

	// eventNamePrefix is prepended to the Tailscale event type to form the OTEL
	// LogRecord EventName, e.g. "tailscale.webhook.nodeCreated".
	eventNamePrefix = "tailscale.webhook."

	// attrType is the low-cardinality event-type attribute.
	attrType = "tailscale.webhook.type"
	// attrReason labels a rejection by cause.
	attrReason = "reason"
)

// warnSubstrings are the case-sensitive substrings of an event type that
// promote a record to WARN severity (e.g. nodeKeyExpiringInOneDay, userNeedsApproval,
// nodeDeleted, userSuspended).
var warnSubstrings = []string{"Expir", "Suspend", "NeedsApproval", "Deleted"}

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
}

// Server receives and verifies Tailscale webhook POSTs and emits telemetry.
type Server struct {
	opts   Options
	e      telemetry.Emitter
	logger *slog.Logger
	now    func() time.Time // injectable clock; defaults to time.Now
	dedup  *dedup.Set       // optional cross-source de-dup set (see WithDedup)
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

// event mirrors a single Tailscale webhook event. Field names and types match
// Tailscale's documented payload and official example consumer.
type event struct {
	Timestamp string            `json:"timestamp"` // RFC3339
	Version   int               `json:"version"`
	Type      string            `json:"type"`
	Tailnet   string            `json:"tailnet"`
	Message   string            `json:"message"`
	Data      map[string]string `json:"data"`
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
		opts:   opts,
		e:      e,
		logger: logger,
		now:    time.Now,
	}
	for _, o := range options {
		o(s)
	}
	return s
}

// Handler returns the http.Handler serving the configured Path. It is the unit
// of behaviour exercised by tests via httptest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.opts.Path, s.handle)
	return mux
}

// Run binds opts.Listen, serves Handler at opts.Path, and shuts down gracefully
// when ctx is cancelled. It returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.reject(w, "read_error", "failed to read request body", err)
		return
	}

	if s.opts.Secret != "" {
		if reason, err := s.verify(r.Header.Get(signatureHeader), body); err != nil {
			s.reject(w, reason, "signature verification failed", err)
			return
		}
	}

	var events []event
	if err := json.Unmarshal(body, &events); err != nil {
		s.reject(w, "invalid_body", "failed to parse webhook body", err)
		return
	}

	for _, ev := range events {
		s.emit(ev)
	}

	w.WriteHeader(http.StatusOK)
}

// reject records the rejection counter, logs at Warn, and writes a 401. A
// "read_error" or "invalid_body" reason still uses 401 here for simplicity:
// the body could not be authenticated as a well-formed signed payload.
func (s *Server) reject(w http.ResponseWriter, reason, msg string, err error) {
	s.logger.Warn(msg, "reason", reason, "error", err)
	s.e.Counter(MetricRejected, semconv.UnitDimensionless, "Rejected Tailscale webhook requests", 1, telemetry.Attrs{
		attrReason: reason,
	})
	http.Error(w, "unauthorized", http.StatusUnauthorized)
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
		if ts.Before(s.now().Add(-s.opts.Tolerance)) {
			return "stale_timestamp", errors.New("timestamp older than tolerance")
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

	severity := telemetry.SeverityInfo
	if isWarn(ev.Type) {
		severity = telemetry.SeverityWarn
	}

	s.e.LogEvent(telemetry.Event{
		Name:      eventNamePrefix + ev.Type,
		Body:      ev.Message,
		Severity:  severity,
		Timestamp: parseTimestamp(ev.Timestamp),
		Attrs: telemetry.Attrs{
			attrType:            ev.Type,
			semconv.AttrTailnet: ev.Tailnet,
		},
	})

	s.e.Counter(MetricEvents, semconv.UnitEvents, "Received Tailscale webhook events", 1, telemetry.Attrs{
		attrType: ev.Type,
	})
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

// isWarn reports whether an event type maps to WARN severity.
func isWarn(eventType string) bool {
	for _, sub := range warnSubstrings {
		if strings.Contains(eventType, sub) {
			return true
		}
	}
	return false
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
