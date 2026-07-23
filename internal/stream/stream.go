// Package stream implements a streaming receiver that emulates a Splunk HTTP
// Event Collector (HEC) endpoint so Tailscale "log streaming" can push
// network-flow and configuration-audit logs to this collector. Received records
// are converted through the SAME shared processors (internal/flowlog and
// internal/audit) used by the polling collectors, so streamed and polled data
// produce identical OTEL metrics and log records.
//
// # Envelope (PINNED by a live capture — S4-10)
//
// The exact wire format was captured from the real TailscaleLogStreamPublisher
// against the example-tailnet lab (2026-06-03). Each POST body is one-or-more concatenated
// Splunk-HEC objects with NO separators, each shaped:
//
//	{"time":<unixFloat>,"event":{<record>},"fields":{"recorded":<rfc3339>}}
//
// i.e. one network-flow or configuration-audit record per "event", many such
// objects per request (a network POST observed ~73). Authentication is HTTP
// Basic auth, base64("<user>:<token>") — NOT "Authorization: Splunk <token>" —
// where the password is the configured token (see authorized). Bodies arrive
// chunked (Transfer-Encoding: chunked), which Go's net/http decodes transparently.
//
// The per-record shapes match the poll API exactly: flow records carry a NUMERIC
// "proto" (e.g. 6 for TCP, 99 here) plus srcNode/dstNodes, and audit records use
// "actionDetails" with polymorphic "old"/"new". A streamed audit record has NO
// inner "eventTime" — its timestamp lives in the HEC "time" (unix seconds) /
// "fields.recorded" (RFC3339), siblings of "event". The parser threads that
// envelope time through (see extractRecords/unwrap/envelopeTime) and the handler
// applies it to an audit record's EventTime when the record has none of its own,
// so a streamed audit log bears the event's real occurrence time rather than the
// OTEL ingest time (S4-10 fidelity fix). Flow records carry their own start/end
// and ignore the envelope time.
//
// The parser stays DEFENSIVE and accepts the union of plausible shapes (the HEC
// "event" wrapper above plus the variants below) so an unexpected sender or a
// future format change degrades gracefully rather than dropping everything:
//
//   - a single JSON object;
//   - newline-delimited JSON (NDJSON), the HEC norm — one JSON object per line;
//   - a Splunk-HEC wrapper {"event": <record>, ...} — the "event" field is
//     unwrapped and classified;
//   - a Tailscale batch wrapper {"logs": [<record>, ...]} — each element is
//     classified (this is also the shape the .capture files use at top level).
//
// # Atomic batch delivery (#201)
//
// The receiver treats each POST as an ALL-OR-NOTHING batch: it parses and
// type-checks the COMPLETE body before routing a single record to a processor,
// so a request is never acknowledged with 200 after silently dropping part of
// its payload. Two conditions reject the whole request with a 4xx and emit
// NOTHING (no partial prefix):
//
//   - the body is structurally corrupt/truncated — a mid-stream JSON syntax
//     error (a torn concatenated batch), or an invalid line in an NDJSON body
//     (rejected{reason=malformed}); or
//   - a record classifies as a KNOWN type (flow/audit) but fails typed decoding
//     (rejected{reason=decode_error} — e.g. a wire-format change we no longer
//     understand).
//
// The sender then retries rather than treating the partial loss as delivered.
// This deliberately REPLACES the earlier valid-prefix salvage (#96): salvaging a
// truncated batch and ACKing it 200 was itself the durability hole #201 closes.
//
// Genuinely-UNKNOWN future record types stay forward-compatible: an object that
// classifies to neither the flow nor the audit shape (or a non-object value in an
// envelope slot) is SKIPPED and counted (#67, MetricSkipped) and the batch still
// SUCCEEDS (200) with the understood records emitted. The distinction is "a known
// record we failed to decode / structural corruption" ⇒ hard reject, versus "a
// record type we do not recognize at all" ⇒ skip.
//
// Each extracted record object is CLASSIFIED by shape, not by a declared type:
//
//   - if it has a non-empty "nodeId" and any of virtualTraffic / subnetTraffic /
//     exitTraffic / physicalTraffic, it is decoded as a flowlog.FlowLog and fed
//     to the flow processor;
//   - otherwise, if it has an "actor" and an "action", it is decoded as an
//     audit.Event and fed to the audit processor;
//   - anything else is counted as skipped.
package stream

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// receiverPropagator extracts W3C TraceContext from incoming request headers.
// Tailscale's sender won't send a traceparent header, so extraction yields an
// empty parent and the span becomes a root — that's correct.
var receiverPropagator = propagation.TraceContext{}

// noopStreamTracer is a package-level cached noop tracer used when no tracer is
// configured, avoiding per-request allocations.
var noopStreamTracer = tracenoop.NewTracerProvider().Tracer("")

// Option configures a Server at construction time.
type Option func(*Server)

// WithTracer sets the tracer for one span per received request. A nil tracer
// disables span emission (the server falls back to the noop tracer).
func WithTracer(tr trace.Tracer) Option { return func(s *Server) { s.tracer = tr } }

