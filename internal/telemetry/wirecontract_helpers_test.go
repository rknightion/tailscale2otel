package telemetry_test

// Shared plumbing for the OTLP wire-contract integration suite
// (wirecontract_http_test.go, wirecontract_grpc_test.go): in-process HTTP and
// gRPC OTLP receivers, throwaway TLS material, and assertion helpers that
// operate on DECODED wire content so the same checks run against both
// transports (#357). See internal/telemetry/CLAUDE.md for why the Resource
// split and const-attribute placement matter.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collectorlogpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/gzip" // registers the default "gzip" grpc compressor we wrap below
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// ---------------------------------------------------------------------------
// Recorder: shared, race-safe request capture with deterministic waiting.
// ---------------------------------------------------------------------------

// capturedRequest is one decoded OTLP export, regardless of transport.
type capturedRequest struct {
	signal string // "metrics" | "logs" | "traces"
	path   string // HTTP request path; empty for gRPC (the method IS the path)
	header http.Header
	gzip   bool // arrived compressed (Content-Encoding: gzip / grpc-encoding: gzip)

	metrics *collectormetricpb.ExportMetricsServiceRequest
	logs    *collectorlogpb.ExportLogsServiceRequest
	traces  *collectortracepb.ExportTraceServiceRequest
}

// wireRecorder captures requests under a mutex and additionally publishes each
// one on a buffered channel so tests can block deterministically instead of
// polling.
type wireRecorder struct {
	mu   sync.Mutex
	reqs []capturedRequest
	ch   chan capturedRequest
}

func newWireRecorder() *wireRecorder {
	return &wireRecorder{ch: make(chan capturedRequest, 64)}
}

func (r *wireRecorder) record(cr capturedRequest) {
	r.mu.Lock()
	r.reqs = append(r.reqs, cr)
	r.mu.Unlock()
	select {
	case r.ch <- cr:
	default:
		// Channel full: every test in this suite reads at least one request per
		// export, so this only happens if a test genuinely doesn't care — the
		// mutex-guarded slice below still has it.
	}
}

func (r *wireRecorder) all() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// ---------------------------------------------------------------------------
// Throwaway TLS material: a CA, a server leaf (SAN 127.0.0.1 + localhost) and
// a client leaf for mTLS. Modeled on internal/config/tlskeypair_test.go's
// writeKeypair, but this is its own implementation per the brief (no import
// across packages) and additionally signs against a real CA with IP SANs so
// httptest/grpc servers on 127.0.0.1 verify.
// ---------------------------------------------------------------------------

type wireTLSMaterial struct {
	caFile         string
	serverCertFile string
	serverKeyFile  string
	clientCertFile string
	clientKeyFile  string

	serverCert tls.Certificate
	caPool     *x509.CertPool
}

func newWireTLSMaterial(t *testing.T) wireTLSMaterial {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wirecontract-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTmpl, &caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caFile := filepath.Join(dir, "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", caDER)

	serverCertFile, serverKeyFile, serverTLSCert := issueLeaf(t, dir, "server", caCert, caKey, x509.ExtKeyUsageServerAuth, true)
	clientCertFile, clientKeyFile, _ := issueLeaf(t, dir, "client", caCert, caKey, x509.ExtKeyUsageClientAuth, false)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	return wireTLSMaterial{
		caFile:         caFile,
		serverCertFile: serverCertFile,
		serverKeyFile:  serverKeyFile,
		clientCertFile: clientCertFile,
		clientKeyFile:  clientKeyFile,
		serverCert:     serverTLSCert,
		caPool:         pool,
	}
}

