package flowlog_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestProcess_ReporterTrustAndFieldCompleteness(t *testing.T) {
	cache := enrich.NewDeviceCache()
	cache.Replace([]enrich.DeviceMeta{{
		NodeID:   "reporter",
		Hostname: "reporter",
		Tags:     []string{"tag:trusted"},
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.9")},
	}})

	for _, mode := range []string{"per_connection", "per_record"} {
		t.Run(mode, func(t *testing.T) {
			store := &fakeStore{}
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(cache, flowlog.Options{
				LogMode:             mode,
				Store:               store,
				TrustedReporterTags: []string{"tag:trusted"},
			})
			p.Process(flowlog.FlowLog{
				NodeID: "reporter",
				// The embedded source identity is written by the reporter and
				// therefore must not confer reporter trust even when it carries
				// the configured trusted tag.
				SrcNode: &flowlog.NodeRef{NodeID: "claimed-source", Tags: []string{"tag:trusted"}},
				VirtualTraffic: []flowlog.ConnectionCounts{{
					Proto: protoTCP, Src: "100.64.0.1:12345", Dst: "100.64.0.2:443", TxBytes: 1,
				}},
			}, rec.Emitter())

			points := rec.MetricPoints(flowlog.MetricReporterObservations)
			if len(points) != 1 {
				t.Fatalf("reporter observations = %d, want 1", len(points))
			}
			if got := points[0].Attrs["trust"]; got != "tagged" {
				t.Errorf("reporter trust = %q, want tagged", got)
			}
			if got := points[0].Attrs["consistency"]; got != "mismatch" {
				t.Errorf("reporter consistency = %q, want mismatch", got)
			}
			if _, ok := points[0].Attrs["tailscale.node.id"]; ok {
				t.Errorf("reporter metric unexpectedly carries raw node ID: %+v", points[0].Attrs)
			}

			field := rec.MetricPoints(flowlog.MetricFieldObservations)
			if len(field) != 5 {
				t.Fatalf("field observations = %d, want one point per field class: %+v", len(field), field)
			}
			for _, class := range []string{"source", "destination", "protocol", "source_port", "destination_port"} {
				point := findPoint(t, field, map[string]string{"field_class": class})
				if got := point.Attrs["state"]; got != "present" {
					t.Errorf("%s state = %q, want present", class, got)
				}
			}

			logs := rec.LogRecords()
			if len(logs) != 1 {
				t.Fatalf("logs = %d, want 1", len(logs))
			}
			if got := logs[0].Attrs["tailscale.reporter.trust"]; got != "tagged" {
				t.Errorf("log reporter trust = %q, want tagged", got)
			}
			if got := logs[0].Attrs["tailscale.reporter.consistency"]; got != "mismatch" {
				t.Errorf("log reporter consistency = %q, want mismatch", got)
			}

			if len(store.got) != 1 {
				t.Fatalf("store observations = %d, want 1", len(store.got))
			}
			got := store.got[0]
			if got.ReporterNodeID != "reporter" || got.ReporterTrust != "tagged" || got.ReporterConsistency != "mismatch" {
				t.Errorf("store reporter diagnosis = %+v", got)
			}
		})
	}
}

func TestProcess_FieldCompletenessRecordsExitOmissions(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{})
	p.Process(flowlog.FlowLog{ExitTraffic: []flowlog.ConnectionCounts{{
		Proto: protoTCP, TxBytes: 1,
	}}}, rec.Emitter())

	field := rec.MetricPoints(flowlog.MetricFieldObservations)
	for _, class := range []string{"source", "destination", "source_port", "destination_port"} {
		point := findPoint(t, field, map[string]string{"field_class": class})
		if got := point.Attrs["state"]; got != "missing" {
			t.Errorf("exit %s state = %q, want missing", class, got)
		}
	}
	if point := findPoint(t, field, map[string]string{"field_class": "protocol"}); point.Attrs["state"] != "present" {
		t.Errorf("exit protocol state = %q, want present", point.Attrs["state"])
	}
}

func TestProcess_FieldCompletenessDistinguishesPresentZeroProtocolFromOmitted(t *testing.T) {
	var flow flowlog.FlowLog
	if err := json.Unmarshal([]byte(`{
		"virtualTraffic": [
			{"proto": 0, "txBytes": 1},
			{"txBytes": 1}
		]
	}`), &flow); err != nil {
		t.Fatalf("decode flow: %v", err)
	}

	rec := telemetrytest.New()
	flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}).Process(flow, rec.Emitter())
	points := rec.MetricPoints(flowlog.MetricFieldObservations)
	present := findPoint(t, points, map[string]string{"field_class": "protocol", "state": "present"})
	missing := findPoint(t, points, map[string]string{"field_class": "protocol", "state": "missing"})
	if present.Value != 1 || missing.Value != 1 {
		t.Fatalf("protocol presence points = present %v, missing %v; want 1 each", present.Value, missing.Value)
	}
}

func TestProcess_ReporterTrustNeverUsesEmbeddedTags(t *testing.T) {
	cache := enrich.NewDeviceCache()
	cache.Replace([]enrich.DeviceMeta{{NodeID: "reporter", Tags: []string{"tag:ordinary"}}})
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cache, flowlog.Options{TrustedReporterTags: []string{"tag:trusted"}})
	p.Process(flowlog.FlowLog{
		NodeID: "reporter",
		SrcNode: &flowlog.NodeRef{
			NodeID: "claimed-source",
			// This tag is attacker-controlled: it must not turn the reporter
			// into a trusted source.
			Tags: []string{"tag:trusted"},
		},
	}, rec.Emitter())

	point := rec.MetricPoints(flowlog.MetricReporterObservations)[0]
	if got := point.Attrs["trust"]; got != "untrusted" {
		t.Errorf("embedded trusted tag produced trust = %q, want untrusted", got)
	}
	if got := point.Attrs["consistency"]; got != "mismatch" {
		t.Errorf("consistency = %q, want mismatch", got)
	}
}

func TestProcess_ReporterTrustByNodeIDAndUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  flowlog.Options
		flow  flowlog.FlowLog
		trust string
		check string
	}{
		{
			name: "node ID allowlist", opts: flowlog.Options{TrustedReporterNodeIDs: []string{"reporter"}},
			flow: flowlog.FlowLog{NodeID: "reporter", SrcNode: &flowlog.NodeRef{NodeID: "reporter"}}, trust: "configured", check: "match",
		},
		{
			name: "no policy", opts: flowlog.Options{}, flow: flowlog.FlowLog{NodeID: "reporter"}, trust: "unconfigured", check: "missing_reference",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			flowlog.NewProcessor(enrich.NewDeviceCache(), tc.opts).Process(tc.flow, rec.Emitter())
			point := rec.MetricPoints(flowlog.MetricReporterObservations)[0]
			if got := point.Attrs["trust"]; got != tc.trust {
				t.Errorf("trust = %q, want %q", got, tc.trust)
			}
			if got := point.Attrs["consistency"]; got != tc.check {
				t.Errorf("consistency = %q, want %q", got, tc.check)
			}
		})
	}
}

// counterCallEmitter measures work at the emitter boundary while retaining the
// real SDK's aggregation, so batching must preserve both cost and values.
type counterCallEmitter struct {
	telemetry.Emitter
	calls map[string]int
}

func (e *counterCallEmitter) Counter(name, unit, desc string, value float64, attrs telemetry.Attrs) {
	e.calls[name]++
	e.Emitter.Counter(name, unit, desc, value, attrs)
}

func TestProcessAllBatchesFixedCardinalityCounters(t *testing.T) {
	rec := telemetrytest.New()
	e := &counterCallEmitter{Emitter: rec.Emitter(), calls: make(map[string]int)}
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "rollup", LogMode: "off"})
	conn := flowlog.ConnectionCounts{Proto: protoTCP, Src: "100.64.0.1:12345", Dst: "100.64.0.2:443", TxBytes: 1}
	resp := flowlog.NetworkResponse{Logs: make([]flowlog.FlowLog, 100)}
	for i := range resp.Logs {
		resp.Logs[i].VirtualTraffic = make([]flowlog.ConnectionCounts, 10)
		for j := range resp.Logs[i].VirtualTraffic {
			resp.Logs[i].VirtualTraffic[j] = conn
		}
	}
	p.ProcessAll(resp, e)
	if calls := e.calls[flowlog.MetricFlows] + e.calls[flowlog.MetricFieldObservations]; calls > 6 {
		t.Fatalf("fixed-cardinality calls = %d, want at most 6", calls)
	}
	for _, name := range []string{flowlog.MetricFlows, flowlog.MetricFieldObservations} {
		points := rec.MetricPoints(name)
		want := 1
		if name == flowlog.MetricFieldObservations {
			want = 5
		}
		if len(points) != want {
			t.Fatalf("%s points = %+v", name, points)
		}
		for _, point := range points {
			if point.Value != 1000 {
				t.Errorf("%s %+v = %v, want 1000", name, point.Attrs, point.Value)
			}
		}
	}
}

func TestProcessAllBatchesFixedCardinalityCountersMixedAndDedup(t *testing.T) {
	rec := telemetrytest.New()
	e := &counterCallEmitter{Emitter: rec.Emitter(), calls: make(map[string]int)}
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "rollup", LogMode: "off", Dedup: dedup.New(0)})
	full := flowlog.ConnectionCounts{Proto: protoTCP, Src: "100.64.0.1:12345", Dst: "100.64.0.2:443", TxBytes: 1}
	resp := flowlog.NetworkResponse{Logs: []flowlog.FlowLog{{VirtualTraffic: []flowlog.ConnectionCounts{full}, ExitTraffic: []flowlog.ConnectionCounts{{Proto: protoUDP}}}}}
	p.ProcessAll(resp, e)
	p.ProcessAll(resp, e)
	flows := rec.MetricPoints(flowlog.MetricFlows)
	if len(flows) != 2 {
		t.Fatalf("flow points = %+v", flows)
	}
	for _, point := range flows {
		if point.Value != 1 {
			t.Fatalf("flow %+v = %v, want 1", point.Attrs, point.Value)
		}
	}
	fields := rec.MetricPoints(flowlog.MetricFieldObservations)
	if len(fields) != 10 {
		t.Fatalf("field points = %+v", fields)
	}
	for _, point := range fields {
		wantState := "present"
		if point.Attrs[semconv.AttrTrafficType] == semconv.TrafficExit && point.Attrs["field_class"] != "protocol" {
			wantState = "missing"
		}
		if point.Value != 1 || point.Attrs["state"] != wantState {
			t.Errorf("field = %+v, want count 1 state %s", point, wantState)
		}
	}
	if e.calls[flowlog.MetricFlows] != 2 || e.calls[flowlog.MetricFieldObservations] != 10 {
		t.Fatalf("duplicate or zero tally emitted: %+v", e.calls)
	}
}
