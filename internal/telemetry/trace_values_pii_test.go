package telemetry

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

func TestPIISpanExporterScrubsSurvivingAttributeValues(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		cats                           pii.Categories
		tailnet, sensitive, key, value string
		drop                           bool
	}{
		{"configured tailnet", pii.Categories{pii.CatTailnetName: false}, "example.com", "example.com", "url.full", "https://api.tailscale.com/api/v2/tailnet/example.com/devices", false},
		{"tailnet IPv4", pii.Categories{pii.CatTailscaleIPs: false}, "", "100.64.0.7", "url.full", "http://100.64.0.7:5252", true},
		{"tailnet IPv6", pii.Categories{pii.CatTailscaleIPs: false}, "", "fd7a:115c:a1e0::7", "url.full", "http://[fd7a:115c:a1e0::7]:5252", true},
		{"dropped hostname", pii.Categories{pii.CatHostnames: false}, "", "synthetic-node", "host.name", "synthetic-node", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp, original := tracetest.NewInMemoryExporter(), tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(newPIISpanExporter(exp, tc.cats, tc.tailnet)), sdktrace.WithSyncer(original))
			t.Cleanup(func() {
				if err := tp.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			detail := "request " + tc.value + " failed"
			_, span := tp.Tracer("test").Start(context.Background(), "synthetic", trace.WithLinks(trace.Link{Attributes: []attribute.KeyValue{attribute.String("detail", detail)}}))
			span.SetAttributes(attribute.String(tc.key, tc.value), attribute.String("detail", detail), attribute.StringSlice("details", []string{"safe", detail}))
			span.SetStatus(codes.Error, detail)
			span.AddEvent("retry", trace.WithAttributes(attribute.String("detail", detail), attribute.StringSlice("details", []string{detail})))
			span.End()
			got := exp.GetSpans()[0]
			check := func(where string, kvs []attribute.KeyValue) {
				t.Helper()
				for _, kv := range kvs {
					if strings.Contains(kv.Value.String(), tc.sensitive) {
						t.Errorf("%s %s leaks disabled value", where, kv.Key)
					}
				}
			}
			check("span", got.Attributes)
			for _, ev := range got.Events {
				check("event", ev.Attributes)
			}
			for _, link := range got.Links {
				check("link", link.Attributes)
			}
			if strings.Contains(got.Status.Description, tc.sensitive) {
				t.Error("status leaks disabled value")
			}
			attrs := spanAttrMap(t, got)
			_, exists := attrs[tc.key]
			if exists == tc.drop {
				t.Errorf("key present=%v drop=%v", exists, tc.drop)
			}
			if !strings.Contains(attrs["detail"].AsString(), "[redacted]") {
				t.Error("surviving value lacks redaction placeholder")
			}
			raw := original.GetSpans()[0]
			if spanAttrMap(t, raw)[tc.key].AsString() != tc.value || raw.Status.Description != detail {
				t.Error("mutated original SDK span")
			}
		})
	}
}

func TestPIISpanExporterTailnetAliasAndDefault(t *testing.T) {
	for _, name := range []string{"", "-"} {
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(newPIISpanExporter(exp, pii.Categories{pii.CatTailnetName: false}, name)))
		_, span := tp.Tracer("test").Start(context.Background(), "synthetic")
		span.SetAttributes(attribute.String("url.full", "https://example.com/a-b"))
		span.End()
		if got := spanAttrMap(t, exp.GetSpans()[0])["url.full"].AsString(); got != "https://example.com/a-b" {
			t.Errorf("alias %q altered URL", name)
		}
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	exp := tracetest.NewInMemoryExporter()
	if got := newPIISpanExporter(exp, nil, "example.com"); got != exp {
		t.Error("default exporter must remain unwrapped")
	}
}