// issueLeaf signs a leaf certificate against caCert/caKey. withIPSAN adds
// 127.0.0.1 + localhost so a server certificate verifies against the loopback
// address httptest/grpc listen on.
func issueLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, eku x509.ExtKeyUsage, withIPSAN bool) (certPath, keyPath string, cert tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate %s key: %v", name, err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
	}
	if withIPSAN {
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		tmpl.DNSNames = []string{"localhost"}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create %s cert: %v", name, err)
	}
	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")
	writePEM(t, certPath, "CERTIFICATE", der)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal %s key: %v", name, err)
	}
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)

	cert, err = tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load %s keypair: %v", name, err)
	}
	return certPath, keyPath, cert
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// tlsConfigFrom builds a bare server-side tls.Config presenting mat's server
// leaf certificate, with no client-auth requirement. Callers that need mTLS
// set ClientAuth/ClientCAs on the returned value themselves.
func tlsConfigFrom(mat wireTLSMaterial) tls.Config {
	return tls.Config{Certificates: []tls.Certificate{mat.serverCert}}
}

// ---------------------------------------------------------------------------
// HTTP receiver
// ---------------------------------------------------------------------------

type partialSuccessSpec struct {
	rejected int64
	message  string
}

type wireHTTPServer struct {
	ts  *httptest.Server
	rec *wireRecorder

	mu      sync.Mutex
	status  int // 0 => 200
	partial *partialSuccessSpec
}

// newWireHTTPServer starts an in-process httptest server. When tlsConf is
// non-nil the server serves TLS with it (mTLS is expressed via
// tlsConf.ClientAuth/ClientCAs).
func newWireHTTPServer(t *testing.T, tlsConf *tls.Config) *wireHTTPServer {
	t.Helper()
	s := &wireHTTPServer{rec: newWireRecorder()}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	if tlsConf != nil {
		ts.TLS = tlsConf
		ts.StartTLS()
	} else {
		ts.Start()
	}
	s.ts = ts
	t.Cleanup(ts.Close)
	return s
}

func (s *wireHTTPServer) setStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

func (s *wireHTTPServer) setPartialSuccess(rejected int64, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = &partialSuccessSpec{rejected: rejected, message: msg}
}

func (s *wireHTTPServer) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	gzipped := r.Header.Get("Content-Encoding") == "gzip"
	if gzipped {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		decoded, err := io.ReadAll(gr)
		if err != nil {
			http.Error(w, "bad gzip body", http.StatusBadRequest)
			return
		}
		body = decoded
	}

	cr := capturedRequest{
		path:   r.URL.Path,
		header: r.Header.Clone(),
		gzip:   gzipped,
	}

	switch {
	case strings.HasSuffix(r.URL.Path, "/v1/metrics"):
		cr.signal = "metrics"
		var req collectormetricpb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "decode metrics", http.StatusBadRequest)
			return
		}
		cr.metrics = &req
	case strings.HasSuffix(r.URL.Path, "/v1/logs"):
		cr.signal = "logs"
		var req collectorlogpb.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "decode logs", http.StatusBadRequest)
			return
		}
		cr.logs = &req
	case strings.HasSuffix(r.URL.Path, "/v1/traces"):
		cr.signal = "traces"
		var req collectortracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, "decode traces", http.StatusBadRequest)
			return
		}
		cr.traces = &req
	default:
		// Unrecognized path: recorded as-is (used by the path-mismatch test),
		// left undecoded.
	}

	s.mu.Lock()
	status := s.status
	partial := s.partial
	s.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}

	var respBody []byte
	if status/100 == 2 {
		switch cr.signal {
		case "metrics":
			resp := &collectormetricpb.ExportMetricsServiceResponse{}
			if partial != nil {
				resp.PartialSuccess = &collectormetricpb.ExportMetricsPartialSuccess{
					RejectedDataPoints: partial.rejected,
					ErrorMessage:       partial.message,
				}
			}
			respBody, _ = proto.Marshal(resp)
		case "logs":
			resp := &collectorlogpb.ExportLogsServiceResponse{}
			if partial != nil {
				resp.PartialSuccess = &collectorlogpb.ExportLogsPartialSuccess{
					RejectedLogRecords: partial.rejected,
					ErrorMessage:       partial.message,
				}
			}
			respBody, _ = proto.Marshal(resp)
		case "traces":
			resp := &collectortracepb.ExportTraceServiceResponse{}
			if partial != nil {
				resp.PartialSuccess = &collectortracepb.ExportTracePartialSuccess{
					RejectedSpans: partial.rejected,
					ErrorMessage:  partial.message,
				}
			}
			respBody, _ = proto.Marshal(resp)
		}
		if len(respBody) > 0 {
			w.Header().Set("Content-Type", "application/x-protobuf")
		}
	}
	w.WriteHeader(status)
	if len(respBody) > 0 {
		_, _ = w.Write(respBody)
	}

	s.rec.record(cr)
}