// Exported metric names emitted by the receiver.
const (
	// MetricRecords counts records successfully routed to a processor. It
	// carries a low-cardinality "type" attribute ("flow" or "audit").
	MetricRecords = "tailscale.stream.records"
	// MetricRejected counts whole REQUESTS the receiver refused to ingest. It
	// carries a low-cardinality "reason" attribute: "auth", "too_large",
	// "unparsable" (nothing JSON-like), "malformed" (the body was structurally
	// corrupt/truncated), or "decode_error" (a known record failed typed decoding).
	// The last two are the #201 atomic-batch rejections — a corrupt or partially
	// undecodable batch is rejected whole rather than partially ACKed.
	MetricRejected = "tailscale.stream.rejected"
	// MetricDecodeErrors counts records that classified as a known type but whose
	// typed decode failed (a malformed flow/audit record). Carries the "type"
	// attribute ("flow" or "audit"). Under the #201 atomic contract any such record
	// also rejects its whole request (rejected{reason=decode_error}); this counter
	// records which wire type broke.
	MetricDecodeErrors = "tailscale.stream.decode_errors"
	// MetricInflight tracks in-flight HTTP requests currently being processed
	// by the HEC receiver (UpDownCounter: +1 on entry, -1 on return).
	MetricInflight = "tailscale.stream.inflight"
	// MetricRequestDuration is the wall-clock duration of HEC receiver HTTP
	// request handling in seconds (Histogram).
	MetricRequestDuration = "tailscale.stream.request.duration"
	// MetricSkipped counts records that were extracted from an otherwise-valid
	// request body but never reached a processor (#67). It carries a
	// low-cardinality "reason" attribute: "unclassified" (the record didn't
	// match either the flow or audit shape) or "unwrap_drop" (a non-object
	// value — e.g. a scalar/null HEC "event" — was encountered while unwrapping
	// the envelope, before classification could even run). Unlike
	// MetricRejected (a whole REQUEST rejected outright), this counts
	// individual records dropped from a request the receiver still 200s.
	MetricSkipped = "tailscale.stream.skipped"
)

// requestDurationBucketsSeconds are the explicit histogram bucket boundaries
// for tailscale.stream.request.duration (in seconds).
var requestDurationBucketsSeconds = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Attribute keys and values for the receiver's own counters.
const (
	attrType   = "type"
	attrReason = "reason"

	typeFlow  = "flow"
	typeAudit = "audit"

	reasonAuth       = "auth"
	reasonUnparsable = "unparsable"
	reasonTooLarge   = "too_large"

	// reasonMalformed and reasonDecodeError are the two #201 atomic-batch
	// rejection reasons on MetricRejected. reasonMalformed = the body was
	// structurally corrupt/truncated (a mid-stream JSON syntax error, or an
	// invalid line in an NDJSON body); reasonDecodeError = a record classified as
	// a KNOWN type (flow/audit) but failed typed decoding. Either rejects the
	// WHOLE request with no partial emit, distinct from an unknown future record
	// type (which is skipped, not rejected — see reasonUnclassified below).
	reasonMalformed   = "malformed"
	reasonDecodeError = "decode_error"

	// reasonUnclassified and reasonUnwrapDrop are the "reason" values for
	// MetricSkipped (#67): the two ways a record extracted from a valid
	// request body can fail to reach a processor.
	reasonUnclassified = "unclassified"
	reasonUnwrapDrop   = "unwrap_drop"

	// reasonAuthRequired, reasonTooManyRecords and reasonOverloaded are the
	// resource/exposure guards on MetricRejected. reasonAuthRequired = the
	// receiver is network-reachable with no token configured, so it refuses to
	// ingest at all (fail closed); reasonTooManyRecords = the body carried more
	// record objects than maxRecordsPerRequest (#229); reasonOverloaded = the
	// aggregate admission budget was full (#209).
	//
	// An over-deep envelope (#228) deliberately has NO reason of its own here: it
	// is a bounded, forward-compatible per-record DROP counted as
	// skipped{reason=unwrap_drop}, not a request-level rejection.
	reasonAuthRequired   = "auth_required"
	reasonTooManyRecords = "too_many_records"
	reasonOverloaded     = "overloaded"
)

// defaultPath is the Splunk-HEC event endpoint path used when Options.Path is
// empty.
const defaultPath = "/services/collector/event"

// authScheme is the Splunk-HEC Authorization scheme: "Authorization: Splunk
// <token>".
const authScheme = "Splunk"

// defaultMaxBodyBytes caps the decompressed body when Options.MaxBodyBytes is 0.
const defaultMaxBodyBytes = 64 << 20 // 64 MiB

// errBodyTooLarge is returned by readAllLimited when the body exceeds the cap.
var errBodyTooLarge = errors.New("stream: request body exceeds max size")

// maxRecordsPerRequest caps how many record objects one request may yield (#229).
//
// The decompressed-byte cap alone does NOT bound memory here, because bytes
// amplify into objects: a 64 MiB body of concatenated `{}` yields ~33M
// json.RawMessage values plus a parallel []extractedRecord, several GB of
// transient allocation from a body that is comfortably inside the byte limit.
// The count is what has to be bounded, so this cap sits alongside MaxBodyBytes
// rather than replacing it. 500k records is ~3 orders of magnitude above the
// largest batch observed from the real TailscaleLogStreamPublisher (~73), so it
// cannot bite a legitimate sender.
const maxRecordsPerRequest = 500_000

// errTooManyRecords is returned by extractRecords when a body would yield more
// than maxRecordsPerRequest records. The handler maps it to 413 +
// rejected{reason=too_many_records}, matching how the byte cap is surfaced.
var errTooManyRecords = errors.New("stream: request exceeds max record count")

// maxUnwrapDepth bounds envelope-unwrapping recursion (#228). The documented
// shapes nest at most a couple of levels ({"logs":[{"event":<record>}]} is two),
// so 4 leaves headroom for a future wrapper while refusing the adversarial case:
// a highly-compressible body nested thousands of levels deep, which used to be
// re-scanned once per level. Beyond the cap the value is DROPPED (counted as
// skipped{reason=unwrap_drop}), never treated as corruption.
const maxUnwrapDepth = 4

// handlerProcessDeadline bounds how long the receiver will spend on one request
// before answering (#228). See withProcessDeadline for what this does and does
// not buy.
const handlerProcessDeadline = 30 * time.Second

// Listener timeouts. readHeaderTimeout and writeTimeout are load-bearing as a
// PAIR — see writeTimeoutFor — so they live together rather than inline in Run.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 120 * time.Second
	// writeGrace is the slack left for actually writing the deadline's 503 after
	// the worst-case moment it can fire.
	writeGrace = 10 * time.Second
)

