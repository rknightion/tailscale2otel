package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// These tests exercise the production exporter constructors and actual
// protobuf responses, without a transport override or an SDK reader fake.
// A nil Export result for a nonzero rejection cannot acknowledge a receipt.
type ackWireSink struct {
	mu             sync.Mutex
	metricRequests []*metrics.ExportMetricsServiceRequest
	logRequests    []*logs.ExportLogsServiceRequest
	rejected       int64
	warning        string
	contentType    string
	malformed      bool
	literalEmpty   bool
}

func (s *ackWireSink) metricResponse() *metrics.ExportMetricsServiceResponse {
	if s.literalEmpty {
		return &metrics.ExportMetricsServiceResponse{}
	}
	return &metrics.ExportMetricsServiceResponse{PartialSuccess: &metrics.ExportMetricsPartialSuccess{RejectedDataPoints: s.rejected, ErrorMessage: s.warning}}
}
func (s *ackWireSink) logResponse() *logs.ExportLogsServiceResponse {
	if s.literalEmpty {
		return &logs.ExportLogsServiceResponse{}
	}
	return &logs.ExportLogsServiceResponse{PartialSuccess: &logs.ExportLogsPartialSuccess{RejectedLogRecords: s.rejected, ErrorMessage: s.warning}}
}
func (s *ackWireSink) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", 400)
		return
	}
	var response proto.Message
	s.mu.Lock()
	switch r.URL.Path {
	case "/v1/metrics":
		request := new(metrics.ExportMetricsServiceRequest)
		err = proto.Unmarshal(body, request)
		s.metricRequests = append(s.metricRequests, request)
		response = s.metricResponse()
	case "/v1/logs":
		request := new(logs.ExportLogsServiceRequest)
		err = proto.Unmarshal(body, request)
		s.logRequests = append(s.logRequests, request)
		response = s.logResponse()
	default:
		s.mu.Unlock()
		http.Error(w, "path", 404)
		return
	}
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "protobuf", 400)
		return
	}
	encoded, err := proto.Marshal(response)
	if err != nil {
		http.Error(w, "response", 500)
		return
	}
	contentType := s.contentType
	if contentType == "" && !s.literalEmpty {
		contentType = "application/x-protobuf"
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if s.malformed {
		encoded = []byte{0xff} // invalid protobuf, not an empty successful response
	}
	_, _ = w.Write(encoded)
}

type ackWireMetricServer struct {
	metrics.UnimplementedMetricsServiceServer
	sink *ackWireSink
}

func (s ackWireMetricServer) Export(_ context.Context, r *metrics.ExportMetricsServiceRequest) (*metrics.ExportMetricsServiceResponse, error) {
	s.sink.mu.Lock()
	defer s.sink.mu.Unlock()
	s.sink.metricRequests = append(s.sink.metricRequests, proto.Clone(r).(*metrics.ExportMetricsServiceRequest))
	return s.sink.metricResponse(), nil
}

type ackWireLogServer struct {
	logs.UnimplementedLogsServiceServer
	sink *ackWireSink
}

func (s ackWireLogServer) Export(_ context.Context, r *logs.ExportLogsServiceRequest) (*logs.ExportLogsServiceResponse, error) {
	s.sink.mu.Lock()
	defer s.sink.mu.Unlock()
	s.sink.logRequests = append(s.sink.logRequests, proto.Clone(r).(*logs.ExportLogsServiceRequest))
	return s.sink.logResponse(), nil
}

type ackWireCase struct {
	name         string
	rejected     int64
	warning      string
	contentType  string
	malformed    bool
	literalEmpty bool
}

