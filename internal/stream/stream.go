// Package stream implements a streaming receiver that emulates a Splunk HTTP
// Event Collector (HEC) endpoint so Tailscale "log streaming" can push
// network-flow and configuration-audit logs to this collector. Received records
// are converted through the SAME shared processors (internal/flowlog and
// internal/audit) used by the polling collectors, so streamed and polled data
// produce identical OTEL metrics and log records.
//
// # Envelope is PROVISIONAL (no live capture)
//
// Tailscale does NOT publicly document the exact JSON envelope it POSTs to a
// Splunk-HEC sink, and the envelope handled here therefore remains PROVISIONAL
// pending a live capture. Truly pinning it empirically would require pointing a
// Tailscale log-streaming configuration at this endpoint and dumping the raw
// request bodies — i.e. reconfiguring the production lab tailnet's streaming.
// That is intentionally OUT OF SCOPE: live capture and any "auto_configure"
// helper that would mutate the tailnet's logstream settings are deliberately
// DEFERRED so this package never alters the production tailnet.
//
// What we CAN pin is the per-record shape, because streamed records match the
// poll API's network-flow and configuration-audit records exactly (verified
// against the real captures in .capture/logging_network.json and
// .capture/logging_config.json). Accordingly: flow records carry a NUMERIC
// "proto" (e.g. "proto":6 for TCP), and audit "old"/"new" values are
// polymorphic (string, object, array, or null). The parser is hardened and
// fixture-tested against those real shapes.
//
// Because the wrapping envelope is not pinned, the parser stays DEFENSIVE and
// accepts the union of shapes it is plausible to receive:
//
//   - a single JSON object;
//   - newline-delimited JSON (NDJSON), the HEC norm — one JSON object per line;
//   - a Splunk-HEC wrapper {"event": <record>, ...} — the "event" field is
//     unwrapped and classified;
//   - a Tailscale batch wrapper {"logs": [<record>, ...]} — each element is
//     classified (this is also the shape the .capture files use at top level).
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
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Exported metric names emitted by the receiver.
const (
	// MetricRecords counts records successfully routed to a processor. It
	// carries a low-cardinality "type" attribute ("flow" or "audit").
	MetricRecords = "tailscale.stream.records"
	// MetricRejected counts requests/records that could not be ingested. It
	// carries a low-cardinality "reason" attribute ("auth" or "unparsable").
	MetricRejected = "tailscale.stream.rejected"
)

// Attribute keys and values for the receiver's own counters.
const (
	attrType   = "type"
	attrReason = "reason"

	typeFlow  = "flow"
	typeAudit = "audit"

	reasonAuth       = "auth"
	reasonUnparsable = "unparsable"
)

// defaultPath is the Splunk-HEC event endpoint path used when Options.Path is
// empty.
const defaultPath = "/services/collector/event"

// authScheme is the Splunk-HEC Authorization scheme: "Authorization: Splunk
// <token>".
const authScheme = "Splunk"

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

	flowProc  *flowlog.Processor
	auditProc *audit.Processor
	emitter   telemetry.Emitter
	logger    *slog.Logger
}

