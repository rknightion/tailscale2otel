package nodemetrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func spanAttrMap(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestScrape_EmitsSpanOnSuccess(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	body := "# TYPE node_load gauge\nnode_load 0.5\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
		Tracer:  tp.Tracer("test"),
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	// The span name is a single bounded class, never the per-target URL/host/
	// instance: nodemetrics scrapes an operator-controlled but dynamically
	// discoverable set of targets (see internal/collector/CLAUDE.md's
	// discovery notes), so anything target-specific in the NAME would make the
	// name cardinality unbounded. Per-target detail (instance, host) rides as
	// span ATTRIBUTES instead, exactly like tsapi's url.full attribute.
	if spans[0].Name() != "nodemetrics.scrape" {
		t.Errorf("span name = %q, want %q", spans[0].Name(), "nodemetrics.scrape")
	}
	if spans[0].SpanKind() != trace.SpanKindClient {
		t.Errorf("span kind = %v, want Client", spans[0].SpanKind())
	}
	attrs := spanAttrMap(spans[0].Attributes())
	if got := attrs[attribute.Key("http.request.method")].AsString(); got != "GET" {
		t.Errorf("http.request.method = %q, want GET", got)
	}
	if got := attrs[attribute.Key("http.response.status_code")].AsInt64(); got != 200 {
		t.Errorf("http.response.status_code = %d, want 200", got)
	}
	if got := attrs[attribute.Key("tailscale.node")].AsString(); got != "node-a" {
		t.Errorf("tailscale.node = %q, want node-a", got)
	}
	if spans[0].Status().Code == codes.Error {
		t.Errorf("expected non-error span status on success, got %+v", spans[0].Status())
	}
}

func TestScrape_EmitsSpanOnStatusFailure(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
		Tracer:  tp.Tracer("test"),
	})
	rec := telemetrytest.New()
	_ = c.Collect(context.Background(), rec.Emitter()) // all-targets-fail is a Collect-level error; span still recorded

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := spanAttrMap(spans[0].Attributes())
	if got := attrs[attribute.Key("http.response.status_code")].AsInt64(); got != 503 {
		t.Errorf("http.response.status_code = %d, want 503", got)
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on 503, got %+v", spans[0].Status())
	}
}

func TestScrape_EmitsSpanOnTimeout(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
		Timeout: 20 * time.Millisecond,
		Tracer:  tp.Tracer("test"),
	})
	rec := telemetrytest.New()
	_ = c.Collect(context.Background(), rec.Emitter())

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on timeout, got %+v", spans[0].Status())
	}
}

func TestScrape_NilTracerDoesNotPanic(t *testing.T) {
	body := "# TYPE node_load gauge\nnode_load 0.5\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
}