// ---------------------------------------------------------------------------
// gRPC receiver
// ---------------------------------------------------------------------------

type wireGRPCServer struct {
	lis net.Listener
	srv *grpc.Server
	rec *wireRecorder
	tb  testing.TB

	mu      sync.Mutex
	code    codes.Code
	partial *partialSuccessSpec
}

func newWireGRPCServer(t *testing.T, creds credentials.TransportCredentials) *wireGRPCServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var opts []grpc.ServerOption
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	s := &wireGRPCServer{
		lis:  lis,
		srv:  grpc.NewServer(opts...),
		rec:  newWireRecorder(),
		tb:   t,
		code: codes.OK,
	}
	collectormetricpb.RegisterMetricsServiceServer(s.srv, &metricsHandler{s: s})
	collectorlogpb.RegisterLogsServiceServer(s.srv, &logsHandler{s: s})
	collectortracepb.RegisterTraceServiceServer(s.srv, &tracesHandler{s: s})

	go func() { _ = s.srv.Serve(lis) }()
	t.Cleanup(s.srv.Stop)
	return s
}

func (s *wireGRPCServer) addr() string { return s.lis.Addr().String() }

func (s *wireGRPCServer) setCode(c codes.Code) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = c
}

func (s *wireGRPCServer) setPartialSuccess(rejected int64, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = &partialSuccessSpec{rejected: rejected, message: msg}
}

// captureCommon builds the transport-agnostic parts of a capturedRequest from
// a gRPC context: incoming metadata (headers). gzip is deliberately NOT set
// here — see gzipDecompressCount's doc comment for why a per-request snapshot
// taken inside the handler can never observe its OWN request's decompression
// (grpc-go decompresses before the handler is invoked, so "before" and
// "after" read from inside the handler are identical). Callers that need to
// prove gzip engaged bracket the whole driveWirePipeline call with
// gzipDecompressSnapshot instead (see TestWireGRPC_GzipCompression).
func captureCommon(ctx context.Context, signal string) capturedRequest {
	cr := capturedRequest{signal: signal}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// grpc-go stores metadata keys lowercase; build via Add so http.Header's
		// canonical-MIME-key lookups (Get("Authorization")) work the same way
		// they do for the HTTP transport's captured headers.
		h := make(http.Header, len(md))
		for k, vs := range md {
			for _, v := range vs {
				h.Add(k, v)
			}
		}
		cr.header = h
	}
	// peer is unused today but confirms the call really crossed the loopback
	// socket rather than being an in-process short-circuit.
	if _, ok := peer.FromContext(ctx); !ok {
		panic("grpc handler invoked with no peer in context")
	}
	return cr
}

func (s *wireGRPCServer) codeAndPartial() (codes.Code, *partialSuccessSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code, s.partial
}

type metricsHandler struct {
	collectormetricpb.UnimplementedMetricsServiceServer
	s *wireGRPCServer
}