// New returns a Server that converts received records via flowProc and
// auditProc and records to e. A nil logger is replaced with a discarding one.
func New(opts Options, flowProc *flowlog.Processor, auditProc *audit.Processor, e telemetry.Emitter, logger *slog.Logger) *Server {
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
	return &Server{
		path:       path,
		token:      opts.Token,
		decompress: decompress,
		tlsCert:    opts.TLSCertFile,
		tlsKey:     opts.TLSKeyFile,
		listen:     opts.Listen,
		flowProc:   flowProc,
		auditProc:  auditProc,
		emitter:    e,
		logger:     logger,
	}
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
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if !s.authorized(r) {
		s.emitter.Counter(MetricRejected, "{rejection}", "Tailscale stream records rejected", 1,
			telemetry.Attrs{attrReason: reasonAuth})
		s.writeError(w, http.StatusUnauthorized, "invalid or missing token")
		return
	}

	raw, err := s.readBody(r)
	if err != nil {
		s.logger.Warn("stream: reading/decompressing body failed", "error", err)
		s.emitter.Counter(MetricRejected, "{rejection}", "Tailscale stream records rejected", 1,
			telemetry.Attrs{attrReason: reasonUnparsable})
		s.writeError(w, http.StatusBadRequest, "could not read body")
		return
	}

	records, err := extractRecords(raw)
	if err != nil {
		s.logger.Warn("stream: parsing body failed", "error", err)
		s.emitter.Counter(MetricRejected, "{rejection}", "Tailscale stream records rejected", 1,
			telemetry.Attrs{attrReason: reasonUnparsable})
		s.writeError(w, http.StatusBadRequest, "could not parse body")
		return
	}

	var flows, audits, skipped int
	for _, rec := range records {
		switch classify(rec) {
		case kindFlow:
			var f flowlog.FlowLog
			if err := json.Unmarshal(rec, &f); err != nil {
				skipped++
				continue
			}
			s.flowProc.Process(f, s.emitter)
			flows++
		case kindAudit:
			var ev audit.Event
			if err := json.Unmarshal(rec, &ev); err != nil {
				skipped++
				continue
			}
			s.auditProc.Process(ev, s.emitter)
			audits++
		default:
			skipped++
		}
	}

	if flows > 0 {
		s.emitter.Counter(MetricRecords, "{record}", "Tailscale stream records processed", float64(flows),
			telemetry.Attrs{attrType: typeFlow})
	}
	if audits > 0 {
		s.emitter.Counter(MetricRecords, "{record}", "Tailscale stream records processed", float64(audits),
			telemetry.Attrs{attrType: typeAudit})
	}
	if skipped > 0 {
		s.logger.Debug("stream: skipped unrecognized records", "count", skipped)
	}

	s.writeAck(w)
}

// authorized reports whether the request carries the configured token. When no
// token is configured, all requests are authorized.
func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	// Accept "Splunk <token>" (the HEC scheme), case-insensitive on the scheme.
	if fields := strings.Fields(h); len(fields) == 2 && strings.EqualFold(fields[0], authScheme) {
		return fields[1] == s.token
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
		return io.ReadAll(zr)
	case "zstd":
		zr, err := zstd.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	default:
		return io.ReadAll(r.Body)
	}
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

// extractRecords parses a request body into zero or more record objects,
// tolerating the several envelope shapes documented on the package. It returns
// an error only when nothing JSON-like can be extracted at all.
func extractRecords(raw []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty body")
	}

	// First try: the body is a stream of one-or-more JSON values (covers a
	// single object as well as concatenated/NDJSON values, since
	// json.Decoder reads successive values regardless of separating
	// whitespace/newlines).
	var values []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	for {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Not a clean JSON stream; fall back to line-by-line below.
			values = nil
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
			return nil, errors.New("no JSON values in body")
		}
	}

	// Unwrap each top-level value into record objects.
	var out []json.RawMessage
	for _, v := range values {
		out = append(out, unwrap(v)...)
	}
	if len(out) == 0 {
		return nil, errors.New("no records after unwrapping")
	}
	return out, nil
}

// envelope captures the optional HEC ("event") and Tailscale ("logs") wrappers.
type envelope struct {
	Event json.RawMessage   `json:"event"`
	Logs  []json.RawMessage `json:"logs"`
}

// unwrap turns a single top-level JSON value into the record object(s) it
// carries: a Splunk-HEC {"event": <record>} wrapper yields its event; a
// Tailscale {"logs": [...]} wrapper yields its elements; any other object is
// itself a record.
func unwrap(v json.RawMessage) []json.RawMessage {
	trimmed := bytes.TrimSpace(v)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Not a JSON object (e.g. an array or scalar at top level); ignore.
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// A bare JSON array of records.
			var arr []json.RawMessage
			if err := json.Unmarshal(trimmed, &arr); err == nil {
				var out []json.RawMessage
				for _, e := range arr {
					out = append(out, unwrap(e)...)
				}
				return out
			}
		}
		return nil
	}

	var env envelope
	if err := json.Unmarshal(trimmed, &env); err == nil {
		if len(env.Logs) > 0 {
			out := make([]json.RawMessage, 0, len(env.Logs))
			for _, e := range env.Logs {
				out = append(out, unwrap(e)...)
			}
			return out
		}
		if len(bytes.TrimSpace(env.Event)) > 0 {
			return unwrap(env.Event)
		}
	}
	// Plain record object.
	return []json.RawMessage{trimmed}
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

// Run binds Options.Listen and serves the handler until ctx is cancelled, then
// performs a graceful shutdown. It serves HTTPS when both TLS files are set.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
