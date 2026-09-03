package telemetry_test

// Real OTLP/gRPC wire-contract integration tests (#357): a real grpc.Server
// on a loopback net.Listener, registering the three OTLP collector services,
// decoding the actual protobuf requests the SDK sends. Mirrors
// wirecontract_http_test.go so the two transports share every assertion
// (defined once in wirecontract_helpers_test.go) — a gap between them is
// visible as an extra/missing test, not implicit.
//
// insecureGRPC is required on every non-TLS test here: unlike the HTTP
// exporter (which defaults to plaintext when no TLS material is configured),
// otlpmetricgrpc/otlploggrpc/otlptracegrpc default to a SECURE connection —
// Options.Insecure must be set explicitly for a plaintext dial, matching the
// documented meaning of that field in provider.go ("plaintext: disable TLS
// entirely"). Omitting it against a plaintext test server produces a
// misleading transport-level handshake error rather than exercising the
// intended behavior, which is exactly what happened on the first run of this
// suite (see the FINDINGS in the delivering agent's final report).

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

func insecureGRPC(o *telemetry.Options) { o.Insecure = true }

// TestWireGRPC_EachSignalReachesItsOwnService is the gRPC analog of the HTTP
// per-path test: metrics/logs/traces must reach the correspondingly
// registered MetricsService/LogsService/TraceService, not just "a" service —
// the wireGRPCServer registers three separate handler types precisely so a
// misrouted export is visible.
func TestWireGRPC_EachSignalReachesItsOwnService(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), insecureGRPC)

	if got["metrics"].metrics == nil {
		t.Error("metrics request did not decode via MetricsServiceServer.Export")
	}
	if got["logs"].logs == nil {
		t.Error("logs request did not decode via LogsServiceServer.Export")
	}
	if got["traces"].traces == nil {
		t.Error("traces request did not decode via TraceServiceServer.Export")
	}
}

// TestWireGRPC_AuthHeadersArriveIntact mirrors TestWireHTTP_AuthHeadersArriveIntact.
func TestWireGRPC_AuthHeadersArriveIntact(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), func(o *telemetry.Options) {
		o.Insecure = true
		o.Headers = map[string]string{
			"authorization":   "Bearer wiretest-secret-token",
			"x-custom-header": "custom-value",
		}
	})
	for signal, cr := range got {
		if v := cr.header.Get("Authorization"); v != "Bearer wiretest-secret-token" {
			t.Errorf("%s authorization metadata = %q, want the configured bearer token", signal, v)
		}
		if v := cr.header.Get("X-Custom-Header"); v != "custom-value" {
			t.Errorf("%s x-custom-header metadata = %q, want custom-value", signal, v)
		}
	}
}

// TestWireGRPC_GzipCompression mirrors TestWireHTTP_GzipCompression: the
// countingGzipCompressor (registered once in wirecontract_helpers_test.go's
// init) must observe at least one Decompress call per signal across the
// pipeline. It brackets the whole driveWirePipeline call rather than reading
// the counter from inside a request handler — grpc-go decompresses BEFORE
// invoking the handler, so a snapshot taken inside the handler that received
// the compressed request can never see its own decompression (see
// captureCommon's doc comment; this is exactly the bug the suite's first
// draft had, caught by actually running it).
func TestWireGRPC_GzipCompression(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	s := newWireGRPCServer(t, nil)
	before := gzipDecompressSnapshot()
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), insecureGRPC)
	after := gzipDecompressSnapshot()

	if len(got) != 3 {
		t.Fatalf("got %d signals, want 3", len(got))
	}
	// One decompress call per signal (metrics, logs, traces) at minimum.
	if want := before + 3; after < want {
		t.Errorf("gzip Decompress call count = %d, want at least %d (one per exported signal)", after, want)
	}
}

// TestWireGRPC_TLSServerVerification mirrors TestWireHTTP_TLSServerVerification.
func TestWireGRPC_TLSServerVerification(t *testing.T) {
	mat := newWireTLSMaterial(t)
	tlsConf := tlsConfigFrom(mat)
	creds := credentials.NewTLS(&tlsConf)
	s := newWireGRPCServer(t, creds)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), func(o *telemetry.Options) {
		o.CAFile = mat.caFile
	})
	if len(got) != 3 {
		t.Fatalf("got %d signals over TLS, want 3", len(got))
	}
}

// TestWireGRPC_MutualTLS mirrors TestWireHTTP_MutualTLS.
func TestWireGRPC_MutualTLS(t *testing.T) {
	mat := newWireTLSMaterial(t)
	tlsConf := tlsConfigFrom(mat)
	tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConf.ClientCAs = mat.caPool
	creds := credentials.NewTLS(&tlsConf)
	s := newWireGRPCServer(t, creds)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), func(o *telemetry.Options) {
		o.CAFile = mat.caFile
		o.CertFile = mat.clientCertFile
		o.KeyFile = mat.clientKeyFile
	})
	if len(got) != 3 {
		t.Fatalf("got %d signals over mTLS, want 3", len(got))
	}
}

