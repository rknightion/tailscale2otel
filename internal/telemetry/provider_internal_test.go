package telemetry

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// TestTLSConfigPinsMinVersionTLS12 guards the OTLP exporter client TLS config
// against the semgrep missing-ssl-minversion finding: when CA/cert/key files
// configure a *tls.Config, it must floor the negotiated version at TLS 1.2 so a
// downgrade to TLS 1.0/1.1 is never possible. 1.2 (not 1.3) is the deliberate
// floor for proxy / self-signed interop.
func TestTLSConfigPinsMinVersionTLS12(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	cfg, err := tlsConfig(Options{CAFile: caFile})
	if err != nil {
		t.Fatalf("tlsConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("tlsConfig returned nil; expected a config when CAFile is set")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("cfg.MinVersion = %#x, want tls.VersionTLS12 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
}

// TestCumulativeTemporalitySelectorAlwaysCumulative pins the OTLP metric
// temporality. Grafana Cloud / Mimir OTLP ingestion accepts CUMULATIVE only
// (delta is rejected with HTTP 400 and there is no server-side delta->cumulative
// conversion), so the selector must return cumulative for EVERY instrument kind
// — a future SDK default change must never silently switch us to delta.
func TestCumulativeTemporalitySelectorAlwaysCumulative(t *testing.T) {
	kinds := []sdkmetric.InstrumentKind{
		sdkmetric.InstrumentKindCounter,
		sdkmetric.InstrumentKindUpDownCounter,
		sdkmetric.InstrumentKindHistogram,
		sdkmetric.InstrumentKindGauge,
		sdkmetric.InstrumentKindObservableCounter,
		sdkmetric.InstrumentKindObservableUpDownCounter,
		sdkmetric.InstrumentKindObservableGauge,
	}
	for _, k := range kinds {
		if got := cumulativeTemporalitySelector(k); got != metricdata.CumulativeTemporality {
			t.Errorf("cumulativeTemporalitySelector(%v) = %v, want CumulativeTemporality", k, got)
		}
	}
}

// TestBuildResourceEnrichesAndMergesCleanly is the regression guard for the
// resource enrichment: combining schemaless WithAttributes (service.*) with the
// core host/os/process detectors (all sharing one semconv schema URL) must NOT
// raise a schema-URL conflict, and the resulting resource must carry the
// instance identity plus host/os/process attributes used to distinguish
// instances in Grafana. A partial-resource error (e.g. a detector that can't
// read its source in CI) is tolerated; a hard error is not.
func TestBuildResourceEnrichesAndMergesCleanly(t *testing.T) {
	res, err := buildResource(context.Background(), Options{
		ServiceName:    "tailscale2otel",
		ServiceVersion: "test",
		InstanceID:     "inst-42",
	})
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		t.Fatalf("buildResource returned a non-partial error: %v", err)
	}
	if res == nil {
		t.Fatal("buildResource returned nil resource")
	}
	if res.SchemaURL() == "" {
		t.Error("resource SchemaURL is empty; expected the semconv schema URL from the detectors")
	}

	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.instance.id"] != "inst-42" {
		t.Errorf("service.instance.id = %q, want inst-42", got["service.instance.id"])
	}
	for _, key := range []string{
		"service.name",
		"host.name",
		"os.type",
		"process.pid",
		"process.executable.name",
		"process.runtime.name",
		"process.runtime.version",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("resource missing attribute %q (have: %v)", key, got)
		}
	}
}

// TestOTLPHTTPURL pins the OTLP/HTTP per-signal URL construction. The OTEL Go
// otlphttp exporter's WithEndpointURL uses the URL path AS-IS (it does not
// append /v1/<signal>), so a Grafana Cloud base endpoint like ".../otlp" must
// have the signal path appended or the gateway returns 404.
func TestOTLPHTTPURL(t *testing.T) {
	cases := []struct {
		base, signal, want string
	}{
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp", "metrics", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/metrics"},
		{"https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/", "logs", "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp/v1/logs"},
		{"https://x/otlp/v1/metrics", "metrics", "https://x/otlp/v1/metrics"}, // already signal-specific: no double-append
		{"http://collector:4318", "metrics", "http://collector:4318/v1/metrics"},
	}
	for _, c := range cases {
		if got := otlpHTTPURL(c.base, c.signal); got != c.want {
			t.Errorf("otlpHTTPURL(%q, %q) = %q, want %q", c.base, c.signal, got, c.want)
		}
	}
}