// writeTimeoutFor returns the listener write deadline that lets the process
// deadline's 503 actually reach the client (#232).
//
// The two clocks do not start together. http.Server arms the write deadline when
// a request's headers START arriving; http.TimeoutHandler arms its own when
// ServeHTTP is ENTERED, which is up to readHeaderTimeout later. So the deadline
// can fire as late as readHeaderTimeout+d on the connection's clock, and a write
// window merely equal to d (the pre-#232 30s/30s pairing) is always closed first
// — the client got a dropped connection instead of a diagnosable 503.
//
// Deriving the value rather than writing a second constant is the point: the two
// numbers look independently reasonable, so a future tidy-up could re-align them
// and silently reintroduce #232. TestServerTimeouts_WriteWindowOutlastsProcessDeadline
// pins the invariant.
func writeTimeoutFor(d time.Duration) time.Duration {
	return readHeaderTimeout + d + writeGrace
}

// defaultMaxConcurrentRequests is the aggregate admission budget used when
// Options.MaxConcurrentRequests is 0 (#209): at most this many handlers may be
// buffering a request body at once, so worst-case buffered memory is bounded by
// MaxConcurrentRequests * MaxBodyBytes rather than by how many senders show up.
const defaultMaxConcurrentRequests = 4

// admissionWait is how long a request will wait for an admission slot before
// being refused. Long enough to absorb a normal burst (bodies parse in
// milliseconds), short enough that a queue cannot itself become the backlog the
// budget exists to prevent.
const admissionWait = 250 * time.Millisecond

// Options configures a Server.
type Options struct {
	// Listen is the host:port the Run method binds to.
	Listen string
	// Path is the HTTP path the handler serves. Defaults to
	// "/services/collector/event" (the Splunk-HEC event endpoint).
	Path string
	// Token, when non-empty, is the expected bearer token; requests must carry
	// "Authorization: Splunk <Token>". An empty Token disables authentication.
	Token string
	// Decompress selects body decompression: "auto" (default), "gzip", "zstd",
	// or "none". In "auto" mode the Content-Encoding header decides.
	Decompress string
	// TLSCertFile and TLSKeyFile, when both set, make Run serve HTTPS.
	TLSCertFile string
	TLSKeyFile  string
	// MaxBodyBytes caps the DECOMPRESSED request body size; a request whose body
	// exceeds it is rejected with 413 and a rejected{reason=too_large} counter, so
	// a huge or zip-bomb POST cannot OOM the receiver. 0 selects a 64 MiB default;
	// a negative value disables the cap.
	MaxBodyBytes int64
	// MaxConcurrentRequests bounds handlers buffering a body simultaneously.
	// 0 selects defaultMaxConcurrentRequests; negative disables the limit.
	MaxConcurrentRequests int
	// OnIngest, when non-nil, is called with ("stream", signal, records, bytes)
	// after a successful parse: once per non-empty signal (records>0, bytes=0) and
	// once for the decompressed body size (records=0, bytes=len(raw)). Supplied by
	// the app, gated on self-observability.
	OnIngest func(source, signal string, records, bytes int)
}

// Server is the streaming receiver. It is safe to share its Handler across
// goroutines; the underlying processors and Emitter are concurrency-safe.
type Server struct {
	path       string
	token      string
	decompress string
	tlsCert    string
	tlsKey     string
	listen     string
	maxBody    int64

	// admit is the aggregate admission semaphore (#209): a buffered channel whose
	// capacity is the number of handlers allowed to buffer a body at once. nil
	// when the limit is disabled.
	admit chan struct{}
	// insecureOpen records that the receiver has NO token AND is bound somewhere
	// other hosts can reach, i.e. it would ingest unauthenticated data from the
	// network. The handler refuses every request in that state (fail closed).
	insecureOpen bool
	// processDeadline is handlerProcessDeadline in production. It is a field only
	// so tests can drive the deadline/write-window interaction (#232) in
	// milliseconds instead of waiting out the real 30s budget.
	processDeadline time.Duration

	flowProc  *flowlog.Processor
	auditProc *audit.Processor
	emitter   telemetry.Emitter
	logger    *slog.Logger
	onIngest  func(source, signal string, records, bytes int)
	tracer    trace.Tracer
}

// New returns a Server that converts received records via flowProc and
// auditProc and records to e. A nil logger is replaced with a discarding one.
// Optional Options (e.g. WithTracer) are applied after construction.
func New(opts Options, flowProc *flowlog.Processor, auditProc *audit.Processor, e telemetry.Emitter, logger *slog.Logger, options ...Option) *Server {
	path := opts.Path
	if path == "" {
		path = defaultPath
	}
	decompress := opts.Decompress
	if decompress == "" {
		decompress = "auto"
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	maxBody := opts.MaxBodyBytes
	if maxBody == 0 {
		maxBody = defaultMaxBodyBytes
	}
	maxConcurrent := opts.MaxConcurrentRequests
	if maxConcurrent == 0 {
		maxConcurrent = defaultMaxConcurrentRequests
	}
	var admit chan struct{}
	if maxConcurrent > 0 {
		admit = make(chan struct{}, maxConcurrent)
	}
	s := &Server{
		path:       path,
		token:      opts.Token,
		decompress: decompress,
		tlsCert:    opts.TLSCertFile,
		tlsKey:     opts.TLSKeyFile,
		listen:     opts.Listen,
		maxBody:    maxBody,
		admit:      admit,
		// Fail closed: with no token, only a loopback bind is safe, because a
		// loopback listener is unreachable from any other host. Everything else —
		// a wildcard bind, a LAN address, a tailnet (100.64/10) address, or an
		// unparseable value — is treated as network-reachable, so an operator who
		// forgets streaming.token gets a refusal rather than an open ingest
		// endpoint. listenaddr.IsLoopback itself fails closed on anything it
		// cannot classify.
		insecureOpen:    opts.Token == "" && !listenaddr.IsLoopback(opts.Listen),
		processDeadline: handlerProcessDeadline,
		flowProc:        flowProc,
		auditProc:       auditProc,
		emitter:         e,
		logger:          logger,
		onIngest:        opts.OnIngest,
	}
	for _, o := range options {
		o(s)
	}
	return s
}

// Handler returns the HTTP handler implementing the HEC-style POST endpoint. It
// is exported (and exercised via httptest) independently of Run.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handle)
	// Wrapped here rather than in Run so httptest-driven callers get the same
	// bound as a real listener.
	d := s.processDeadline
	if d <= 0 {
		// A zero-value Server (constructed literally rather than via New) would
		// otherwise get a 0 deadline, which TimeoutHandler treats as "expire
		// immediately" and would 503 every request.
		d = handlerProcessDeadline
	}
	return withProcessDeadline(mux, d)
}

