package flowlog_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// ioTotal returns the sum of every recorded MetricIO data point's value.
func ioTotal(rec *telemetrytest.Recorder) float64 {
	var total float64
	for _, p := range rec.MetricPoints(flowlog.MetricIO) {
		total += p.Value
	}
	return total
}

// TASK A: dedup.

func TestProcessNilDedupProcessesDuplicates(t *testing.T) {
	// Default (nil) Dedup preserves today's behavior: two identical FlowLogs are
	// both fully processed, so the io byte counter total doubles.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})

	flow := virtualTCPFlow()
	p.Process(flow, rec.Emitter())
	single := ioTotal(rec)
	if single == 0 {
		t.Fatalf("io total after first Process = 0, want non-zero")
	}

	p.Process(flow, rec.Emitter())
	if got := ioTotal(rec); got != single*2 {
		t.Fatalf("io total after second Process = %v, want %v (doubled) with nil Dedup", got, single*2)
	}
}

func TestProcessSharedDedupSuppressesDuplicate(t *testing.T) {
	// A shared Dedup set suppresses the second identical FlowLog entirely: the io
	// total is unchanged after the 2nd Process.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{Dedup: dedup.New(0)})

	flow := virtualTCPFlow()
	p.Process(flow, rec.Emitter())
	single := ioTotal(rec)
	if single == 0 {
		t.Fatalf("io total after first Process = 0, want non-zero")
	}

	p.Process(flow, rec.Emitter())
	if got := ioTotal(rec); got != single {
		t.Fatalf("io total after duplicate Process = %v, want unchanged %v with shared Dedup", got, single)
	}
}

func TestProcessSharedDedupProcessesDistinctRecords(t *testing.T) {
	// Two DIFFERENT FlowLogs (different Start) are both processed even with a
	// shared Dedup set.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{Dedup: dedup.New(0)})

	flow := virtualTCPFlow()
	p.Process(flow, rec.Emitter())
	single := ioTotal(rec)
	if single == 0 {
		t.Fatalf("io total after first Process = 0, want non-zero")
	}

	other := virtualTCPFlow()
	other.Start = flow.Start.Add(time.Minute)
	p.Process(other, rec.Emitter())
	if got := ioTotal(rec); got != single*2 {
		t.Fatalf("io total after distinct Process = %v, want %v (both processed)", got, single*2)
	}
}

// TASK B: hostname enrichment of flow logs.

func TestProcessConnLogCarriesNodeHostname(t *testing.T) {
	// A populated cache whose NodeID matches the FlowLog's NodeID adds the
	// originating node's hostname to the per-connection flow log record.
	rec := telemetrytest.New()
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{
		{NodeID: "n-x", Hostname: "laptop", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	})
	p := flowlog.NewProcessor(c, flowlog.Options{LogMode: "per_connection"})

	flow := virtualTCPFlow()
	flow.NodeID = "n-x"
	p.Process(flow, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1 (%+v)", len(logs), logs)
	}
	if got := logs[0].Attrs["tailscale.node.hostname"]; got != "laptop" {
		t.Fatalf("tailscale.node.hostname = %q, want laptop", got)
	}
}

func TestProcessConnLogNoHostnameWithEmptyCache(t *testing.T) {
	// An empty cache misses, so the hostname attribute is ABSENT from the record.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection"})
	p.Process(virtualTCPFlow(), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1 (%+v)", len(logs), logs)
	}
	if _, ok := logs[0].Attrs["tailscale.node.hostname"]; ok {
		t.Fatalf("tailscale.node.hostname present with empty cache: %+v", logs[0].Attrs)
	}
}

func TestProcessRecordLogCarriesNodeHostname(t *testing.T) {
	// per_record mode also enriches with the originating node's hostname.
	rec := telemetrytest.New()
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{
		{NodeID: "n-x", Hostname: "laptop", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	})
	p := flowlog.NewProcessor(c, flowlog.Options{LogMode: "per_record"})

	flow := virtualTCPFlow()
	flow.NodeID = "n-x"
	p.Process(flow, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1 (%+v)", len(logs), logs)
	}
	if got := logs[0].Attrs["tailscale.node.hostname"]; got != "laptop" {
		t.Fatalf("tailscale.node.hostname = %q, want laptop", got)
	}
}

func TestProcessRecordLogNoHostnameWithEmptyCache(t *testing.T) {
	// per_record mode with an empty cache omits the hostname attribute.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_record"})
	p.Process(virtualTCPFlow(), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1 (%+v)", len(logs), logs)
	}
	if _, ok := logs[0].Attrs["tailscale.node.hostname"]; ok {
		t.Fatalf("tailscale.node.hostname present with empty cache: %+v", logs[0].Attrs)
	}
}
