package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
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

// TestCollisionSeenCapStopsLoggingAtCap verifies that:
//   - logCollisions logs normally for distinct (metric, key) pairs up to collisionSeenCap
//   - exactly one saturation warning is emitted once the cap is reached
//   - no new collisions are logged after the cap
//   - collisionSeen holds at most collisionSeenCap entries
func TestCollisionSeenCapStopsLoggingAtCap(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	e := &otelEmitter{diag: logger}

	// Drive logCollisions past the cap with distinct (metric, key) pairs.
	// Each call to logCollisions contributes one new distinct key.
	total := collisionSeenCap + 10
	for i := 0; i < total; i++ {
		metricName := fmt.Sprintf("m%d", i)
		e.logCollisions(metricName, []labelDrop{{key: "k", prom: "k", winner: ""}})
	}

	// Count how many entries are in collisionSeen.
	var mapLen int
	e.collisionSeen.Range(func(_, _ any) bool {
		mapLen++
		return true
	})
	if mapLen > collisionSeenCap {
		t.Errorf("collisionSeen has %d entries, want at most %d", mapLen, collisionSeenCap)
	}

	output := buf.String()

	// Exactly one saturation message must appear.
	satCount := strings.Count(output, "label-collision diagnostics saturated")
	if satCount != 1 {
		t.Errorf("saturation message count = %d, want 1; log output:\n%s", satCount, output)
	}

	// No collision logs should appear AFTER the saturation message.
	satIdx := strings.Index(output, "label-collision diagnostics saturated")
	afterSat := output[satIdx+len("label-collision diagnostics saturated"):]
	if strings.Contains(afterSat, "dropped colliding metric label") {
		t.Errorf("collision log appeared after saturation; output after saturation:\n%s", afterSat)
	}
}

// TestCollisionSeenCapAlreadyStoredKeysStillSuppressed verifies that entries
// inserted before the cap was hit continue to suppress duplicate logs (the
// already-stored fast path must not be broken by the cap).
func TestCollisionSeenCapAlreadyStoredKeysStillSuppressed(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	e := &otelEmitter{diag: logger}

	// Insert one entry well below cap.
	e.logCollisions("m", []labelDrop{{key: "k", prom: "k", winner: ""}})
	first := buf.String()
	if !strings.Contains(first, "dropped colliding metric label") {
		t.Fatal("first call must log the collision")
	}

	// Second call with the same (metric, key) must NOT log again.
	buf.Reset()
	e.logCollisions("m", []labelDrop{{key: "k", prom: "k", winner: ""}})
	if buf.Len() != 0 {
		t.Errorf("duplicate collision must be suppressed; got: %s", buf.String())
	}
}