// httpServer builds the listener's http.Server. Split out of Run so the timeout
// set is constructed in exactly one place and can be asserted directly (#232) —
// the write window has to be derived from the process deadline, and a second
// hand-written copy of these values in a test would not catch them drifting.
func (s *Server) httpServer() *http.Server {
	d := s.processDeadline
	if d <= 0 {
		d = handlerProcessDeadline
	}
	return &http.Server{
		Addr:              s.listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		// Derived, never a bare constant: see writeTimeoutFor (#232).
		WriteTimeout: writeTimeoutFor(d),
		IdleTimeout:  idleTimeout,
	}
}

// withProcessDeadline bounds how long the receiver takes to ANSWER a request.
//
// Be precise about what this buys: http.TimeoutHandler cannot preempt a
// CPU-bound goroutine. When the deadline elapses it writes a 503 to the client
// and abandons the in-flight handler, which keeps running to completion. It
// therefore bounds the RESPONSE, not the work. The real bounds on the work are
// the unwrap depth cap (maxUnwrapDepth) and the record cap
// (maxRecordsPerRequest); this is defense in depth for anything they miss, not
// the fix for #228.
func withProcessDeadline(h http.Handler, d time.Duration) http.Handler {
	return http.TimeoutHandler(h, d, `{"text":"request processing deadline exceeded","code":503}`)
}

