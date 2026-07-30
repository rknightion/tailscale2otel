package telemetry_test

// Real OTLP/HTTP wire-contract integration tests (#357): an in-process
// httptest server decodes the actual protobuf ExportXServiceRequest bodies
// the SDK sends, rather than asserting against Go objects. See
// wirecontract_helpers_test.go for the shared server/driver/assertion
// plumbing (also used by wirecontract_grpc_test.go so the same assertions
// run against both transports).

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// TestWireHTTP_RequestPathsPerSignal guards otlpHTTPURL's whole reason for
// existing: a base gateway endpoint must get exactly one signal-specific path
// appended. #383 specifically flags /v1/traces as the currently
// under-documented one, so it is asserted here alongside the other two.
func TestWireHTTP_RequestPathsPerSignal(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, nil)

	wantPaths := map[string]string{
		"metrics": "/v1/metrics",
		"logs":    "/v1/logs",
		"traces":  "/v1/traces",
	}
	for signal, wantPath := range wantPaths {
		cr, ok := got[signal]
		if !ok {
			t.Fatalf("no %s request captured", signal)
		}
		if cr.path != wantPath {
			t.Errorf("%s request path = %q, want %q", signal, cr.path, wantPath)
		}
	}
}

// TestWireHTTP_AuthHeadersArriveIntact asserts configured headers reach the
// backend unmodified, on all three signals.
func TestWireHTTP_AuthHeadersArriveIntact(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, func(o *telemetry.Options) {
		o.Headers = map[string]string{
			"Authorization":   "Bearer wiretest-secret-token",
			"X-Custom-Header": "custom-value",
		}
	})
	for signal, cr := range got {
		if v := cr.header.Get("Authorization"); v != "Bearer wiretest-secret-token" {
			t.Errorf("%s Authorization header = %q, want the configured bearer token", signal, v)
		}
		if v := cr.header.Get("X-Custom-Header"); v != "custom-value" {
			t.Errorf("%s X-Custom-Header = %q, want custom-value", signal, v)
		}
	}
}

// TestWireHTTP_GzipCompression asserts the request arrives Content-Encoding:
// gzip and decodes correctly once un-gzipped (driveWirePipeline's decoding
// already un-gzips; a wrong Content-Encoding check would make every other
// HTTP test in this file fail to decode, which is exactly the regression this
// guards).
func TestWireHTTP_GzipCompression(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "gzip")
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, nil)
	for signal, cr := range got {
		if !cr.gzip {
			t.Errorf("%s request did not arrive Content-Encoding: gzip", signal)
		}
		if cr.metrics == nil && cr.logs == nil && cr.traces == nil {
			t.Errorf("%s request failed to decode after gunzip", signal)
		}
	}
}

// TestWireHTTP_TLSServerVerification exercises a real TLS handshake against a
// throwaway CA-signed server certificate (CAFile only — no client cert).
func TestWireHTTP_TLSServerVerification(t *testing.T) {
	mat := newWireTLSMaterial(t)
	s := newWireHTTPServer(t, &tls.Config{Certificates: []tls.Certificate{mat.serverCert}})
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, func(o *telemetry.Options) {
		o.CAFile = mat.caFile
	})
	if len(got) != 3 {
		t.Fatalf("got %d signals over TLS, want 3", len(got))
	}
}

// TestWireHTTP_MutualTLS exercises full mTLS: the server requires and
// verifies a client certificate signed by the same throwaway CA.
func TestWireHTTP_MutualTLS(t *testing.T) {
	mat := newWireTLSMaterial(t)
	s := newWireHTTPServer(t, &tls.Config{
		Certificates: []tls.Certificate{mat.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    mat.caPool,
	})
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, func(o *telemetry.Options) {
		o.CAFile = mat.caFile
		o.CertFile = mat.clientCertFile
		o.KeyFile = mat.clientKeyFile
	})
	if len(got) != 3 {
		t.Fatalf("got %d signals over mTLS, want 3", len(got))
	}
}

// TestWireHTTP_MutualTLSRejectsMissingClientCert proves the mTLS test above
// is not vacuous: a client with no certificate must fail the handshake and
// never reach the handler.
func TestWireHTTP_MutualTLSRejectsMissingClientCert(t *testing.T) {
	mat := newWireTLSMaterial(t)
	s := newWireHTTPServer(t, &tls.Config{
		Certificates: []tls.Certificate{mat.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    mat.caPool,
	})

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       s.ts.URL,
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
		t.Fatal("ForceFlush succeeded against an mTLS server with no client certificate configured — the handshake should have failed")
	}
	if got := len(s.rec.all()); got != 0 {
		t.Errorf("server captured %d requests from a client with no cert; want 0 (the TLS handshake must fail before the request layer)", got)
	}
}

