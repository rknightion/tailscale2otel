package release_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/release"
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

func TestRefresh_EmitsSpanOnSuccess(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"1.98.4"}`))
	}))
	defer srv.Close()

	f := release.NewFetcher("test", srv.URL, "ua/1", release.ParseTailscalePkgs,
		&http.Client{}, time.Hour, nil, release.WithTracer(tp.Tracer("test")))
	f.Refresh(context.Background())

	if v, ok := f.Latest(); !ok || v != "1.98.4" {
		t.Fatalf("Latest() = %q, %v; want 1.98.4, true", v, ok)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Name() != "release.check test" {
		t.Errorf("span name = %q, want %q", spans[0].Name(), "release.check test")
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
	if spans[0].Status().Code == codes.Error {
		t.Errorf("expected non-error span status on success, got %+v", spans[0].Status())
	}
}

func TestRefresh_EmitsSpanOnStatusFailure(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := release.NewFetcher("test", srv.URL, "ua/1", release.ParseTailscalePkgs,
		&http.Client{}, time.Hour, nil, release.WithTracer(tp.Tracer("test")))
	f.Refresh(context.Background())

	if _, ok := f.Latest(); ok {
		t.Fatal("Latest() ok=true, want false on a failed fetch")
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := spanAttrMap(spans[0].Attributes())
	if got := attrs[attribute.Key("http.response.status_code")].AsInt64(); got != 500 {
		t.Errorf("http.response.status_code = %d, want 500", got)
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on 500, got %+v", spans[0].Status())
	}
}

func TestRefresh_EmitsSpanOnTimeout(t *testing.T) {
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

	f := release.NewFetcher("test", srv.URL, "ua/1", release.ParseTailscalePkgs,
		&http.Client{}, time.Hour, nil, release.WithTracer(tp.Tracer("test")))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	f.Refresh(ctx)

	if _, ok := f.Latest(); ok {
		t.Fatal("Latest() ok=true, want false on a timed-out fetch")
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on timeout, got %+v", spans[0].Status())
	}
}

func TestRefresh_NilTracerDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"1.98.4"}`))
	}))
	defer srv.Close()

	f := release.NewFetcher("test", srv.URL, "ua/1", release.ParseTailscalePkgs, &http.Client{}, time.Hour, nil)
	f.Refresh(context.Background())
	if v, ok := f.Latest(); !ok || v != "1.98.4" {
		t.Fatalf("Latest() = %q, %v; want 1.98.4, true", v, ok)
	}
}