// acquire takes an admission slot (#209), returning a release func. It tries a
// non-blocking take first (the steady state), then waits up to admissionWait for
// a slot, giving up early if the client goes away. ok=false means the receiver
// is at capacity and the caller must refuse the request.
func (s *Server) acquire(ctx context.Context) (release func(), ok bool) {
	if s.admit == nil {
		return func() {}, true
	}
	select {
	case s.admit <- struct{}{}:
		return func() { <-s.admit }, true
	default:
	}
	timer := time.NewTimer(admissionWait)
	defer timer.Stop()
	select {
	case s.admit <- struct{}{}:
		return func() { <-s.admit }, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

// handle implements the receiver's request lifecycle: method/auth checks, body
// decompression, parsing, routing, and the Splunk-HEC ack response.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// Start a server span for this request. W3C trace-context is extracted from
	// headers; Tailscale's sender won't send a traceparent, so the span becomes a
	// root — that's correct. The span ends via defer regardless of exit path.
	tr := s.tracer
	if tr == nil {
		tr = noopStreamTracer
	}
	ctx := receiverPropagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := tr.Start(ctx, "stream.receive", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	r = r.WithContext(ctx)

	// In-flight counter: +1 now, -1 when the handler returns (balanced via defer).
	s.emitter.UpDownCounter(docStreamInflight.Name, docStreamInflight.Unit, docStreamInflight.Description, +1, nil)
	defer s.emitter.UpDownCounter(docStreamInflight.Name, docStreamInflight.Unit, docStreamInflight.Description, -1, nil)

	// Request duration histogram: record wall-clock time of the whole handler.
	start := time.Now()
	defer func() {
		s.emitter.Histogram(docStreamRequestDuration.Name, docStreamRequestDuration.Unit,
			docStreamRequestDuration.Description, time.Since(start).Seconds(),
			requestDurationBucketsSeconds, nil)
	}()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		span.SetStatus(codes.Error, "method not allowed")
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Fail closed BEFORE any body is read: with no token configured and a
	// network-reachable bind, this endpoint would accept unauthenticated flow and
	// audit records from anyone who can route to it. Refuse outright and name both
	// remedies, rather than ingesting attacker-supplied telemetry.
	if s.insecureOpen {
		span.SetStatus(codes.Error, "auth required")
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonAuthRequired})
		s.writeError(w, http.StatusForbidden,
			"streaming receiver refuses unauthenticated requests: set streaming.token, or bind streaming.listen to a loopback address")
		return
	}

	if !s.authorized(r) {
		span.SetStatus(codes.Error, "unauthorized")
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonAuth})
		s.writeError(w, http.StatusUnauthorized, "invalid or missing token")
		return
	}

	// Aggregate admission control (#209): the per-request byte cap bounds ONE
	// body, not the sum of every body in flight. Take a slot before buffering
	// anything so worst-case buffered memory is capped at
	// MaxConcurrentRequests * MaxBodyBytes regardless of how many senders arrive
	// at once. Released via defer on every exit path below.
	release, admitted := s.acquire(r.Context())
	if !admitted {
		span.SetStatus(codes.Error, "overloaded")
		s.logger.Warn("stream: refusing request, receiver at capacity", "max_concurrent_requests", cap(s.admit))
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonOverloaded})
		w.Header().Set("Retry-After", "1")
		s.writeError(w, http.StatusServiceUnavailable, "receiver at capacity, retry shortly")
		return
	}
	defer release()

	raw, err := s.readBody(r)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			span.SetStatus(codes.Error, "body too large")
			s.logger.Warn("stream: request body exceeds max size", "limit_bytes", s.maxBody)
			s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
				telemetry.Attrs{attrReason: reasonTooLarge})
			s.writeError(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		span.SetStatus(codes.Error, "could not read body")
		s.logger.Warn("stream: reading/decompressing body failed", "error", err)
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonUnparsable})
		s.writeError(w, http.StatusBadRequest, "could not read body")
		return
	}

	records, outcome, err := extractRecords(raw)
	if err != nil {
		// #229: too many record objects for one request. Surfaced like the byte cap
		// (413 + a dedicated reason) because it is the same class of failure — the
		// request is refused for its size, not for being malformed.
		if errors.Is(err, errTooManyRecords) {
			span.SetStatus(codes.Error, "too many records")
			s.logger.Warn("stream: request exceeds max record count", "limit_records", maxRecordsPerRequest)
			s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
				telemetry.Attrs{attrReason: reasonTooManyRecords})
			s.writeError(w, http.StatusRequestEntityTooLarge, "too many records in body")
			return
		}
		span.SetStatus(codes.Error, "could not parse body")
		s.logger.Warn("stream: parsing body failed", "error", err)
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonUnparsable})
		s.writeError(w, http.StatusBadRequest, "could not parse body")
		return
	}

	// #201 atomic batch delivery, phase 0: a structurally corrupt/truncated body
	// rejects the WHOLE request before any record is emitted, so a partial batch
	// is never ACKed as success and the sender retries. This replaces the earlier
	// valid-prefix salvage (#96) — the salvageable records are deliberately
	// discarded here rather than routed to a processor.
	if outcome.corrupt {
		span.SetStatus(codes.Error, "corrupt batch")
		s.logger.Warn("stream: rejecting structurally corrupt batch (no partial emit)",
			"dropped_bytes", outcome.droppedBytes)
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonMalformed})
		s.writeError(w, http.StatusBadRequest, "corrupt or truncated batch")
		return
	}

	// Phase 1: classify and typed-decode the COMPLETE batch WITHOUT routing any
	// record to a processor yet. Decoded records are staged; a known record that
	// fails typed decoding is tallied so the whole request can be rejected below
	// (nothing emitted). Genuinely-unknown record types are counted as skips and
	// do NOT fail the batch (forward compatibility).
	pendingFlows := make([]flowlog.FlowLog, 0, len(records))
	pendingAudits := make([]audit.Event, 0, len(records))
	var classifyUnknown, flowDecodeErr, auditDecodeErr int
	for _, rec := range records {
		switch classify(rec.raw) {
		case kindFlow:
			var f flowlog.FlowLog
			if err := json.Unmarshal(rec.raw, &f); err != nil {
				flowDecodeErr++
				continue
			}
			pendingFlows = append(pendingFlows, f)
		case kindAudit:
			var ev audit.Event
			if err := json.Unmarshal(rec.raw, &ev); err != nil {
				auditDecodeErr++
				continue
			}
			// A streamed configuration-audit record carries no inner eventTime —
			// its timestamp lives in the HEC envelope (see the package doc). Fall
			// back to the envelope time so the emitted log record bears the event's
			// real occurrence time instead of the OTEL ingest time. Only fill it in
			// when the record has no time of its own (polled records always do), so
			// the envelope time never overrides a real eventTime.
			if ev.EventTime.IsZero() && !rec.envTime.IsZero() {
				ev.EventTime = rec.envTime
			}
			pendingAudits = append(pendingAudits, ev)
		default:
			classifyUnknown++
		}
	}

	// #201: a KNOWN record (flow/audit) that failed typed decoding means the batch
	// is not fully understood — reject the WHOLE request with nothing emitted, so a
	// wire-format change surfaces as retries + an alert rather than silent record
	// loss behind a 200. decode_errors{type=...} records which wire type broke.
	if flowDecodeErr > 0 || auditDecodeErr > 0 {
		span.SetStatus(codes.Error, "record decode failure")
		if flowDecodeErr > 0 {
			s.emitter.Counter(docStreamDecodeErrors.Name, docStreamDecodeErrors.Unit, docStreamDecodeErrors.Description,
				float64(flowDecodeErr), telemetry.Attrs{attrType: typeFlow})
		}
		if auditDecodeErr > 0 {
			s.emitter.Counter(docStreamDecodeErrors.Name, docStreamDecodeErrors.Unit, docStreamDecodeErrors.Description,
				float64(auditDecodeErr), telemetry.Attrs{attrType: typeAudit})
		}
		s.logger.Warn("stream: rejecting batch with undecodable known record(s) (no partial emit)",
			"flow_decode_errors", flowDecodeErr, "audit_decode_errors", auditDecodeErr)
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonDecodeError})
		s.writeError(w, http.StatusBadRequest, "batch contains an undecodable record")
		return
	}

	// Phase 2: the batch is fully understood (every record either decoded cleanly
	// or is a forward-compatible unknown). NOW emit — route the staged records,
	// count skips, and ack success. Nothing above this point touched a processor.
	if s.onIngest != nil && len(raw) > 0 {
		s.onIngest(semconv.IngestSourceStream, "", 0, len(raw))
	}
	for i := range pendingFlows {
		s.flowProc.Process(pendingFlows[i], s.emitter)
	}
	for i := range pendingAudits {
		s.auditProc.Process(pendingAudits[i], s.emitter)
	}
	flows := len(pendingFlows)
	audits := len(pendingAudits)
	// #67: total skipped records, combining both stages a record can be
	// dropped at — classify()-stage kindUnknown (above) and unwrap-stage drops
	// (a non-object HEC "event"/nested value, tallied inside extractRecords).
	// The request still 200s; this is a per-RECORD count, not a request-level
	// rejection like MetricRejected.
	skipped := classifyUnknown + outcome.unwrapDropped

	if flows > 0 {
		s.emitter.Counter(docStreamRecords.Name, docStreamRecords.Unit, docStreamRecords.Description, float64(flows),
			telemetry.Attrs{attrType: typeFlow})
		if s.onIngest != nil {
			s.onIngest(semconv.IngestSourceStream, semconv.IngestSignalFlow, flows, 0)
		}
	}
	if audits > 0 {
		s.emitter.Counter(docStreamRecords.Name, docStreamRecords.Unit, docStreamRecords.Description, float64(audits),
			telemetry.Attrs{attrType: typeAudit})
		if s.onIngest != nil {
			s.onIngest(semconv.IngestSourceStream, semconv.IngestSignalAudit, audits, 0)
		}
	}
	if classifyUnknown > 0 {
		s.emitter.Counter(docStreamSkipped.Name, docStreamSkipped.Unit, docStreamSkipped.Description,
			float64(classifyUnknown), telemetry.Attrs{attrReason: reasonUnclassified})
	}
	if outcome.unwrapDropped > 0 {
		s.emitter.Counter(docStreamSkipped.Name, docStreamSkipped.Unit, docStreamSkipped.Description,
			float64(outcome.unwrapDropped), telemetry.Attrs{attrReason: reasonUnwrapDrop})
	}
	if skipped > 0 {
		s.logger.Debug("stream: skipped unrecognized records", "count", skipped,
			"unclassified", classifyUnknown, "unwrap_drop", outcome.unwrapDropped)
	}

	// Record aggregate counts and body size on the span before the success ack.
	if span.IsRecording() {
		span.SetAttributes(
			attribute.Int("tailscale.stream.flows", flows),
			attribute.Int("tailscale.stream.audits", audits),
			attribute.Int("tailscale.stream.skipped", skipped),
			attribute.Int("http.request.body.size", len(raw)), // bounded decompressed bytes
		)
	}

	s.writeAck(w)
}