func (h *metricsHandler) Export(ctx context.Context, req *collectormetricpb.ExportMetricsServiceRequest) (*collectormetricpb.ExportMetricsServiceResponse, error) {
	cr := captureCommon(ctx, "metrics")
	cr.metrics = req
	h.s.rec.record(cr)

	code, partial := h.s.codeAndPartial()
	if code != codes.OK {
		return nil, status.Error(code, "wirecontract test forced error")
	}
	resp := &collectormetricpb.ExportMetricsServiceResponse{}
	if partial != nil {
		resp.PartialSuccess = &collectormetricpb.ExportMetricsPartialSuccess{
			RejectedDataPoints: partial.rejected,
			ErrorMessage:       partial.message,
		}
	}
	return resp, nil
}

type logsHandler struct {
	collectorlogpb.UnimplementedLogsServiceServer
	s *wireGRPCServer
}

func (h *logsHandler) Export(ctx context.Context, req *collectorlogpb.ExportLogsServiceRequest) (*collectorlogpb.ExportLogsServiceResponse, error) {
	cr := captureCommon(ctx, "logs")
	cr.logs = req
	h.s.rec.record(cr)

	code, partial := h.s.codeAndPartial()
	if code != codes.OK {
		return nil, status.Error(code, "wirecontract test forced error")
	}
	resp := &collectorlogpb.ExportLogsServiceResponse{}
	if partial != nil {
		resp.PartialSuccess = &collectorlogpb.ExportLogsPartialSuccess{
			RejectedLogRecords: partial.rejected,
			ErrorMessage:       partial.message,
		}
	}
	return resp, nil
}

type tracesHandler struct {
	collectortracepb.UnimplementedTraceServiceServer
	s *wireGRPCServer
}

func (h *tracesHandler) Export(ctx context.Context, req *collectortracepb.ExportTraceServiceRequest) (*collectortracepb.ExportTraceServiceResponse, error) {
	cr := captureCommon(ctx, "traces")
	cr.traces = req
	h.s.rec.record(cr)

	code, partial := h.s.codeAndPartial()
	if code != codes.OK {
		return nil, status.Error(code, "wirecontract test forced error")
	}
	resp := &collectortracepb.ExportTraceServiceResponse{}
	if partial != nil {
		resp.PartialSuccess = &collectortracepb.ExportTracePartialSuccess{
			RejectedSpans: partial.rejected,
			ErrorMessage:  partial.message,
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// gRPC gzip observation: grpc-go strips reserved headers like grpc-encoding
// before a handler ever sees metadata, so the only way to prove gzip actually
// ran on the wire is to intercept the codec that does the decompression.
// We register our OWN "gzip" compressor that wraps the real one and counts
// Decompress calls; behavior is unchanged (it delegates), so this is safe to
// install once for the whole test binary.
// ---------------------------------------------------------------------------

var gzipDecompressCount atomic.Int64

type countingGzipCompressor struct {
	inner encoding.Compressor
}

func (c countingGzipCompressor) Compress(w io.Writer) (io.WriteCloser, error) {
	return c.inner.Compress(w)
}

func (c countingGzipCompressor) Decompress(r io.Reader) (io.Reader, error) {
	gzipDecompressCount.Add(1)
	return c.inner.Decompress(r)
}

func (c countingGzipCompressor) Name() string { return "gzip" }

// gzipDecompressSnapshot returns the current process-wide Decompress call
// count. Bracket a whole operation with it (snapshot before, snapshot after,
// compare) rather than reading it from inside a single request's handler —
// see captureCommon's doc comment for why the latter is always a no-op.
func gzipDecompressSnapshot() int64 { return gzipDecompressCount.Load() }

func init() {
	inner := encoding.GetCompressor("gzip")
	if inner == nil {
		panic("google.golang.org/grpc/encoding/gzip did not register a \"gzip\" compressor")
	}
	encoding.RegisterCompressor(countingGzipCompressor{inner: inner})
}

// ---------------------------------------------------------------------------
// Attribute helpers shared by both transports' assertions.
// ---------------------------------------------------------------------------

func wireFindAttr(kvs []*commonpb.KeyValue, key string) (*commonpb.AnyValue, bool) {
	for _, kv := range kvs {
		if kv.GetKey() == key {
			return kv.GetValue(), true
		}
	}
	return nil, false
}

func wireHasAttr(kvs []*commonpb.KeyValue, key string) bool {
	_, ok := wireFindAttr(kvs, key)
	return ok
}

func wireAttrString(kvs []*commonpb.KeyValue, key string) (string, bool) {
	v, ok := wireFindAttr(kvs, key)
	if !ok {
		return "", false
	}
	return v.GetStringValue(), true
}

// metricDataPointAttrs returns every data point's attribute list across every
// metric in req, regardless of instrument kind, plus every Sum/Histogram's
// aggregation temporality (for the cumulative-pinning assertion).
func metricDataPointAttrs(req *collectormetricpb.ExportMetricsServiceRequest) (attrSets [][]*commonpb.KeyValue, temporalities []metricpb.AggregationTemporality) {
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				switch data := m.GetData().(type) {
				case *metricpb.Metric_Gauge:
					for _, dp := range data.Gauge.GetDataPoints() {
						attrSets = append(attrSets, dp.GetAttributes())
					}
				case *metricpb.Metric_Sum:
					temporalities = append(temporalities, data.Sum.GetAggregationTemporality())
					for _, dp := range data.Sum.GetDataPoints() {
						attrSets = append(attrSets, dp.GetAttributes())
					}
				case *metricpb.Metric_Histogram:
					temporalities = append(temporalities, data.Histogram.GetAggregationTemporality())
					for _, dp := range data.Histogram.GetDataPoints() {
						attrSets = append(attrSets, dp.GetAttributes())
					}
				case *metricpb.Metric_ExponentialHistogram:
					temporalities = append(temporalities, data.ExponentialHistogram.GetAggregationTemporality())
					for _, dp := range data.ExponentialHistogram.GetDataPoints() {
						attrSets = append(attrSets, dp.GetAttributes())
					}
				case *metricpb.Metric_Summary:
					for _, dp := range data.Summary.GetDataPoints() {
						attrSets = append(attrSets, dp.GetAttributes())
					}
				}
			}
		}
	}
	return attrSets, temporalities
}

