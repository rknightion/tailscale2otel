package hsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// spanAttrMap indexes a span's attributes by key for value lookups.
func spanAttrMap(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestGetJSON_EmitsSpanOnSuccess(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	var got RequestInfo
	c := NewClient(Options{
		URL:     srv.URL,
		APIKey:  "secret",
		Timeout: 5 * time.Second,
		Tracer:  tp.Tracer("test"),
		OnRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	})
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("Nodes: %v", err)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Name() != "headscale.api node" {
		t.Errorf("span name = %q, want %q", spans[0].Name(), "headscale.api node")
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

	if got.Endpoint != "node" {
		t.Errorf("RequestInfo.Endpoint = %q, want %q", got.Endpoint, "node")
	}
	if got.Status != 200 {
		t.Errorf("RequestInfo.Status = %d, want 200", got.Status)
	}
	if got.Err != "" {
		t.Errorf("RequestInfo.Err = %q, want empty", got.Err)
	}
}

func TestGetJSON_EmitsSpanOnStatusFailure(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	var got RequestInfo
	c := NewClient(Options{
		URL:    srv.URL,
		APIKey: "x",
		Tracer: tp.Tracer("test"),
		OnRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	})
	if _, err := c.Nodes(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	attrs := spanAttrMap(spans[0].Attributes())
	if got := attrs[attribute.Key("http.response.status_code")].AsInt64(); got != 401 {
		t.Errorf("http.response.status_code = %d, want 401", got)
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on 401, got %+v", spans[0].Status())
	}
	if got.Status != 401 {
		t.Errorf("RequestInfo.Status = %d, want 401", got.Status)
	}
}

func TestGetJSON_EmitsSpanOnTimeout(t *testing.T) {
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

	var got RequestInfo
	c := NewClient(Options{
		URL:    srv.URL,
		APIKey: "x",
		Tracer: tp.Tracer("test"),
		OnRequest: func(_ context.Context, i RequestInfo) {
			got = i
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Nodes(ctx); err == nil {
		t.Fatal("expected timeout error")
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected error span status on timeout, got %+v", spans[0].Status())
	}
	if got.Err == "" {
		t.Error("RequestInfo.Err should be non-empty on timeout")
	}
	if got.Status != 0 {
		t.Errorf("RequestInfo.Status = %d, want 0 (no HTTP response)", got.Status)
	}
}

func TestGetJSON_NilTracerAndOnRequestDoNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "x", Timeout: 5 * time.Second})
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("Nodes: %v", err)
	}
}