// TestWireHTTP_NonOKResponseIsDeliveryFailure asserts a 404 is a hard export
// failure, not a successful exchange. #383 records that the troubleshooting
// docs currently claim otherwise for this exact status.
func TestWireHTTP_NonOKResponseIsDeliveryFailure(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	s.setStatus(http.StatusNotFound)

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       s.ts.URL,
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

	p.Emitter().Counter("tailscale.wiretest.counter404", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err == nil {
		t.Fatal("ForceFlush against a 404 endpoint returned a nil error — a 404 must be treated as a failed export, not a successful one")
	}

	assertMetricsDeliveryFailure(t, p, true)
}

// TestWireHTTP_PartialSuccessSurfacesAsExportFailure covers OTLP
// partial_success (200 OK, some items rejected).
//
// Two separate facts are asserted here, and conflating them is the trap this
// test exists to prevent.
//
// UPSTREAM: the pinned exporter returns a NON-NIL error for a partial success.
// otlpmetrichttp's client.go joins internal.MetricPartialSuccessError into
// UploadMetrics' return whenever the response carries a non-empty
// partial_success, so the request arrives, decodes correctly, the server
// returns 200 — and Export/ForceFlush still errors. That is upstream behavior
// this repo does not control, so it is pinned rather than fixed: if this
// assertion ever flips, the dependency changed.
//
// OURS: a partial success must NOT be accounted as a delivery failure. It is
// forward progress — some data got through — so deliveryTracker.observe routes
// it to PartialSuccesses and leaves Failures/ConsecutiveFailures alone (#359).
// Before that carve-out existed, a backend that accepted everything while
// attaching an advisory partial_success incremented ConsecutiveFailures, and
// three in a row flipped DeliveryState.Failing() on a healthy deployment.
func TestWireHTTP_PartialSuccessSurfacesAsExportFailure(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	s.setPartialSuccess(1, "wiretest partial rejection")

	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       s.ts.URL,
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

	p.Emitter().Counter("tailscale.wiretest.partial_success", "1", "", 1, nil)
	err = p.ForceFlush(ctx)
	if got := len(s.rec.all()); got == 0 {
		t.Fatal("server never received the request")
	}
	if err == nil {
		t.Fatal("ForceFlush with a partial_success response returned a nil error — if this now passes, the upstream SDK's partial_success handling changed; update this test's doc comment and the #359 note accordingly")
	}
	assertMetricsPartialSuccess(t, p)
}

// TestWireHTTP_ResourceServiceVersionSplit guards #187: metrics must never
// carry service.version on the Resource (it would become a per-series label
// that churns on every redeploy), while logs and traces — which have no
// per-series label surface — must.
func TestWireHTTP_ResourceServiceVersionSplit(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, nil)

	assertServiceVersion(t, "metrics", metricResourceAttrs(got["metrics"].metrics), false, wiretestServiceVersion)
	assertServiceVersion(t, "logs", logResourceAttrs(got["logs"].logs), true, wiretestServiceVersion)
	assertServiceVersion(t, "traces", traceResourceAttrs(got["traces"].traces), true, wiretestServiceVersion)
}

// TestWireHTTP_ConstAttrsOnItemsNotResource guards roadmap item L: the
// tailnet name and control-plane provider are per-item attributes (data
// point / log record / span) on every signal, and never Resource attributes.
func TestWireHTTP_ConstAttrsOnItemsNotResource(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, nil)

	metricAttrs, _ := metricDataPointAttrs(got["metrics"].metrics)
	assertConstAttrsOnItems(t, "metric data points", metricAttrs, wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, metricResourceAttrs(got["metrics"].metrics))

	assertConstAttrsOnItems(t, "log records", logRecordAttrs(got["logs"].logs), wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, logResourceAttrs(got["logs"].logs))

	assertConstAttrsOnItems(t, "spans", spanAttrs(got["traces"].traces), wiretestTailnet, wiretestProvider)
	assertConstAttrsAbsentFromResource(t, traceResourceAttrs(got["traces"].traces))
}

// TestWireHTTP_CumulativeTemporalityPinned guards cumulativeTemporalitySelector:
// every Sum/Histogram metric must report CUMULATIVE regardless of instrument
// kind, since Grafana Cloud/Mimir rejects delta temporality outright.
func TestWireHTTP_CumulativeTemporalityPinned(t *testing.T) {
	s := newWireHTTPServer(t, nil)
	got := driveWirePipeline(t, s.rec, "http", s.ts.URL, nil)

	_, temporalities := metricDataPointAttrs(got["metrics"].metrics)
	assertCumulativeTemporality(t, temporalities)
}