// authorized reports whether the request carries the configured token. When no
// token is configured, all requests are authorized.
//
// Tailscale log streaming authenticates with HTTP Basic auth — base64(user:<token>),
// where the password is the configured token — NOT "Authorization: Splunk <token>"
// (verified via a live capture of the TailscaleLogStreamPublisher; S4-10). Accept
// Basic auth first, then the Splunk-HEC scheme other HEC senders use. The token
// comparison is constant-time.
func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	if _, pass, ok := r.BasicAuth(); ok {
		return subtle.ConstantTimeCompare([]byte(pass), []byte(s.token)) == 1
	}
	if fields := strings.Fields(r.Header.Get("Authorization")); len(fields) == 2 && strings.EqualFold(fields[0], authScheme) {
		return subtle.ConstantTimeCompare([]byte(fields[1]), []byte(s.token)) == 1
	}
	return false
}

// readBody reads and (per Decompress / Content-Encoding) decompresses the
// request body.
func (s *Server) readBody(r *http.Request) ([]byte, error) {
	mode := s.decompress
	if mode == "auto" {
		switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
		case "gzip":
			mode = "gzip"
		case "zstd":
			mode = "zstd"
		default:
			mode = "none"
		}
	}

	switch mode {
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return readAllLimited(zr, s.maxBody)
	case "zstd":
		var dopts []zstd.DOption
		if w := zstdMaxWindow(s.maxBody); w > 0 {
			dopts = append(dopts, zstd.WithDecoderMaxWindow(w))
		}
		zr, err := zstd.NewReader(r.Body, dopts...)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return readAllLimited(zr, s.maxBody)
	default:
		return readAllLimited(r.Body, s.maxBody)
	}
}

// readAllLimited reads all of r but fails with errBodyTooLarge when more than
// limit bytes are available, bounding memory against a huge or zip-bomb body
// (the limit is checked on the DECOMPRESSED stream). A negative limit reads
// without a cap.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return io.ReadAll(r)
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errBodyTooLarge
	}
	return b, nil
}

// zstdMaxWindow returns the cap for the zstd decoder's back-reference window,
// derived from the decompressed-body limit so a crafted frame cannot declare a
// window larger than the most we would ever decompress (the library default cap
// is 512 MB, allowing a large allocation even when the output stays under the
// body cap). 0 means "leave the library default" — used when the body is
// uncapped (maxBody <= 0). The result is clamped up to zstd's 1 KB minimum.
func zstdMaxWindow(maxBody int64) uint64 {
	if maxBody <= 0 {
		return 0
	}
	if maxBody < 1<<10 {
		return 1 << 10
	}
	return uint64(maxBody)
}

// recordKind is the classification of an extracted record object.
type recordKind int

const (
	kindUnknown recordKind = iota
	kindFlow
	kindAudit
)

// shape is the minimal set of fields probed to classify a record without fully
// decoding it.
type shape struct {
	NodeID          string          `json:"nodeId"`
	VirtualTraffic  json.RawMessage `json:"virtualTraffic"`
	SubnetTraffic   json.RawMessage `json:"subnetTraffic"`
	ExitTraffic     json.RawMessage `json:"exitTraffic"`
	PhysicalTraffic json.RawMessage `json:"physicalTraffic"`

	Actor  json.RawMessage `json:"actor"`
	Action string          `json:"action"`
}

// classify inspects a record's shape and returns its kind. A record is a flow
// if it has a non-empty nodeId and at least one traffic field; otherwise it is
// an audit event if it has both an actor and an action.
func classify(rec json.RawMessage) recordKind {
	var sh shape
	if err := json.Unmarshal(rec, &sh); err != nil {
		return kindUnknown
	}
	hasTraffic := len(sh.VirtualTraffic) > 0 || len(sh.SubnetTraffic) > 0 ||
		len(sh.ExitTraffic) > 0 || len(sh.PhysicalTraffic) > 0
	if sh.NodeID != "" && hasTraffic {
		return kindFlow
	}
	if len(sh.Actor) > 0 && sh.Action != "" {
		return kindAudit
	}
	return kindUnknown
}

// extractedRecord pairs a raw record object with the timestamp carried by its
// enclosing HEC envelope ("time"/"fields.recorded", siblings of "event"), or the
// zero time when the record had no such envelope. The envelope time is used only
// as a FALLBACK for streamed audit records that lack an inner eventTime (S4-10):
// flow records ignore it (they carry their own start/end).
type extractedRecord struct {
	raw     json.RawMessage
	envTime time.Time
}

// extractOutcome reports structural facts about a parsed request body that the
// caller (handle) needs for the #201 atomic batch-delivery decision.
type extractOutcome struct {
	// corrupt is true when the body contained a JSON structural error somewhere:
	// a mid-stream syntax error during concatenated-value decoding (after at
	// least one clean value), or a non-empty NDJSON line that is not valid JSON.
	// It rejects the WHOLE request (rejected{reason=malformed}) — the receiver no
	// longer salvages a valid prefix and ACKs the partial batch (this reverses
	// #96). Any records still returned alongside it are for logging only and must
	// NOT be emitted.
	corrupt bool
	// droppedBytes is the length, in bytes, of the undecoded remainder discarded
	// at a concatenated-stream corruption point (0 for the NDJSON-line case). The
	// exact record count inside that remainder is unknowable — those bytes never
	// parsed as JSON — so byte length is the honest signal logged instead.
	droppedBytes int
	// unwrapDropped counts values dropped inside unwrap itself (#67): a
	// non-object value (e.g. a scalar/null HEC "event", or a malformed nested
	// array) encountered while unwrapping an envelope, before the record ever
	// reaches classify(). This is a forward-compatible SKIP (the batch still
	// succeeds), distinct from the corrupt flag above.
	unwrapDropped int
}

