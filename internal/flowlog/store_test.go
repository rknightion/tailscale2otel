package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/dedup"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// fakeStore captures what the processor feeds the flow store, so the tests
// assert the observation the admin view will actually render rather than the
// processor's internals.
type fakeStore struct{ got []flowstore.Observation }

func (f *fakeStore) Record(o flowstore.Observation) { f.got = append(f.got, o) }

// storeProcessor builds a processor writing into a fresh fake store.
func storeProcessor(t *testing.T, opts flowlog.Options) (*flowlog.Processor, *fakeStore) {
	t.Helper()
	fs := &fakeStore{}
	opts.Store = fs
	return flowlog.NewProcessor(enrich.NewDeviceCache(), opts), fs
}

func TestProcess_FeedsStoreOnePerConnection(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	if len(fs.got) != 1 {
		t.Fatalf("store received %d observations, want 1 (one per connection): %+v", len(fs.got), fs.got)
	}
	o := fs.got[0]

	// The traffic HAPPENED at the window start; the store buckets on this, so
	// using the capture time here would misplace every point on the timeline.
	if want := time.Date(2026, 7, 24, 7, 0, 0, 0, time.UTC); !o.Time.Equal(want) {
		t.Errorf("Time = %v, want the window start %v", o.Time, want)
	}
	if o.TrafficType != "virtual" || o.Transport != "tcp" {
		t.Errorf("TrafficType/Transport = %q/%q, want virtual/tcp", o.TrafficType, o.Transport)
	}
	if o.SrcAddr != "100.64.0.1:443" || o.DstAddr != "100.64.0.2:51820" {
		t.Errorf("raw endpoints = %q -> %q", o.SrcAddr, o.DstAddr)
	}
	if o.SrcNode != "camden" || o.DstNode != "mbp16" {
		t.Errorf("nodes = %q -> %q, want camden -> mbp16 (self-enrichment)", o.SrcNode, o.DstNode)
	}
	if o.DstPort != "51820" {
		t.Errorf("DstPort = %q, want 51820", o.DstPort)
	}
	want := flowstore.Counts{TxBytes: 1000, RxBytes: 800, TxPkts: 10, RxPkts: 8, Flows: 1}
	if o.Counts != want {
		t.Errorf("Counts = %+v, want %+v", o.Counts, want)
	}
}

// Identity is what phases 4 onward render; it comes from the record's own node
// blocks, not the device cache.
func TestProcess_StoreCarriesEndpointIdentity(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	o := fs.got[0]
	if o.SrcTags != "tag:servers,tag:sshrecorder" {
		t.Errorf("SrcTags = %q", o.SrcTags)
	}
	if o.DstUser != "rob@example.com" {
		t.Errorf("DstUser = %q, want rob@example.com", o.DstUser)
	}
	if o.DstOS != "macOS" {
		t.Errorf("DstOS = %q, want macOS", o.DstOS)
	}
	// The source node block carries neither user nor os in the live shape; absent
	// must stay absent rather than becoming a placeholder label.
	if o.SrcUser != "" || o.SrcOS != "" {
		t.Errorf("SrcUser/SrcOS = %q/%q, want both empty", o.SrcUser, o.SrcOS)
	}
}

// The two paths out of one connection are governed by different rules, and this
// is where they part company (#241).
//
// pii_filter controls what this process EXPORTS: the emitter redacts, so a
// category switched off never reaches the backend. The flow store is local,
// in-memory, and readable only through the admin-authenticated /flows surface,
// so it keeps what the record carried — an operator who has narrowed what they
// send onward has not asked to be blinded to their own tailnet.
//
// One processor, one record, one emitter with every identifying category
// disabled: the emitted telemetry loses the identity, the observation keeps it.
func TestProcess_StoreShowsIdentityTheEmitterRedacts(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.NewWithPII(pii.Categories{
		pii.CatEmails:       false,
		pii.CatHostnames:    false,
		pii.CatTailscaleIPs: false,
	})

	p.Process(decodeLiveRecord(t), rec.Emitter())

	// The exported side, still filtered.
	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	for _, key := range []string{
		semconv.AttrDstUser, semconv.AttrSrcNode, semconv.AttrDstNode,
		semconv.SourceAddress, semconv.DestinationAddress,
	} {
		if got, ok := recs[0].Attrs[key]; ok {
			t.Errorf("emitted %s = %v, want it redacted out of exported telemetry", key, got)
		}
	}

	// The local side, in full.
	if len(fs.got) != 1 {
		t.Fatalf("store received %d observations, want 1", len(fs.got))
	}
	o := fs.got[0]
	if o.DstUser != "rob@example.com" {
		t.Errorf("DstUser = %q, want it shown to the admin in full", o.DstUser)
	}
	if o.SrcNode != "camden" || o.DstNode != "mbp16" {
		t.Errorf("nodes = %q/%q, want camden/mbp16", o.SrcNode, o.DstNode)
	}
	if o.SrcAddr != "100.64.0.1:443" || o.DstAddr != "100.64.0.2:51820" {
		t.Errorf("raw endpoints = %q/%q, want both kept", o.SrcAddr, o.DstAddr)
	}
	if o.SrcTags != "tag:servers,tag:sshrecorder" || o.DstOS != "macOS" {
		t.Errorf("tags/os = %q/%q, want both kept", o.SrcTags, o.DstOS)
	}
}

// Exit traffic carries no destination at all. The store must see that absence,
// not a fabricated endpoint — its topology graph draws an edge for every pair it
// holds, so a placeholder here becomes a visible lie on the page.
func TestProcess_StoreKeepsAbsentEndpointsAbsent(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID: "n1",
		ExitTraffic: []flowlog.ConnectionCounts{
			{Src: "100.64.0.1:0", TxBytes: 320, TxPkts: 4},
		},
	}, rec.Emitter())

	if len(fs.got) != 1 {
		t.Fatalf("store received %d observations, want 1", len(fs.got))
	}
	o := fs.got[0]
	if o.DstNode != "" || o.DstAddr != "" || o.DstPort != "" {
		t.Errorf("exit observation carries a destination it never had: %+v", o)
	}
	if o.Counts.TxBytes != 320 {
		t.Errorf("TxBytes = %d, want 320 — the traffic still counts", o.Counts.TxBytes)
	}
}

// Poll and stream share one processor. A connection suppressed as a duplicate
// must not be counted twice in the store either, or the page would disagree with
// the metrics for the same traffic.
func TestProcess_StoreSkipsDeduplicatedConnections(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{Dedup: dedup.New(0)})
	rec := telemetrytest.New()

	flow := decodeLiveRecord(t)
	p.Process(flow, rec.Emitter())
	p.Process(flow, rec.Emitter()) // same window, redelivered

	if len(fs.got) != 1 {
		t.Fatalf("store received %d observations, want 1 (the duplicate must be skipped)", len(fs.got))
	}
}

// The store is optional; the default configuration has none.
func TestProcess_NoStoreConfigured(t *testing.T) {
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter()) // must not panic

	if len(rec.MetricPoints(flowlog.MetricFlows)) == 0 {
		t.Error("no flow metrics emitted without a store configured")
	}
}