func metricResourceAttrs(req *collectormetricpb.ExportMetricsServiceRequest) []*commonpb.KeyValue {
	for _, rm := range req.GetResourceMetrics() {
		if rm.GetResource() != nil {
			return rm.GetResource().GetAttributes()
		}
	}
	return nil
}

func logResourceAttrs(req *collectorlogpb.ExportLogsServiceRequest) []*commonpb.KeyValue {
	for _, rl := range req.GetResourceLogs() {
		if rl.GetResource() != nil {
			return rl.GetResource().GetAttributes()
		}
	}
	return nil
}

func logRecordAttrs(req *collectorlogpb.ExportLogsServiceRequest) [][]*commonpb.KeyValue {
	var out [][]*commonpb.KeyValue
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				out = append(out, lr.GetAttributes())
			}
		}
	}
	return out
}

func traceResourceAttrs(req *collectortracepb.ExportTraceServiceRequest) []*commonpb.KeyValue {
	for _, rs := range req.GetResourceSpans() {
		if rs.GetResource() != nil {
			return rs.GetResource().GetAttributes()
		}
	}
	return nil
}

func spanAttrs(req *collectortracepb.ExportTraceServiceRequest) [][]*commonpb.KeyValue {
	var out [][]*commonpb.KeyValue
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				out = append(out, sp.GetAttributes())
			}
		}
	}
	return out
}

// wantConstAttrs are the roadmap-item-L provider-scoped constant attributes
// (internal/telemetry/CLAUDE.md, resource.go's constLabelAttrs) every data
// point / log record / span must carry, and every Resource must NOT.
const (
	constAttrTailnet  = "tailscale.tailnet"
	constAttrProvider = "tailscale2otel.provider"
)