// extractRecords parses a request body into zero or more record objects (each
// paired with its envelope time), tolerating the several envelope shapes
// documented on the package. It sets outcome.corrupt when the body contains a
// structural JSON error, and returns an error only when nothing JSON-like can be
// extracted at all.
func extractRecords(raw []byte) ([]extractedRecord, extractOutcome, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, extractOutcome{}, errors.New("empty body")
	}

	// First try: the body is a stream of one-or-more JSON values (covers a
	// single object as well as concatenated/NDJSON values, since
	// json.Decoder reads successive values regardless of separating
	// whitespace/newlines).
	var values []json.RawMessage
	var outcome extractOutcome
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	for {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A mid-stream syntax error after at least one clean value: the body
			// is structurally corrupt/truncated. Under the #201 atomic contract
			// the whole request is rejected (no partial emit), so flag it and stop.
			// The values decoded before the error are kept only so the caller can
			// report the dropped-bytes count — they are NOT emitted. An EMPTY
			// prefix (nothing decoded before the very first error) means the body
			// doesn't even start with valid JSON; fall through to the line scan
			// below to tell "entirely non-JSON" apart from "mixes valid and corrupt
			// NDJSON lines".
			if len(values) > 0 {
				outcome.corrupt = true
				outcome.droppedBytes = len(trimmed) - int(dec.InputOffset())
			}
			break
		}
		// #229: checked BEFORE the append, so `values` never grows past the cap —
		// the slice itself is the amplification this bounds.
		if len(values) >= maxRecordsPerRequest {
			return nil, extractOutcome{}, errTooManyRecords
		}
		values = append(values, v)
	}

	if len(values) == 0 {
		// The stream decoder could not read even one value: the body does not
		// start with valid JSON. Scan it line by line so we can distinguish a body
		// that is entirely non-JSON (errors -> rejected{reason=unparsable}) from an
		// NDJSON body that mixes valid lines with a corrupt one (corrupt ->
		// rejected{reason=malformed}). strings.SplitSeq (Go 1.24+) iterates lines
		// lazily without allocating an intermediate slice.
		for line := range strings.SplitSeq(string(trimmed), "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if json.Valid([]byte(line)) {
				// #229 again: the line-scan path grows the same slice, so it needs
				// the same pre-append cap. Without it the cap is bypassed by simply
				// prefixing the body with a non-JSON line.
				if len(values) >= maxRecordsPerRequest {
					return nil, extractOutcome{}, errTooManyRecords
				}
				values = append(values, json.RawMessage(line))
			} else {
				// A structurally-invalid line alongside valid ones: the batch is
				// corrupt. (When there are NO valid lines at all we return an error
				// below, which the caller maps to reason=unparsable instead.)
				outcome.corrupt = true
			}
		}
		if len(values) == 0 {
			return nil, extractOutcome{}, errors.New("no JSON values in body")
		}
	}

	// Unwrap each top-level value into record objects, propagating each HEC
	// envelope's time down to the record(s) it carries. st.dropped tallies values
	// dropped along the way (#67) — a non-object value, or one nested past
	// maxUnwrapDepth, encountered while unwrapping before classify() ever runs.
	// st.remaining is the #229 record budget: one wrapper can carry far more
	// records than there are top-level values (a single {"logs":[...]} batch), so
	// the budget is threaded INTO the walk rather than checked after it.
	var out []extractedRecord
	st := unwrapState{remaining: maxRecordsPerRequest}
	for _, v := range values {
		out = append(out, unwrap(v, time.Time{}, 0, &st)...)
		if st.overflow {
			return nil, extractOutcome{}, errTooManyRecords
		}
	}
	if len(out) == 0 {
		return nil, extractOutcome{}, errors.New("no records after unwrapping")
	}
	outcome.unwrapDropped = st.dropped
	return out, outcome, nil
}

// unwrapState carries the mutable counters threaded through the recursive
// envelope walk: the #67 drop tally and the #229 record budget.
type unwrapState struct {
	// dropped counts values the walk discarded (a non-object value, or nesting
	// past maxUnwrapDepth). Surfaced as skipped{reason=unwrap_drop}.
	dropped int
	// remaining is how many more records may be produced before
	// maxRecordsPerRequest is exceeded.
	remaining int
	// overflow is set once the budget is exhausted; extractRecords turns it into
	// errTooManyRecords.
	overflow bool
}

// take reserves budget for one record, reporting whether it was available.
func (st *unwrapState) take() bool {
	if st.remaining <= 0 {
		st.overflow = true
		return false
	}
	st.remaining--
	return true
}

// envelope captures the optional HEC ("event") and Tailscale ("logs") wrappers
// plus the HEC timestamp fields ("time" and "fields.recorded") that sit beside
// "event". Time and Fields are kept raw so a malformed or unexpected shape can
// never fail the whole envelope decode — keeping the parser defensive.
type envelope struct {
	Time   json.RawMessage   `json:"time"`
	Event  json.RawMessage   `json:"event"`
	Fields json.RawMessage   `json:"fields"`
	Logs   []json.RawMessage `json:"logs"`
}