// TestNormalizedEqual pins the allocation-free comparator that buildAttrs's
// fast path uses to detect label-name collisions without ever materializing a
// key's normalized (metricdoc.PromLabelName) form. It must agree with
// metricdoc.PromLabelName's rules exactly: dots/other punctuation fold to
// '_', a leading digit gets a '_' prefix, and already-normalized strings
// compare equal to themselves.
func TestNormalizedEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"host.name", "host.name", true},
		{"host.name", "host_name", true}, // dot vs underscore fold to the same label
		{"a.b", "a_b", true},             // both normalize to "a_b"
		{"a.b", "a-b", true},             // both normalize to "a_b"
		{"tailscale.node", "tailscale_node", true},
		{"host.name", "host.id", false},
		{"1abc", "_1abc", true}, // leading-digit protection matches an already-escaped form
		{"1abc", "1abd", false},
		{"", "", true},
		{"a", "b", false},
		{"instance", "instance", true},
	}
	for _, c := range cases {
		t.Run(c.a+"|"+c.b, func(t *testing.T) {
			got := normalizedEqual(c.a, c.b)
			if got != c.want {
				t.Errorf("normalizedEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
			// normalizedEqual must agree with the actual PromLabelName values in
			// both directions and be symmetric.
			want := metricdoc.PromLabelName(c.a) == metricdoc.PromLabelName(c.b)
			if got != want {
				t.Errorf("normalizedEqual(%q, %q) = %v, disagrees with PromLabelName equality %v", c.a, c.b, got, want)
			}
			if rev := normalizedEqual(c.b, c.a); rev != got {
				t.Errorf("normalizedEqual not symmetric: (%q,%q)=%v but (%q,%q)=%v", c.a, c.b, got, c.b, c.a, rev)
			}
		})
	}
}

// TestHasLabelCollision pins the no-allocation pre-check buildAttrs uses to
// decide whether the (allocating) resolveLabelCollisions path is needed at
// all.
func TestHasLabelCollision(t *testing.T) {
	cases := []struct {
		name     string
		attrs    Attrs
		reserved map[string]struct{}
		want     bool
	}{
		{
			name:  "no collision, no reserved",
			attrs: Attrs{"host.name": "h", "host.id": "i"},
			want:  false,
		},
		{
			name:  "empty attrs",
			attrs: Attrs{},
			want:  false,
		},
		{
			name:  "duplicate normalized keys",
			attrs: Attrs{"tailscale.node": "real", "tailscale_node": "spoof"},
			want:  true,
		},
		{
			name:     "reserved hit",
			attrs:    Attrs{"instance": "scraped", "host.name": "h"},
			reserved: map[string]struct{}{"instance": {}},
			want:     true,
		},
		{
			name:     "reserved present but no attr collides",
			attrs:    Attrs{"host.name": "h"},
			reserved: map[string]struct{}{"instance": {}, "job": {}},
			want:     false,
		},
		{
			name:  "no collision with several keys",
			attrs: Attrs{"a": 1, "b": 2, "c": 3, "d": 4},
			want:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasLabelCollision(c.attrs, c.reserved)
			if got != c.want {
				t.Errorf("hasLabelCollision(%v, %v) = %v, want %v", c.attrs, c.reserved, got, c.want)
			}
			// Must agree with what resolveLabelCollisions actually finds.
			_, drops := resolveLabelCollisions(c.attrs, c.reserved)
			if wantFromSlow := len(drops) > 0; got != wantFromSlow {
				t.Errorf("hasLabelCollision disagrees with resolveLabelCollisions: got %v, drops=%v", got, drops)
			}
		})
	}
}

// TestBuildAttrsFastPathIsAllocationFree proves the common no-collision case
// in buildAttrs takes the cheap branch: it allocates only the one output
// slice (no intermediate maps), regardless of how many attrs are supplied.
func TestBuildAttrsFastPathIsAllocationFree(t *testing.T) {
	e := newOtelEmitter(nil, nil, nil, nil, nil, nil, nil)
	attrs := Attrs{
		"host.name":            "device-1",
		"host.id":              "n123",
		"os.type":              "linux",
		"tailscale.tailnet":    "example.ts.net",
		"tailscale.node.id":    "nabc",
		"network.io.direction": "tx",
	}

	allocs := testing.AllocsPerRun(100, func() {
		got := e.buildAttrs("tailscale.device.online", attrs)
		if len(got) != len(attrs) {
			t.Fatalf("buildAttrs returned %d attrs, want %d", len(got), len(attrs))
		}
	})
	// The output slice itself is one allocation; there must be no additional
	// map allocations (resolveLabelCollisions' chosen/keep maps) in the
	// no-collision path.
	if allocs > 1 {
		t.Errorf("buildAttrs no-collision fast path allocated %.1f times per call, want <= 1", allocs)
	}
}

// BenchmarkBuildAttrs_NoCollision quantifies the fast path's allocation win
// for the common case (#86): a realistic per-device attrs map with no
// reserved-label collision.
func BenchmarkBuildAttrs_NoCollision(b *testing.B) {
	e := newOtelEmitter(nil, nil, nil, nil, nil, nil, nil)
	attrs := Attrs{
		"host.name":            "device-1",
		"host.id":              "n123",
		"os.type":              "linux",
		"tailscale.tailnet":    "example.ts.net",
		"tailscale.node.id":    "nabc",
		"network.io.direction": "tx",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.buildAttrs("tailscale.device.online", attrs)
	}
}

// BenchmarkBuildAttrs_WithCollision covers the (rarer) colliding case so the
// slow-path cost is visible alongside the fast path above.
func BenchmarkBuildAttrs_WithCollision(b *testing.B) {
	e := newOtelEmitter(nil, nil, nil, nil, nil, nil, nil)
	attrs := Attrs{
		"tailscale.node": "real-node",
		"tailscale_node": "scraped-spoof",
		"host.name":      "device-1",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.buildAttrs("tailscale.node.up", attrs)
	}
}
