package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fiveConnFlow returns a single FlowLog whose VirtualTraffic holds five distinct
// connections (distinct destinations), so per_connection mode emits five flow
// log records and five sets of io metric points.
func fiveConnFlow() flowlog.FlowLog {
	dsts := []string{
		"100.64.0.2:51820",
		"100.64.0.3:51820",
		"100.64.0.4:51820",
		"100.64.0.5:51820",
		"100.64.0.6:51820",
	}
	conns := make([]flowlog.ConnectionCounts, 0, len(dsts))
	for _, d := range dsts {
		conns = append(conns, flowlog.ConnectionCounts{
			Proto: protoTCP, Src: "100.64.0.1:443", Dst: d,
			TxPkts: 1, TxBytes: 100, RxPkts: 1, RxBytes: 80,
		})
	}
	return flowlog.FlowLog{
		Logged:         time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID:         "nLaptop",
		Start:          time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		End:            time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC),
		VirtualTraffic: conns,
	}
}

// droppedTotal returns the summed value of every MetricLogsDropped data point.
func droppedTotal(rec *telemetrytest.Recorder) float64 {
	var total float64
	for _, p := range rec.MetricPoints(flowlog.MetricLogsDropped) {
		total += p.Value
	}
	return total
}

func TestProcessAllCapsLogsButNotMetrics(t *testing.T) {
	// cap=2 over a window of five per_connection connections: only two flow LOG
	// records are emitted, but io metric points are emitted for ALL five
	// connections (10 points: tx+rx each). The dropped counter == 3.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		LogMode:                "per_connection",
		MaxLogRecordsPerWindow: 2,
	})
	p.ProcessAll(flowlog.NetworkResponse{Logs: []flowlog.FlowLog{fiveConnFlow()}}, rec.Emitter())

	if logs := rec.LogRecords(); len(logs) != 2 {
		t.Fatalf("flow log records = %d, want 2 (cap)", len(logs))
	}

	// Metrics are never capped: all five connections (each 100B tx + 80B rx)
	// contribute to the aggregated io counter regardless of the log cap.
	if got := ioTotal(rec); got != 5*(100+80) {
		t.Fatalf("io total = %v, want %v (metrics never capped)", got, 5*(100+80))
	}
	// MetricFlows counts all five flows even though only two logs were emitted.
	if flows := rec.MetricPoints(flowlog.MetricFlows); len(flows) != 1 || flows[0].Value != 5 {
		t.Fatalf("MetricFlows = %+v, want one point value 5 (metrics never capped)", flows)
	}

	if got := droppedTotal(rec); got != 3 {
		t.Fatalf("logs_dropped = %v, want 3", got)
	}
	for _, p := range rec.MetricPoints(flowlog.MetricLogsDropped) {
		if p.Unit != "{record}" {
			t.Fatalf("logs_dropped unit = %q, want {record}", p.Unit)
		}
	}
}

func TestProcessAllUnlimitedWhenCapZero(t *testing.T) {
	// cap=0 (default): all five logs emitted and NO dropped counter series.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		LogMode:                "per_connection",
		MaxLogRecordsPerWindow: 0,
	})
	p.ProcessAll(flowlog.NetworkResponse{Logs: []flowlog.FlowLog{fiveConnFlow()}}, rec.Emitter())

	if logs := rec.LogRecords(); len(logs) != 5 {
		t.Fatalf("flow log records = %d, want 5 (unlimited)", len(logs))
	}
	if pts := rec.MetricPoints(flowlog.MetricLogsDropped); len(pts) != 0 {
		t.Fatalf("logs_dropped series present with cap=0: %+v", pts)
	}
}

func TestProcessAllCapsPerRecordSummaries(t *testing.T) {
	// per_record mode: each FlowLog emits one summary log. With cap=1 over two
	// records, only one summary is emitted and the second is dropped (==1).
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		LogMode:                "per_record",
		MaxLogRecordsPerWindow: 1,
	})
	flow2 := fiveConnFlow()
	flow2.Start = flow2.Start.Add(time.Minute)
	flow2.End = flow2.End.Add(time.Minute)
	resp := flowlog.NetworkResponse{Logs: []flowlog.FlowLog{fiveConnFlow(), flow2}}
	p.ProcessAll(resp, rec.Emitter())

	if logs := rec.LogRecords(); len(logs) != 1 {
		t.Fatalf("per_record summary logs = %d, want 1 (cap)", len(logs))
	}
	if got := droppedTotal(rec); got != 1 {
		t.Fatalf("logs_dropped = %v, want 1", got)
	}
	// Metrics never capped: two records of five connections each contribute to
	// the aggregated io counter (2 * 5 * (100 tx + 80 rx)).
	if got := ioTotal(rec); got != 2*5*(100+80) {
		t.Fatalf("io total = %v, want %v (metrics never capped)", got, 2*5*(100+80))
	}
}