func TestExportACKProductionWire(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		responses := []ackWireCase{
			{name: "nonzero_rejection", rejected: 1, warning: "synthetic rejection"},
			{name: "zero_rejection_warning", warning: "synthetic warning"},
			{name: "empty_partial_success_message"},
			{name: "literal_empty_success", literalEmpty: true},
		}
		if protocol == "http" {
			responses = append(responses,
				ackWireCase{name: "nonzero_rejection_parameterized_content_type", rejected: 1, warning: "synthetic rejection", contentType: "application/x-protobuf; charset=utf-8"},
				ackWireCase{name: "malformed_protobuf", malformed: true},
				ackWireCase{name: "malformed_unexpected_content_type", contentType: "text/html", malformed: true},
				ackWireCase{name: "literal_empty_unknown_mime", literalEmpty: true, contentType: "text/html"},
				ackWireCase{name: "negative_rejection", rejected: -1},
				ackWireCase{name: "oversized_response", warning: strings.Repeat("x", ackResponseLimit+1)},
			)
		}
		for _, response := range responses {
			t.Run(protocol+"/"+response.name, func(t *testing.T) {
				sink := &ackWireSink{rejected: response.rejected, warning: response.warning, contentType: response.contentType, malformed: response.malformed, literalEmpty: response.literalEmpty}
				var endpoint string
				if protocol == "http" {
					server := httptest.NewServer(http.HandlerFunc(sink.serveHTTP))
					t.Cleanup(server.Close)
					endpoint = server.URL
				} else {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					server := grpc.NewServer()
					metrics.RegisterMetricsServiceServer(server, ackWireMetricServer{sink: sink})
					logs.RegisterLogsServiceServer(server, ackWireLogServer{sink: sink})
					done := make(chan struct{})
					go func() { defer close(done); _ = server.Serve(listener) }()
					t.Cleanup(func() { server.Stop(); <-done })
					endpoint = listener.Addr().String()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				opts := Options{Protocol: protocol, Endpoint: endpoint, Insecure: true, Transport: TransportOptions{Timeout: 2 * time.Second, Compression: "none", Retry: &RetryPolicy{Enabled: false}}}
				metricExporter, err := newMetricExporter(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				logExporter, err := newLogExporter(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = metricExporter.Shutdown(c)
					_ = logExporter.Shutdown(c)
				}()
				at := time.Date(2000, 1, 1, 0, 1, 0, 0, time.UTC)
				start := at.Add(-time.Minute)
				data := metricdata.ResourceMetrics{Resource: resource.NewSchemaless(attribute.String("service.name", "synthetic-wire")), ScopeMetrics: []metricdata.ScopeMetrics{{Scope: instrumentation.Scope{Name: "synthetic-wire"}, Metrics: []metricdata.Metrics{{Name: "synthetic.wire.count", Unit: "1", Data: metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, IsMonotonic: true, DataPoints: []metricdata.DataPoint[int64]{{StartTime: start, Time: at, Value: 3}}}}}}}}
				var record sdklog.Record
				record.SetTimestamp(at)
				record.SetObservedTimestamp(at.Add(time.Millisecond))
				record.SetEventName("synthetic.wire.event")
				record.SetBody(attribute.StringValue("synthetic body"))
				metricErr := metricExporter.Export(ctx, &data)
				logErr := logExporter.Export(ctx, []sdklog.Record{record})
				sink.mu.Lock()
				defer sink.mu.Unlock()
				if len(sink.metricRequests) != 1 || len(sink.logRequests) != 1 {
					t.Fatalf("wire request count metrics/logs = %d/%d, want 1/1", len(sink.metricRequests), len(sink.logRequests))
				}
				points := sink.metricRequests[0].GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()[0].GetSum().GetDataPoints()
				if len(points) != 1 || points[0].GetAsInt() != 3 || points[0].GetTimeUnixNano() != uint64(at.UnixNano()) || points[0].GetStartTimeUnixNano() != uint64(start.UnixNano()) {
					t.Fatalf("actual serialized metric mismatch: %v", points)
				}
				records := sink.logRequests[0].GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()
				if len(records) != 1 || records[0].GetTimeUnixNano() != uint64(at.UnixNano()) || records[0].GetObservedTimeUnixNano() != uint64(at.Add(time.Millisecond).UnixNano()) || records[0].GetEventName() != "synthetic.wire.event" {
					t.Fatalf("actual serialized log mismatch: %v", records)
				}
				t.Logf("actual wire response rejected=%d warning=%t content-type=%q malformed=%t; metric Export error=%T %v; log Export error=%T %v; input metric value=3 timestamp=%s; log records=1", response.rejected, response.warning != "", response.contentType, response.malformed, metricErr, metricErr, logErr, logErr, at.Format(time.RFC3339Nano))
				if response.rejected != 0 || response.malformed || len(response.warning) > ackResponseLimit {
					if metricErr == nil {
						t.Error("rejected/malformed wire metric response returned nil; cannot acknowledge required metrics")
					}
					if logErr == nil {
						t.Error("rejected/malformed wire log response returned nil; cannot acknowledge required logs")
					}
				} else if response.warning == "" {
					if metricErr != nil || logErr != nil {
						t.Errorf("empty successful wire response errors = %v / %v", metricErr, logErr)
					}
				} else {
					for signal, warningErr := range map[string]error{"metrics": metricErr, "logs": logErr} {
						if warningErr == nil {
							t.Errorf("%s warning fixture lost SDK leaf", signal)
						}
						result := classifyExportResult(warningErr, signal)
						if !result.ack || !result.warning {
							t.Errorf("%s zero-count warning blocked ACK: %+v", signal, result)
						}
						joined := classifyExportResult(errors.Join(warningErr, context.Canceled), signal)
						if joined.ack || !errors.Is(joined.err, context.Canceled) {
							t.Errorf("%s joined cancellation lost: %+v", signal, joined)
						}
					}
				}
			})
		}
	}
}

type ackRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ackRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ackClosedBody struct {
	io.Reader
	closed bool
}

func (b *ackClosedBody) Close() error { b.closed = true; return nil }

func TestExportACKGzipBoundAndClosure(t *testing.T) {
	for _, signal := range []string{"metrics", "logs"} {
		for _, tc := range []struct {
			name      string
			size      int
			truncate  bool
			wantError bool
		}{
			{name: "valid_gzip", size: 0},
			{name: "compressed_expansion", size: ackResponseLimit + 1, wantError: true},
			{name: "truncated_checksum", truncate: true, wantError: true},
		} {
			t.Run(signal+"/"+tc.name, func(t *testing.T) {
				var compressed bytes.Buffer
				gz := gzip.NewWriter(&compressed)
				payload := bytes.Repeat([]byte{0}, tc.size)
				if _, err := gz.Write(payload); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
				encoded := compressed.Bytes()
				if tc.truncate {
					encoded = encoded[:len(encoded)-4]
				}
				body := &ackClosedBody{Reader: bytes.NewReader(encoded)}
				guard := validatingACKTransport{signal: signal, base: ackRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/x-protobuf"}, "Content-Encoding": {"gzip"}}, Body: body}, nil
				})}
				req := httptest.NewRequest(http.MethodPost, "http://example.invalid/v1/"+signal, nil)
				req.Header.Set("Accept-Encoding", "gzip") // explicit gzip must be bounded too
				result, err := guard.RoundTrip(req)
				if !body.closed {
					t.Error("original response body not closed")
				}
				if (err != nil) != tc.wantError {
					t.Fatalf("error=%v, wantError=%t", err, tc.wantError)
				}
				if err == nil {
					defer result.Body.Close()
					restored, err := io.ReadAll(result.Body)
					if err != nil || len(restored) != len(payload) || result.Header.Get("Content-Encoding") != "" || result.ContentLength != int64(len(payload)) {
						t.Fatalf("restored gzip response: %v %+v", err, result)
					}
				}
			})
		}
	}
}

func TestExportACKClientSettings(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "3000")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "1200")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_TIMEOUT", "2300")
	for signal, want := range map[string]time.Duration{"metrics": 1200 * time.Millisecond, "logs": 2300 * time.Millisecond} {
		client, err := guardedHTTPClient(Options{}, signal)
		if err != nil {
			t.Fatal(err)
		}
		if client.Timeout != want {
			t.Errorf("%s timeout=%s, want=%s", signal, client.Timeout, want)
		}
		client, err = guardedHTTPClient(Options{Transport: TransportOptions{Timeout: time.Second}, InsecureSkipVerify: true}, signal)
		if err != nil {
			t.Fatal(err)
		}
		guard := client.Transport.(*validatingACKTransport)
		transport := guard.base.(*http.Transport)
		if client.Timeout != time.Second || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify || transport.Proxy == nil {
			t.Fatalf("explicit client options lost: %+v", client)
		}
	}
	// SDK-env TLS failures fail closed rather than falling back to system roots.
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE", "/nonexistent/synthetic.pem")
	if _, err := guardedHTTPClient(Options{}, "metrics"); !errors.Is(err, errACKTLS) {
		t.Errorf("invalid env certificate error=%v", err)
	}
	client, err := guardedHTTPClient(Options{DynamicTLSConfig: func() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }}, "metrics")
	if err != nil || client == nil {
		t.Fatalf("dynamic TLS precedence lost: %v", err)
	}
}

func TestExportACKUnknownLeafFailsClosed(t *testing.T) {
	for _, err := range []error{errors.New("OTLP partial success: warning (0 logs rejected)"), context.DeadlineExceeded, errors.Join(errors.New("serialization"), context.Canceled)} {
		if classifyExportResult(err, "logs").ack {
			t.Errorf("unknown error acknowledged: %T", err)
		}
	}
}