// unwrap turns a single top-level JSON value into the record object(s) it
// carries, tagging each with the enclosing HEC envelope time (inherited down the
// recursion): a Splunk-HEC {"event": <record>} wrapper yields its event tagged
// with the wrapper's own "time"/"fields.recorded"; a Tailscale {"logs": [...]}
// wrapper yields its elements; any other object is itself a record.
//
// st.dropped is incremented once per value discarded along the way: a non-object
// value (a scalar/null HEC "event", a bare scalar at top level, or a malformed
// nested array), or a value nested deeper than maxUnwrapDepth. Both are the #67
// unwrap-stage drop count, distinct from a classify()-stage kindUnknown result in
// the caller. st.remaining is the #229 record budget.
//
// depth is the current nesting level, 0 at the top. Past maxUnwrapDepth the value
// is dropped (#228): the documented envelope shapes nest at most a couple of
// levels, and unbounded recursion here means re-scanning most of the body once per
// level, which turns a small compressed body into minutes of CPU. The drop is
// deliberately forward-compatible — over-deep nesting is an unrecognized shape,
// not evidence the batch is corrupt, so it must not fail the whole request.
func unwrap(v json.RawMessage, inherited time.Time, depth int, st *unwrapState) []extractedRecord {
	if depth > maxUnwrapDepth {
		st.dropped++
		return nil
	}
	trimmed := bytes.TrimSpace(v)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Not a JSON object (e.g. an array or scalar at top level); ignore.
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// A bare JSON array of records.
			var arr []json.RawMessage
			if err := decodeOnce(trimmed, &arr); err == nil {
				var out []extractedRecord
				for _, e := range arr {
					out = append(out, unwrap(e, inherited, depth+1, st)...)
					if st.overflow {
						return out
					}
				}
				return out
			}
		}
		st.dropped++
		return nil
	}

	var env envelope
	if err := decodeOnce(trimmed, &env); err == nil {
		// A HEC/batch wrapper's "time"/"fields.recorded" sibling is the event
		// time; prefer it over an inherited time when propagating down to the
		// record(s) it carries. Applied to BOTH the {"logs":[...]} and {"event":...}
		// wrappers so the two shapes behave consistently.
		t := env.envelopeTime()
		if t.IsZero() {
			t = inherited
		}
		if len(env.Logs) > 0 {
			out := make([]extractedRecord, 0, len(env.Logs))
			for _, e := range env.Logs {
				out = append(out, unwrap(e, t, depth+1, st)...)
				if st.overflow {
					return out
				}
			}
			return out
		}
		if len(bytes.TrimSpace(env.Event)) > 0 {
			return unwrap(env.Event, t, depth+1, st)
		}
	}
	// Plain record object.
	if !st.take() {
		return nil
	}
	return []extractedRecord{{raw: trimmed, envTime: inherited}}
}

// decodeOnce decodes the single JSON value in data into out.
//
// It reads through a json.Decoder rather than calling json.Unmarshal so each
// level of the envelope walk consumes exactly the one value it is unwrapping and
// stops there, instead of requiring (and therefore scanning) the whole slice as a
// single value. Every `data` this walk sees is already exactly one value, so on
// today's inputs the two are equivalent in work done — measured, the decoder form
// is if anything marginally slower, because it copies through an internal buffer.
//
// Be clear about what actually fixes #228, then: it is maxUnwrapDepth. Bounding
// the recursion is what turns O(depth x bodySize) into a small constant number of
// passes. This form is the shape that stays correct if the walk ever has to read
// a value out of a larger buffer; it is not itself the performance fix, and it
// should not be described as one.
func decodeOnce(data []byte, out any) error {
	return json.NewDecoder(bytes.NewReader(data)).Decode(out)
}

// envelopeTime extracts the event time from a HEC envelope, preferring the
// numeric "time" (unix epoch seconds — the event's own occurrence time) and
// falling back to "fields.recorded" (RFC3339 — the publisher's record/ship time).
// It returns the zero time when neither is present or parseable.
func (e envelope) envelopeTime() time.Time {
	if t := parseHECTime(e.Time); !t.IsZero() {
		return t
	}
	if len(bytes.TrimSpace(e.Fields)) > 0 {
		var fields struct {
			Recorded string `json:"recorded"`
		}
		if err := json.Unmarshal(e.Fields, &fields); err == nil && fields.Recorded != "" {
			if t, err := time.Parse(time.RFC3339Nano, fields.Recorded); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Time{}
}

// maxHECTimeSeconds is the largest epoch-seconds value that still converts to a
// time representable as int64 nanoseconds — the width of an OTLP timestamp. It
// lands in 2262; anything beyond it would wrap rather than fail.
const maxHECTimeSeconds = float64(math.MaxInt64) / 1e9

// parseHECTime parses a Splunk-HEC "time" value (unix epoch SECONDS, normally a
// JSON number but tolerated as a quoted string) into a UTC time.Time. It returns
// the zero time for an absent, null, non-positive, out-of-range, or unparseable
// value — the receiver body is attacker-reachable (the token is optional), and the
// parsed value becomes an exported log record's timestamp, so a value that cannot
// be represented is rejected rather than silently wrapped into a nonsense year.
func parseHECTime(raw json.RawMessage) time.Time {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return time.Time{}
	}
	s := string(trimmed)
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return time.Time{}
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > maxHECTimeSeconds {
		return time.Time{}
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// writeAck writes the Splunk-HEC success acknowledgement.
func (s *Server) writeAck(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"text":"Success","code":0}`)
}

// writeError writes a Splunk-HEC-style error body with the given status.
func (s *Server) writeError(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := struct {
		Text string `json:"text"`
		Code int    `json:"code"`
	}{Text: text, Code: status}
	_ = json.NewEncoder(w).Encode(body)
}

// Run binds Options.Listen and serves the handler until ctx is canceled, then
// performs a graceful shutdown. It serves HTTPS when both TLS files are set.
func (s *Server) Run(ctx context.Context) error {
	if s.insecureOpen {
		// Logged once, loudly, at startup: the handler refuses every request in
		// this state, and an operator staring at 403s deserves to find the reason
		// in the logs rather than in the source.
		s.logger.Error("stream: receiver is network-reachable with no streaming.token configured; ALL requests will be refused with 403. Set streaming.token, or bind streaming.listen to a loopback address.",
			"listen", s.listen)
	}
	srv := s.httpServer()

	errCh := make(chan error, 1)
	go func() {
		if s.tlsCert != "" && s.tlsKey != "" {
			errCh <- srv.ListenAndServeTLS(s.tlsCert, s.tlsKey)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
