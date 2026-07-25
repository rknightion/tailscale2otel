package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
)

// --- #473 (security:SEC-10) ---
//
// Span status descriptions are free text written by whatever failed. Before this
// fix the exporter only ran them through the attribute-value scrub, so disabling
// free_text_details removed the exception event that carried the error string
// while the identical string survived in the status description.

func freeTextOffExporter(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(
		newPIISpanExporter(exp, pii.Categories{pii.CatFreeTextDetails: false}),
	))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exp, tp
}

func TestPIISpanExporterRedactsFreeTextStatusDescription(t *testing.T) {
	cases := []struct {
		name    string
		marker  string
		desc    string
		recErr  error
		spanFor string
	}{
		{
			name:    "recovered panic",
			marker:  "index out of range",
			desc:    fmt.Sprintf("panic: %v", "runtime error: index out of range [3] with length 2"),
			spanFor: "scrape devices",
		},
		{
			name:    "collector failure",
			marker:  "api unavailable",
			desc:    "api unavailable",
			recErr:  errors.New("api unavailable"),
			spanFor: "scrape keys",
		},
		{
			name:    "wrapped error",
			marker:  "context deadline exceeded",
			desc:    "devices collector: fetch page 2: context deadline exceeded",
			recErr:  fmt.Errorf("devices collector: %w", fmt.Errorf("fetch page 2: %w", context.DeadlineExceeded)),
			spanFor: "scrape devices",
		},
		{
			name:    "http response error",
			marker:  "tailnet/example.com",
			desc:    "unexpected status 500 from /api/v2/tailnet/example.com/devices",
			recErr:  errors.New("unexpected status 500 from /api/v2/tailnet/example.com/devices"),
			spanFor: "api devices",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp, tp := freeTextOffExporter(t)
			_, span := tp.Tracer("test").Start(context.Background(), tc.spanFor)
			span.SetAttributes(attribute.String("tailscale.collector", "devices"))
			if tc.recErr != nil {
				span.RecordError(tc.recErr)
			}
			span.SetStatus(codes.Error, tc.desc)
			span.End()

			spans := exp.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			got := spans[0]
			if strings.Contains(got.Status.Description, tc.marker) {
				t.Errorf("free_text_details off: status description still carries %q: %q", tc.marker, got.Status.Description)
			}
			if got.Status.Code != codes.Error {
				t.Errorf("status code = %v, want Error (the failure must stay visible)", got.Status.Code)
			}
			for _, ev := range got.Events {
				for _, a := range ev.Attributes {
					if a.Key == "exception.message" {
						t.Errorf("free_text_details off: exception.message survived on event %q: %q", ev.Name, a.Value.AsString())
					}
					if strings.Contains(a.Value.AsString(), tc.marker) {
						t.Errorf("free_text_details off: event %q attribute %q still carries %q", ev.Name, a.Key, tc.marker)
					}
				}
			}
			// The bounded, code-defined attribute that names the failure class must
			// survive so the span is still diagnosable.
			var kept bool
			for _, a := range got.Attributes {
				if a.Key == "tailscale.collector" {
					kept = true
				}
			}
			if !kept {
				t.Error("bounded tailscale.collector attribute must survive the free-text policy")
			}
		})
	}
}

// A bounded, code-defined description names an error class and survives, so a
// deployment with free_text_details off can still tell a rejected webhook from a
// rate-limited API call.
func TestPIISpanExporterKeepsBoundedStatusDescription(t *testing.T) {
	for _, desc := range []string{"method not allowed", "corrupt batch", http.StatusText(http.StatusTooManyRequests)} {
		t.Run(desc, func(t *testing.T) {
			exp, tp := freeTextOffExporter(t)
			_, span := tp.Tracer("test").Start(context.Background(), "webhook.receive")
			span.SetStatus(codes.Error, desc)
			span.End()

			spans := exp.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			if spans[0].Status.Description != desc {
				t.Errorf("bounded status description = %q, want %q", spans[0].Status.Description, desc)
			}
		})
	}
}

// With the category enabled the description keeps its full text (subject to the
// SDK's own limits) — the fix must not degrade the default deployment.
func TestPIISpanExporterKeepsStatusDescriptionWhenFreeTextOn(t *testing.T) {
	const desc = "devices collector: fetch page 2: context deadline exceeded"
	for _, cats := range []pii.Categories{
		nil,
		{pii.CatHostnames: false}, // an unrelated category off: wrapper active, text kept
	} {
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(newPIISpanExporter(exp, cats)))
		_, span := tp.Tracer("test").Start(context.Background(), "scrape devices")
		span.SetStatus(codes.Error, desc)
		span.End()

		// GetSpans BEFORE Shutdown: tracetest.InMemoryExporter.Shutdown resets its
		// recorded batch.
		spans := exp.GetSpans()
		_ = tp.Shutdown(context.Background())
		if len(spans) != 1 {
			t.Fatalf("cats=%v: got %d spans, want 1", cats, len(spans))
		}
		if spans[0].Status.Description != desc {
			t.Errorf("cats=%v: status description = %q, want it unchanged", cats, spans[0].Status.Description)
		}
	}
}
