package stream_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/stream"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for the
// stream receiver's OWN gateway counters. Every tailscale.stream.* metric the
// receiver emits must be declared in Catalog() with a matching unit, instrument,
// and description (docs/metrics.md is generated from Catalog(), so this keeps the
// generated docs honest).
//
// Scoping: this is a GATEWAY package — accepted records are routed to the shared
// flowlog.Processor and audit.Processor, which emit tailscale.network.* /
// tailscale.config.audit.* metrics and audit log records that are NOT this
// package's own. So (a) only tailscale.stream.* metrics are treated as this
// package's responsibility (downstream processor metrics are cataloged in their
// own packages), and (b) emitted log records (audit logs are downstream) are not
// asserted here. LogCatalog() is empty for this receiver.
func TestCatalogMatchesEmitted(t *testing.T) {
	// Drive every declared metric to be emitted at least once.
	//
	// (1) A valid authed body with one flow + one audit record ->
	//     records{type=flow} + records{type=audit}.
	s, rec := newServer(t, stream.Options{Token: testToken})
	body := captureFlowRecord + "\n" + captureAuditRecord + "\n"
	if w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(body)); w.Code != http.StatusOK {
		t.Fatalf("valid body status = %d, want 200; body=%q", w.Code, w.Body.String())
	}

	// (2) Missing/wrong auth -> rejected{reason=auth}. Reuse the same recorder so
	//     all emissions accumulate together.
	wrongAuth := http.Header{}
	wrongAuth.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:wrong-token")))
	if w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", wrongAuth, strings.NewReader(captureFlowRecord)); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-auth status = %d, want 401", w.Code)
	}

	// (3) Authed garbage JSON -> rejected{reason=unparsable}.
	if w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader("this is not json at all <<<>>>")); w.Code != http.StatusBadRequest {
		t.Fatalf("garbage-body status = %d, want 400", w.Code)
	}

	// (4) Oversized authed body on a tiny-MaxBodyBytes server -> rejected{reason=too_large}.
	sSmall, recSmall := newServer(t, stream.Options{Token: testToken, MaxBodyBytes: 8})
	if w := post(t, sSmall.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(captureFlowRecord)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized-body status = %d, want 413", w.Code)
	}

	// (5) A malformed-but-classifiable flow record -> decode_errors{type=flow} plus
	//     a rejected{reason=decode_error} request-level rejection. Under the #201
	//     atomic contract a known record that fails typed decoding rejects the whole
	//     batch (400), so this drives both counters at once. Reuses the first
	//     recorder so it accumulates with the rest.
	malformedFlow := `{"nodeId":"x","virtualTraffic":[{"proto":6}],"start":123}`
	if w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(malformedFlow)); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed-flow status = %d, want 400; body=%q", w.Code, w.Body.String())
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range stream.Catalog() {
		declared[m.Name] = m
	}

	check := func(rec *telemetrytest.Recorder) {
		for _, name := range rec.MetricNames() {
			// Only the receiver's own gateway counters are this package's
			// responsibility; downstream processor metrics are cataloged in
			// their own packages.
			if !strings.HasPrefix(name, "tailscale.stream.") {
				continue
			}
			pts := rec.MetricPoints(name)
			if len(pts) == 0 {
				continue
			}
			p0 := pts[0]
			d, ok := declared[name]
			if !ok {
				t.Errorf("emitted metric %q is not declared in stream.Catalog()", name)
				continue
			}
			if p0.Unit != d.Unit {
				t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
			}
			if p0.Description != d.Description {
				t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
			}
			wantCounter := d.Instrument == metricdoc.Counter
			gotCounter := p0.Kind == "sum" && p0.Monotonic
			if wantCounter != gotCounter {
				t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
			}
		}
	}
	check(rec)
	check(recSmall)

	// Both gateway counters must have been driven and declared.
	if len(rec.MetricPoints("tailscale.stream.records")) == 0 {
		t.Errorf("tailscale.stream.records was not emitted; the drive recipe is incomplete")
	}
	if len(rec.MetricPoints("tailscale.stream.rejected")) == 0 && len(recSmall.MetricPoints("tailscale.stream.rejected")) == 0 {
		t.Errorf("tailscale.stream.rejected was not emitted; the drive recipe is incomplete")
	}

	// Attribute-drift guard (#126): every emitted attribute must be declared.
	telemetrytest.AssertCatalogAttrs(t, rec, stream.Catalog(), stream.LogCatalog())
	telemetrytest.AssertCatalogAttrs(t, recSmall, stream.Catalog(), stream.LogCatalog())
}