// assertConstAttrsOnItems checks that every one of attrSets carries the
// expected tailnet/provider const attributes, and assertConstAttrsAbsentFromResource
// checks the Resource never does — shared by all three signals, both
// transports.
func assertConstAttrsOnItems(t *testing.T, kind string, attrSets [][]*commonpb.KeyValue, wantTailnet, wantProvider string) {
	t.Helper()
	if len(attrSets) == 0 {
		t.Fatalf("no %s captured to assert const attrs on", kind)
	}
	for i, attrs := range attrSets {
		if got, ok := wireAttrString(attrs, constAttrTailnet); !ok || got != wantTailnet {
			t.Errorf("%s[%d]: %s = %q, %v, want %q", kind, i, constAttrTailnet, got, ok, wantTailnet)
		}
		if got, ok := wireAttrString(attrs, constAttrProvider); !ok || got != wantProvider {
			t.Errorf("%s[%d]: %s = %q, %v, want %q", kind, i, constAttrProvider, got, ok, wantProvider)
		}
	}
}

func assertConstAttrsAbsentFromResource(t *testing.T, resourceAttrs []*commonpb.KeyValue) {
	t.Helper()
	if wireHasAttr(resourceAttrs, constAttrTailnet) {
		t.Errorf("Resource carries %s — roadmap item L moved this to per-item attributes only (#187/CLAUDE.md)", constAttrTailnet)
	}
	if wireHasAttr(resourceAttrs, constAttrProvider) {
		t.Errorf("Resource carries %s — roadmap item L moved this to per-item attributes only (#187/CLAUDE.md)", constAttrProvider)
	}
}

// assertServiceVersion checks the #187 Resource split: metrics never carry
// service.version, logs/traces always do (when opts.ServiceVersion is set).
func assertServiceVersion(t *testing.T, signal string, resourceAttrs []*commonpb.KeyValue, want bool, wantValue string) {
	t.Helper()
	got, ok := wireAttrString(resourceAttrs, "service.version")
	switch {
	case want && !ok:
		t.Errorf("%s Resource is missing service.version (#187 requires it present on logs/traces)", signal)
	case want && got != wantValue:
		t.Errorf("%s Resource service.version = %q, want %q", signal, got, wantValue)
	case !want && ok:
		t.Errorf("%s Resource carries service.version=%q — #187 requires metrics OMIT it (per-series cardinality)", signal, got)
	}
}

// assertMetricsDeliveryFailure checks the metrics signal's DeliveryState
// against wantFailure: true after a hard transport failure (e.g. a non-2xx
// response), false after a successful exchange (including one carrying an
// OTLP partial_success).
func assertMetricsDeliveryFailure(t *testing.T, p *telemetry.Provider, wantFailure bool) {
	t.Helper()
	states := p.Delivery()
	var metricsState *telemetry.DeliveryState
	for i := range states {
		if states[i].Signal == telemetry.SignalMetrics {
			metricsState = &states[i]
		}
	}
	if metricsState == nil {
		t.Fatal("no metrics delivery state returned by Provider.Delivery()")
	}
	if wantFailure && metricsState.Failures == 0 {
		t.Error("delivery state shows zero failures; want at least one")
	}
	if !wantFailure && metricsState.Failures != 0 {
		t.Errorf("delivery state shows %d failures; want zero", metricsState.Failures)
	}
}