// TestWireGRPC_MutualTLSRejectsMissingClientCert mirrors
// TestWireHTTP_MutualTLSRejectsMissingClientCert.
func TestWireGRPC_MutualTLSRejectsMissingClientCert(t *testing.T) {
	mat := newWireTLSMaterial(t)
	tlsConf := tlsConfigFrom(mat)
	tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConf.ClientCAs = mat.caPool
	creds := credentials.NewTLS(&tlsConf)
	s := newWireGRPCServer(t, creds)

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "grpc",
		Endpoint:       s.addr(),
		CAFile:         mat.caFile,
		MetricInterval: time.Hour,
	}
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(sctx)
	}()

	p.Emitter().Counter("tailscale.wiretest.mtls_reject", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err == nil {
		t.Fatal("ForceFlush succeeded against an mTLS gRPC server with no client certificate configured — the handshake should have failed")
	}
	if got := len(s.rec.all()); got != 0 {
		t.Errorf("server captured %d requests from a client with no cert; want 0 (the TLS handshake must fail before any RPC)", got)
	}
}

// TestWireGRPC_NonOKResponseIsDeliveryFailure is the gRPC analog of the HTTP
// 404 test: a non-OK status must be a hard export failure.
func TestWireGRPC_NonOKResponseIsDeliveryFailure(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	s.setCode(codes.Unauthenticated)

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "grpc",
		Endpoint:       s.addr(),
		Insecure:       true,
		MetricInterval: time.Hour,
	}
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(sctx)
	}()

	p.Emitter().Counter("tailscale.wiretest.counter_grpc_err", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err == nil {
		t.Fatal("ForceFlush against a server returning codes.Unauthenticated returned a nil error")
	}

	assertMetricsDeliveryFailure(t, p, true)
}

// TestWireGRPC_PartialSuccessSurfacesAsExportFailure mirrors
// TestWireHTTP_PartialSuccessSurfacesAsExportFailure — see that test's doc
// comment for the finding this documents.
func TestWireGRPC_PartialSuccessSurfacesAsExportFailure(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	s.setPartialSuccess(1, "wiretest partial rejection")

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "grpc",
		Endpoint:       s.addr(),
		Insecure:       true,
		MetricInterval: time.Hour,
	}
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(sctx)
	}()

	p.Emitter().Counter("tailscale.wiretest.partial_success_grpc", "1", "", 1, nil)
	err = p.ForceFlush(ctx)
	if got := len(s.rec.all()); got == 0 {
		t.Fatal("server never received the request")
	}
	if err == nil {
		t.Fatal("ForceFlush with a partial_success response returned a nil error — expected the otlpmetricgrpc client to surface it (see the HTTP test's finding)")
	}
	// Currently classified as a delivery failure — see the HTTP test.
	assertMetricsPartialSuccess(t, p)
}

// TestWireGRPC_ResourceServiceVersionSplit mirrors
// TestWireHTTP_ResourceServiceVersionSplit — the #187 contract must hold
// identically regardless of transport.
func TestWireGRPC_ResourceServiceVersionSplit(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), insecureGRPC)

	assertServiceVersion(t, "metrics", metricResourceAttrs(got["metrics"].metrics), false, wiretestServiceVersion)
	assertServiceVersion(t, "logs", logResourceAttrs(got["logs"].logs), true, wiretestServiceVersion)
	assertServiceVersion(t, "traces", traceResourceAttrs(got["traces"].traces), true, wiretestServiceVersion)
}

// TestWireGRPC_ConstAttrsOnItemsNotResource mirrors
// TestWireHTTP_ConstAttrsOnItemsNotResource.
func TestWireGRPC_ConstAttrsOnItemsNotResource(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), insecureGRPC)

	metricAttrs, _ := metricDataPointAttrs(got["metrics"].metrics)
	assertConstAttrsOnItems(t, "metric data points", metricAttrs, wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, metricResourceAttrs(got["metrics"].metrics))

	assertConstAttrsOnItems(t, "log records", logRecordAttrs(got["logs"].logs), wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, logResourceAttrs(got["logs"].logs))

	assertConstAttrsOnItems(t, "spans", spanAttrs(got["traces"].traces), wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, traceResourceAttrs(got["traces"].traces))
}

// TestWireGRPC_CumulativeTemporalityPinned mirrors
// TestWireHTTP_CumulativeTemporalityPinned.
func TestWireGRPC_CumulativeTemporalityPinned(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), insecureGRPC)

	_, temporalities := metricDataPointAttrs(got["metrics"].metrics)
	assertCumulativeTemporality(t, temporalities)
}

func TestWireGRPC_DeltaTemporalityConfigured(t *testing.T) {
	s := newWireGRPCServer(t, nil)
	got := driveWirePipeline(t, s.rec, "grpc", s.addr(), func(o *telemetry.Options) {
		o.Insecure = true
		o.MetricTemporality = "delta"
	})

	assertDeltaTemporality(t, s.rec.all())
	assertWireTestGauge(t, got["metrics"].metrics)
}
