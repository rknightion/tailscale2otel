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
// A genuinely corrupt/truncated body (e.g. a torn transfer) is handled by
// SALVAGING the valid prefix rather than rejecting the whole batch (#96): when
// the concatenated-JSON decode hits a mid-stream syntax error after already
// decoding at least one clean value, extractRecords keeps that decoded prefix
// and drops only the corrupted remainder — since the real wire format has no
// separators between records, one bad object no longer costs an entire
// ~73-record POST. The salvaged/dropped counts are logged (WARN) so the
// partial loss stays observable. The newline-split fallback above is only
// attempted when NOTHING decoded at all.
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
	// MetricRejected counts requests/records that could not be ingested. It
	// carries a low-cardinality "reason" attribute ("auth" or "unparsable").
	MetricRejected = "tailscale.stream.rejected"
	// MetricDecodeErrors counts records that classified as a known type but whose
	// typed decode failed (a malformed flow/audit record inside an otherwise
	// valid request). Carries the "type" attribute ("flow" or "audit").
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

	// reasonUnclassified and reasonUnwrapDrop are the "reason" values for
	// MetricSkipped (#67): the two ways a record extracted from a valid
	// request body can fail to reach a processor.
	reasonUnclassified = "unclassified"
	reasonUnwrapDrop   = "unwrap_drop"
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
	s := &Server{
		path:       path,
		token:      opts.Token,
		decompress: decompress,
		tlsCert:    opts.TLSCertFile,
		tlsKey:     opts.TLSKeyFile,
		listen:     opts.Listen,
		maxBody:    maxBody,
		flowProc:   flowProc,
		auditProc:  auditProc,
		emitter:    e,
		logger:     logger,
		onIngest:   opts.OnIngest,
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
	return mux
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

	if !s.authorized(r) {
		span.SetStatus(codes.Error, "unauthorized")
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonAuth})
		s.writeError(w, http.StatusUnauthorized, "invalid or missing token")
		return
	}

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
		span.SetStatus(codes.Error, "could not parse body")
		s.logger.Warn("stream: parsing body failed", "error", err)
		s.emitter.Counter(docStreamRejected.Name, docStreamRejected.Unit, docStreamRejected.Description, 1,
			telemetry.Attrs{attrReason: reasonUnparsable})
		s.writeError(w, http.StatusBadRequest, "could not parse body")
		return
	}
	if outcome.truncated {
		// #96: a mid-stream decode error salvaged a valid prefix rather than
		// discarding the whole batch; surface the salvaged/dropped counts so
		// the partial loss stays visible instead of hiding behind a plain 200.
		s.logger.Warn("stream: salvaged valid prefix from a corrupted batch",
			"salvaged_records", outcome.salvaged,
			"dropped_bytes", outcome.droppedBytes)
	}

	if s.onIngest != nil && len(raw) > 0 {
		s.onIngest(semconv.IngestSourceStream, "", 0, len(raw))
	}

	var flows, audits, classifyUnknown, flowDecodeErr, auditDecodeErr int
	for _, rec := range records {
		switch classify(rec.raw) {
		case kindFlow:
			var f flowlog.FlowLog
			if err := json.Unmarshal(rec.raw, &f); err != nil {
				flowDecodeErr++
				continue
			}
			s.flowProc.Process(f, s.emitter)
			flows++
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
			s.auditProc.Process(ev, s.emitter)
			audits++
		default:
			classifyUnknown++
		}
	}
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
	if flowDecodeErr > 0 {
		s.emitter.Counter(docStreamDecodeErrors.Name, docStreamDecodeErrors.Unit, docStreamDecodeErrors.Description,
			float64(flowDecodeErr), telemetry.Attrs{attrType: typeFlow})
	}
	if auditDecodeErr > 0 {
		s.emitter.Counter(docStreamDecodeErrors.Name, docStreamDecodeErrors.Unit, docStreamDecodeErrors.Description,
			float64(auditDecodeErr), telemetry.Attrs{attrType: typeAudit})
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

// extractOutcome reports whether extractRecords had to salvage a valid prefix
// after a mid-stream decode error (#96), and how much was salvaged vs. dropped,
// so the caller can surface the partial loss instead of it staying invisible
// behind a plain 200 ack.
type extractOutcome struct {
	// truncated is true when concatenated-JSON decoding stopped on a syntax
	// error after at least one value had already decoded cleanly. The
	// corrupted value and everything after it in the body was dropped.
	truncated bool
	// salvaged is the number of records recovered from the decoded prefix
	// (after envelope unwrapping); meaningful only when truncated is true.
	salvaged int
	// droppedBytes is the length, in bytes, of the undecoded remainder
	// discarded at the corruption point. The exact record count inside that
	// remainder is unknowable — those bytes never parsed as JSON — so byte
	// length is the honest signal surfaced instead.
	droppedBytes int
	// unwrapDropped counts values dropped inside unwrap itself (#67): a
	// non-object value (e.g. a scalar/null HEC "event", or a malformed nested
	// array) encountered while unwrapping an envelope, before the record ever
	// reaches classify(). Distinct from a classify()-stage kindUnknown result,
	// which counts as skipped in the caller (handle) instead.
	unwrapDropped int
}

// extractRecords parses a request body into zero or more record objects (each
// paired with its envelope time), tolerating the several envelope shapes
// documented on the package. It returns an error only when nothing JSON-like can
// be extracted at all.
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
			// A mid-stream syntax error. The real Tailscale wire format
			// concatenates HEC objects with NO separators (see the package
			// doc), so one corrupt record used to reject an entire ~73-record
			// batch: discarding the already-decoded values here and falling
			// through to the newline-split fallback below salvages nothing
			// from a no-separator stream. Keep the valid PREFIX instead (#96)
			// — only an empty prefix (nothing decoded before the very first
			// error) falls through to that fallback.
			if len(values) > 0 {
				outcome.truncated = true
				outcome.droppedBytes = len(trimmed) - int(dec.InputOffset())
			}
			break
		}
		values = append(values, v)
	}

	if len(values) == 0 {
		// Fallback: split on newlines and parse each non-empty line. This
		// salvages NDJSON where one line is malformed without discarding the
		// rest. strings.SplitSeq (Go 1.24+) iterates the lines lazily without
		// allocating an intermediate slice.
		for line := range strings.SplitSeq(string(trimmed), "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if json.Valid([]byte(line)) {
				values = append(values, json.RawMessage(line))
			}
		}
		if len(values) == 0 {
			return nil, extractOutcome{}, errors.New("no JSON values in body")
		}
	}

	// Unwrap each top-level value into record objects, propagating each HEC
	// envelope's time down to the record(s) it carries. unwrapDropped tallies
	// values dropped along the way (#67) — a non-object value encountered while
	// unwrapping, before classify() ever runs.
	var out []extractedRecord
	var unwrapDropped int
	for _, v := range values {
		out = append(out, unwrap(v, time.Time{}, &unwrapDropped)...)
	}
	if len(out) == 0 {
		return nil, extractOutcome{}, errors.New("no records after unwrapping")
	}
	if outcome.truncated {
		outcome.salvaged = len(out)
	}
	outcome.unwrapDropped = unwrapDropped
	return out, outcome, nil
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
// dropped is incremented once per non-object value encountered along the way
// (a scalar/null HEC "event", a bare scalar at top level, or a malformed
// nested array) — the #67 unwrap-stage drop count, distinct from a
// classify()-stage kindUnknown result in the caller.
func unwrap(v json.RawMessage, inherited time.Time, dropped *int) []extractedRecord {
	trimmed := bytes.TrimSpace(v)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Not a JSON object (e.g. an array or scalar at top level); ignore.
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// A bare JSON array of records.
			var arr []json.RawMessage
			if err := json.Unmarshal(trimmed, &arr); err == nil {
				var out []extractedRecord
				for _, e := range arr {
					out = append(out, unwrap(e, inherited, dropped)...)
				}
				return out
			}
		}
		*dropped++
		return nil
	}

	var env envelope
	if err := json.Unmarshal(trimmed, &env); err == nil {
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
				out = append(out, unwrap(e, t, dropped)...)
			}
			return out
		}
		if len(bytes.TrimSpace(env.Event)) > 0 {
			return unwrap(env.Event, t, dropped)
		}
	}
	// Plain record object.
	return []extractedRecord{{raw: trimmed, envTime: inherited}}
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
	srv := &http.Server{
		Addr:              s.listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

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