// assertMetricsPartialSuccess pins the post-#359 accounting: a backend that
// accepts an export while rejecting some items is forward progress, not an
// outage, so it must land in PartialSuccesses and must NOT touch Failures or
// ConsecutiveFailures. That distinction is load-bearing — three consecutive
// "failures" flip DeliveryState.Failing(), which would report an outage on a
// deployment that is delivering fine.
func assertMetricsPartialSuccess(t *testing.T, p *telemetry.Provider) {
	t.Helper()
	states := p.Delivery()
	var st *telemetry.DeliveryState
	for i := range states {
		if states[i].Signal == telemetry.SignalMetrics {
			st = &states[i]
		}
	}
	if st == nil {
		t.Fatal("no metrics delivery state returned by Provider.Delivery()")
	}
	if st.PartialSuccesses == 0 {
		t.Error("PartialSuccesses = 0; want at least one")
	}
	if st.Failures != 0 {
		t.Errorf("Failures = %d; want 0 — a partial success is not an outage", st.Failures)
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d; want 0 — else Failing() reports a healthy deployment as down",
			st.ConsecutiveFailures)
	}
}

func assertCumulativeTemporality(t *testing.T, temporalities []metricpb.AggregationTemporality) {
	t.Helper()
	if len(temporalities) == 0 {
		t.Fatal("no Sum/Histogram metrics captured to assert temporality on")
	}
	for i, tp := range temporalities {
		if tp != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
			t.Errorf("metric[%d] aggregation_temporality = %v, want CUMULATIVE", i, tp)
		}
	}
}

func assertDeltaTemporality(t *testing.T, requests []capturedRequest) {
	t.Helper()
	var sawCounter, sawHistogram, sawUpDown bool
	for _, req := range requests {
		if req.metrics == nil {
			continue
		}
		for _, rm := range req.metrics.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					switch m.GetName() {
					case "tailscale.wiretest.counter":
						sum := m.GetSum()
						if sum == nil {
							t.Errorf("wiretest counter data = %T, want Sum", m.GetData())
							continue
						}
						sawCounter = true
						if got := sum.GetAggregationTemporality(); got != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
							t.Errorf("wiretest counter aggregation_temporality = %v, want DELTA", got)
						}
					case "tailscale.wiretest.histogram":
						hist := m.GetHistogram()
						if hist == nil {
							t.Errorf("wiretest histogram data = %T, want Histogram", m.GetData())
							continue
						}
						sawHistogram = true
						if got := hist.GetAggregationTemporality(); got != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
							t.Errorf("wiretest histogram aggregation_temporality = %v, want DELTA", got)
						}
					case "tailscale.wiretest.updown":
						sum := m.GetSum()
						if sum == nil {
							t.Errorf("wiretest updown data = %T, want Sum", m.GetData())
							continue
						}
						sawUpDown = true
						if got := sum.GetAggregationTemporality(); got != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
							t.Errorf("wiretest updown aggregation_temporality = %v, want CUMULATIVE", got)
						}
					}
				}
			}
		}
	}
	if !sawCounter {
		t.Error("wiretest counter not captured")
	}
	if !sawHistogram {
		t.Error("wiretest histogram not captured")
	}
	if !sawUpDown {
		t.Error("wiretest updown counter not captured")
	}
}

func assertWireTestGauge(t *testing.T, req *collectormetricpb.ExportMetricsServiceRequest) {
	t.Helper()
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() != "tailscale.wiretest.gauge" {
					continue
				}
				gauge, ok := m.GetData().(*metricpb.Metric_Gauge)
				if !ok || gauge.Gauge == nil {
					t.Fatalf("wiretest gauge data = %T, want Gauge", m.GetData())
				}
				points := gauge.Gauge.GetDataPoints()
				if len(points) != 1 {
					t.Fatalf("wiretest gauge datapoints = %d, want 1", len(points))
				}
				if got := points[0].GetAsDouble(); got != 1 {
					t.Errorf("wiretest gauge value = %v, want 1", got)
				}
				return
			}
		}
	}
	t.Fatal("wiretest gauge not captured")
}

// ---------------------------------------------------------------------------
// Shared pipeline driver: builds one Provider against the given
// protocol/endpoint, emits one data point of each instrument kind, one log
// record, and one span, flushes all three signals, and returns the decoded
// wire request captured for each — so HTTP and gRPC tests exercise an
// identical pipeline and can share every assertion above.
// ---------------------------------------------------------------------------

