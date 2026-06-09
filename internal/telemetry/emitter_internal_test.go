package telemetry

import (
	"context"
	"slices"
	"sort"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newReaderEmitter builds an *otelEmitter wired to an in-memory ManualReader with
// the given reserved promoted-label set, so the collision guard can be asserted
// against the actually-recorded data points.
func newReaderEmitter(t *testing.T, reserved map[string]struct{}) (*otelEmitter, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return newOtelEmitter(mp.Meter("test"), noop.NewLoggerProvider().Logger("test"), nil, reserved, nil, nil, nil), reader
}

func collectAttrs(t *testing.T, reader *sdkmetric.ManualReader, name string) attribute.Set {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			switch d := m.Data.(type) {
			case metricdata.Sum[float64]:
				if len(d.DataPoints) != 1 {
					t.Fatalf("%s: %d data points, want 1", name, len(d.DataPoints))
				}
				return d.DataPoints[0].Attributes
			case metricdata.Gauge[float64]:
				if len(d.DataPoints) != 1 {
					t.Fatalf("%s: %d data points, want 1", name, len(d.DataPoints))
				}
				return d.DataPoints[0].Attributes
			}
		}
	}
	t.Fatalf("metric %q not found", name)
	return attribute.Set{}
}

// TestResolveLabelCollisions pins the pure key-resolution logic that decides which
// attribute keys survive when several fold onto the same Prometheus label.
func TestResolveLabelCollisions(t *testing.T) {
	cases := []struct {
		name     string
		attrs    Attrs
		reserved map[string]struct{}
		wantKeep []string // sorted; nil means "assert exactly one survivor"
	}{
		{
			name:     "no collision keeps all",
			attrs:    Attrs{"host.name": "h", "host.id": "i"},
			wantKeep: []string{"host.id", "host.name"},
		},
		{
			name:     "dotted identity wins over scraped underscore spelling",
			attrs:    Attrs{"tailscale.node": "real", "tailscale_node": "spoof"},
			wantKeep: []string{"tailscale.node"},
		},
		{
			name:     "reserved promoted label is dropped, resource wins",
			attrs:    Attrs{"instance": "scraped", "host.name": "h"},
			reserved: map[string]struct{}{"instance": {}},
			wantKeep: []string{"host.name"},
		},
		{
			name:     "three keys folding to one label keep exactly one",
			attrs:    Attrs{"a.b": "1", "a_b": "2", "a-b": "3"},
			wantKeep: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keep, _ := resolveLabelCollisions(c.attrs, c.reserved)
			if c.wantKeep == nil {
				if len(keep) != 1 {
					t.Fatalf("keep = %v, want exactly 1 surviving key", keep)
				}
				return
			}
			got := make([]string, 0, len(keep))
			for k := range keep {
				got = append(got, k)
			}
			sort.Strings(got)
			if !slices.Equal(got, c.wantKeep) {
				t.Fatalf("keep = %v, want %v", got, c.wantKeep)
			}
		})
	}
}

// TestEmitterDropsCollidingLabel proves the end-to-end guard: two attribute keys
// that normalize to the same Prometheus label (the node-metrics #16 class) emit a
// single label carrying the semantic identity's value, not a duplicate.
func TestEmitterDropsCollidingLabel(t *testing.T) {
	e, reader := newReaderEmitter(t, nil)
	e.Counter("tailscale.node.up", "1", "", 1, Attrs{
		"tailscale.node": "real-node",
		"tailscale_node": "scraped-spoof",
	})
	set := collectAttrs(t, reader, "tailscale.node.up")
	if set.Len() != 1 {
		t.Fatalf("attrs len = %d, want 1 (%s)", set.Len(), set.Encoded(attribute.DefaultEncoder()))
	}
	if v, ok := set.Value(attribute.Key("tailscale.node")); !ok || v.AsString() != "real-node" {
		t.Fatalf("kept attr = %q (ok=%v), want tailscale.node=real-node", v.AsString(), ok)
	}
	if _, ok := set.Value(attribute.Key("tailscale_node")); ok {
		t.Fatal("scraped tailscale_node should have been dropped")
	}
}

// TestEmitterDropsReservedPromotedLabel proves a data-point attribute that would
// collide with a promoted resource label (instance/job) is dropped so the sample
// is not rejected for a duplicate label.
func TestEmitterDropsReservedPromotedLabel(t *testing.T) {
	e, reader := newReaderEmitter(t, map[string]struct{}{"instance": {}})
	e.Gauge("tailscale.node.up", "1", "", 1, Attrs{
		"instance":  "scraped",
		"host.name": "h",
	})
	set := collectAttrs(t, reader, "tailscale.node.up")
	if _, ok := set.Value(attribute.Key("instance")); ok {
		t.Fatal("reserved 'instance' label should be dropped (resource wins)")
	}
	if v, ok := set.Value(attribute.Key("host.name")); !ok || v.AsString() != "h" {
		t.Fatalf("host.name should survive, got %q ok=%v", v.AsString(), ok)
	}
}

func TestBuildAttrsAppendsConstAttrs(t *testing.T) {
	ca := []attribute.KeyValue{
		attribute.String("tailscale.tailnet", "alpha"),
		attribute.String("tailscale2otel.provider", "tailscale"),
	}
	e := newOtelEmitter(nil, nil, nil, nil, nil, nil, ca)

	got := e.buildAttrs("m", Attrs{"k": "v"})
	if !hasAttr(got, "k", "v") || !hasAttr(got, "tailscale.tailnet", "alpha") || !hasAttr(got, "tailscale2otel.provider", "tailscale") {
		t.Errorf("buildAttrs with attrs missing keys: %v", got)
	}

	got = e.buildAttrs("m", Attrs{})
	if !hasAttr(got, "tailscale.tailnet", "alpha") {
		t.Errorf("buildAttrs with empty attrs dropped const attrs: %v", got)
	}

	e2 := newOtelEmitter(nil, nil, nil, nil, nil, nil, nil)
	if got := e2.buildAttrs("m", Attrs{}); got != nil {
		t.Errorf("buildAttrs nil-const empty-attrs = %v, want nil", got)
	}
}

func hasAttr(kvs []attribute.KeyValue, key, val string) bool {
	for _, kv := range kvs {
		if string(kv.Key) == key && kv.Value.AsString() == val {
			return true
		}
	}
	return false
}

func TestLogEventAppendsConstAttrs(t *testing.T) {
	rec := &sliceLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(rec)))
	defer func() { _ = lp.Shutdown(context.Background()) }()

	ca := []attribute.KeyValue{attribute.String("tailscale.tailnet", "alpha")}
	e := newOtelEmitter(nil, lp.Logger("test"), nil, nil, nil, nil, ca)
	e.LogEvent(Event{Name: "x", Body: "hi", Severity: SeverityInfo})

	if len(rec.records) != 1 {
		t.Fatalf("got %d records, want 1", len(rec.records))
	}
	found := false
	rec.records[0].WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key == "tailscale.tailnet" && kv.Value.AsString() == "alpha" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("log record missing tailscale.tailnet const attr")
	}
}

// sliceLogExporter is a minimal in-memory log.Exporter for assertions.
// Not safe for concurrent use — test helper only.
type sliceLogExporter struct{ records []sdklog.Record }

func (e *sliceLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.records = append(e.records, recs...)
	return nil
}
func (e *sliceLogExporter) Shutdown(context.Context) error   { return nil }
func (e *sliceLogExporter) ForceFlush(context.Context) error { return nil }
