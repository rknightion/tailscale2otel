package telemetry

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

// apiSpanAttrs mirrors the attribute set internal/tsapi's retryTransport.observe
// records on every API span (transport.go). Keeping the fixture identical to the
// production call site is what makes these tests a regression guard for #212.
func apiSpanAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tailscale.endpoint", "devices"),
		attribute.String("url.full", "https://api.tailscale.com/api/v2/tailnet/example.com/devices"),
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "api.tailscale.com"),
		attribute.Int("http.request.resend_count", 0),
		attribute.Int64("tailscale.rate_limit.wait_ms", 12),
		attribute.Int("http.response.status_code", 200),
	}
}

// spanAttrMap flattens an exported span's attributes for assertions.
func spanAttrMap(t *testing.T, s tracetest.SpanStub) map[string]attribute.Value {
	t.Helper()
	m := make(map[string]attribute.Value, len(s.Attributes))
	for _, a := range s.Attributes {
		m[string(a.Key)] = a.Value
	}
	return m
}

func TestPIISpanExporterDropsDisabledEndpointPaths(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(
		newPIISpanExporter(exp, pii.Categories{pii.CatEndpointPaths: false}),
	))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "api devices")
	span.SetAttributes(apiSpanAttrs()...)
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := spanAttrMap(t, spans[0])
	for _, k := range []string{"url.full", "tailscale.endpoint"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("endpoint_paths=false: span attribute %q must be dropped", k)
		}
	}
	// Non-endpoint attributes must survive: the filter is category-scoped, not a
	// blanket strip of the HTTP attributes.
	for _, k := range []string{"http.request.method", "server.address", "http.request.resend_count", "tailscale.rate_limit.wait_ms", "http.response.status_code"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("endpoint_paths=false: span attribute %q must be kept", k)
		}
	}
}

func TestPIISpanExporterAllCategoriesEnabledKeepsEverything(t *testing.T) {
	for _, tc := range []struct {
		name string
		cats pii.Categories
	}{
		{name: "nil map", cats: nil},
		{name: "all true", cats: func() pii.Categories {
			c := pii.Categories{}
			for _, cat := range pii.AllCategories {
				c[cat] = true
			}
			return c
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(newPIISpanExporter(exp, tc.cats)))
			defer func() { _ = tp.Shutdown(context.Background()) }()

			want := apiSpanAttrs()
			_, span := tp.Tracer("test").Start(context.Background(), "api devices")
			span.SetAttributes(want...)
			span.End()

			spans := exp.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			attrs := spanAttrMap(t, spans[0])
			if len(attrs) != len(want) {
				t.Fatalf("got %d attributes, want %d: %v", len(attrs), len(want), spans[0].Attributes)
			}
			for _, kv := range want {
				got, ok := attrs[string(kv.Key)]
				if !ok {
					t.Errorf("attribute %q missing", kv.Key)
					continue
				}
				if got != kv.Value {
					t.Errorf("attribute %q = %v, want %v", kv.Key, got.AsInterface(), kv.Value.AsInterface())
				}
			}
		})
	}
}

// TestPIISpanExporterHonorsUnrelatedCategory proves the fix is the general PII
// contract on spans, not a url.full special case: a category that has nothing to
// do with endpoints (hostnames) filters its own span attribute.
func TestPIISpanExporterHonorsUnrelatedCategory(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(
		newPIISpanExporter(exp, pii.Categories{pii.CatHostnames: false}),
	))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "op")
	span.SetAttributes(
		attribute.String("host.name", "laptop-1"),
		attribute.String("url.full", "https://api.tailscale.com/api/v2/tailnet/example.com/devices"),
	)
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := spanAttrMap(t, spans[0])
	if _, ok := attrs["host.name"]; ok {
		t.Error("hostnames=false: host.name must be dropped from span attributes")
	}
	if _, ok := attrs["url.full"]; !ok {
		t.Error("hostnames=false: url.full must be kept (endpoint_paths still enabled)")
	}
}

// TestPIISpanExporterScrubsStatusAndEvents covers the error path: sanitizeTransportError
// embeds the request URL in the span status description, and RecordError copies it
// into the exception event's exception.message. Both must lose the redacted
// attribute's value, exactly like log bodies do (#197).
func TestPIISpanExporterScrubsStatusAndEvents(t *testing.T) {
	const url = "https://api.tailscale.com/api/v2/tailnet/example.com/devices"
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(
		newPIISpanExporter(exp, pii.Categories{pii.CatEndpointPaths: false}),
	))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	_, span := tp.Tracer("test").Start(context.Background(), "api devices")
	span.SetAttributes(apiSpanAttrs()...)
	span.RecordError(errors.New("Get " + url + ": context deadline exceeded"))
	span.AddEvent("retry", trace.WithAttributes(attribute.String("detail", "retrying "+url)))
	span.SetStatus(codes.Error, "Get "+url+": context deadline exceeded")
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if strings.Contains(spans[0].Status.Description, url) {
		t.Errorf("endpoint_paths=false: span status description still carries the URL: %q", spans[0].Status.Description)
	}
	for _, ev := range spans[0].Events {
		for _, a := range ev.Attributes {
			if strings.Contains(a.Value.AsString(), url) {
				t.Errorf("endpoint_paths=false: event %q attribute %q still carries the URL: %q", ev.Name, a.Key, a.Value.AsString())
			}
		}
	}
}

// TestNewProviderRegistersPIISpanExporter is the end-to-end proof that the filter
// is wired into the production pipeline (not merely constructible): a span
// recorded through Provider.Tracer must reach the stdout exporter without the
// disabled category's attributes.
func TestNewProviderRegistersPIISpanExporter(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	p, err := NewProvider(ctx, Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "stdout",
		StdoutWriter:   &buf,
		MetricInterval: time.Hour,
		TracingEnabled: true,
		TraceSampler:   "always_on",
		PIIFilter:      pii.Categories{pii.CatEndpointPaths: false},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	_, span := p.Tracer().Start(ctx, "api devices")
	span.SetAttributes(apiSpanAttrs()...)
	span.End()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "api devices") {
		t.Fatalf("stdout trace export missing the span: %s", out)
	}
	if strings.Contains(out, "url.full") || strings.Contains(out, "example.com") {
		t.Errorf("endpoint_paths=false: exported span still carries url.full: %s", out)
	}
	if !strings.Contains(out, "http.request.method") {
		t.Errorf("exported span lost a non-PII attribute: %s", out)
	}
}