// wiretestTailnet and wiretestProvider are the const-attribute values the
// driven pipeline configures; tests assert these appear as per-item
// attributes (never Resource attributes) via assertConstAttrsOnItems.
const (
	wiretestTailnet        = "wiretest-tailnet"
	wiretestProvider       = "tailscale"
	wiretestServiceVersion = "9.9.9-wiretest"
)

// driveWirePipeline drives the pipeline and blocks (deterministically, via
// wireRecorder's channel — no sleeps or polling) until all three signals have
// been captured, or fails the test. configure may override any Options field
// (TLS, headers, protocol-specific endpoint quirks) before NewProvider runs.
func driveWirePipeline(t *testing.T, rec *wireRecorder, protocol, endpoint string, configure func(*telemetry.Options)) map[string]capturedRequest {
	t.Helper()
	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: wiretestServiceVersion,
		InstanceID:     "wiretest-instance",
		Protocol:       protocol,
		Endpoint:       endpoint,
		TailnetName:    wiretestTailnet,
		Provider:       wiretestProvider,
		TracingEnabled: true,
		TraceSampler:   "always_on",
		MetricInterval: time.Hour,
	}
	if configure != nil {
		configure(&opts)
	}

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := p.Shutdown(sctx); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		})
	}
	t.Cleanup(shutdown)

	e := p.Emitter()
	e.Counter("tailscale.wiretest.counter", "1", "wiretest counter", 1, telemetry.Attrs{"k": "v"})
	e.UpDownCounter("tailscale.wiretest.updown", "1", "wiretest updown counter", 1, telemetry.Attrs{"k": "v"})
	e.Gauge("tailscale.wiretest.gauge", "1", "wiretest gauge", 1, telemetry.Attrs{"k": "v"})
	e.Histogram("tailscale.wiretest.histogram", "s", "wiretest histogram", 0.5, []float64{0.1, 1, 10}, telemetry.Attrs{"k": "v"})
	e.LogEvent(telemetry.Event{
		Name:     "tailscale.wiretest.log",
		Body:     "wire contract test log",
		Severity: telemetry.SeverityInfo,
		Attrs:    telemetry.Attrs{"k": "v"},
	})
	_, span := p.Tracer().Start(ctx, "wiretest-span")

	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	span.End()

	got := make(map[string]capturedRequest, 3)
	// ForceFlush waits for the metric and log exporters to finish. Capture those
	// requests before Shutdown triggers their second collection; delta sums are
	// intentionally empty on that second collection while cumulative gauges and
	// UpDownCounters remain present.
	got["metrics"] = waitForSignal(t, rec, "metrics", 5*time.Second, got)
	got["logs"] = waitForSignal(t, rec, "logs", 5*time.Second, got)

	// Provider.ForceFlush deliberately excludes traces (see its doc comment in
	// provider.go); only Shutdown flushes the span processor.
	shutdown()
	got["traces"] = waitForSignal(t, rec, "traces", 5*time.Second, got)

	return got
}

// waitForSignal drains rec's channel until a request for the wanted signal
// arrives, stashing any other-signal requests it sees along the way into
// already so a later call for that signal returns immediately instead of
// re-blocking. Blocks on the channel; never polls.
func waitForSignal(t *testing.T, rec *wireRecorder, signal string, timeout time.Duration, already map[string]capturedRequest) capturedRequest {
	t.Helper()
	if cr, ok := already[signal]; ok {
		return cr
	}
	deadline := time.After(timeout)
	for {
		select {
		case cr := <-rec.ch:
			if cr.signal == signal {
				return cr
			}
			already[cr.signal] = cr
		case <-deadline:
			t.Fatalf("timed out after %s waiting for a %s wire request", timeout, signal)
			return capturedRequest{}
		}
	}
}
