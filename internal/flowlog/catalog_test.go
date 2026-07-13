package flowlog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the processor actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog().
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection", NodeDims: true})

	p.Process(flowlog.FlowLog{
		NodeID: "n1",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:1", Dst: "100.64.0.2:443", TxPkts: 3, TxBytes: 300, RxPkts: 2, RxBytes: 200},
		},
	}, rec.Emitter())

	declared := map[string]metricdoc.Metric{}
	for _, m := range flowlog.Catalog() {
		declared[m.Name] = m
	}

	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in flowlog.Catalog()", name)
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

	logDeclared := map[string]bool{}
	for _, le := range flowlog.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in flowlog.LogCatalog()", lr.EventName)
		}
	}
}

// TestCatalogMatchesEmittedRollup extends the drift guard to the rollup-mode
// families: driving a rollup-mode processor + FlushRollup must emit the declared
// *.rollup counters and unique gauges, each with a matching unit, instrument, and
// description.
func TestCatalogMatchesEmittedRollup(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(flowlog.FlowLog{
		NodeID: "n1",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:1", Dst: "100.64.0.2:443", TxPkts: 3, TxBytes: 300, RxPkts: 2, RxBytes: 200},
		},
	}, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	declared := map[string]metricdoc.Metric{}
	for _, m := range flowlog.Catalog() {
		declared[m.Name] = m
	}

	got := map[string]bool{}
	for _, name := range rec.MetricNames() {
		got[name] = true
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in flowlog.Catalog()", name)
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

	for _, name := range []string{
		flowlog.MetricIORollup, flowlog.MetricPacketsRollup,
		flowlog.MetricUniqueDstPeers, flowlog.MetricUniqueDstPorts,
	} {
		if !got[name] {
			t.Errorf("rollup mode did not emit declared metric %q", name)
		}
	}
}
