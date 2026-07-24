package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

// The store bypasses the emitter, so it bypasses the emitter's PII redaction
// too. It must apply the same policy itself, or switching a category off would
// hide an identifier from the backend while leaving it on the admin page.
func TestProcess_StoreHonoursPIIFilter(t *testing.T) {
	tests := []struct {
		name  string
		cats  pii.Categories
		check func(*testing.T, flowstore.Observation)
	}{
		{
			name: "emails off drops the user, keeps everything else",
			cats: pii.Categories{pii.CatEmails: false},
			check: func(t *testing.T, o flowstore.Observation) {
				if o.DstUser != "" {
					t.Errorf("DstUser = %q, want empty with emails disabled", o.DstUser)
				}
				if o.DstNode == "" || o.DstOS == "" || o.SrcTags == "" {
					t.Errorf("non-email identity was collaterally dropped: %+v", o)
				}
			},
		},
		{
			name: "hostnames off drops the resolved node names",
			cats: pii.Categories{pii.CatHostnames: false},
			check: func(t *testing.T, o flowstore.Observation) {
				if o.SrcNode != "" || o.DstNode != "" {
					t.Errorf("nodes = %q/%q, want both empty with hostnames disabled", o.SrcNode, o.DstNode)
				}
				if o.DstUser == "" {
					t.Errorf("DstUser was collaterally dropped: %+v", o)
				}
			},
		},
		{
			name: "tailscale ips off drops the raw endpoints",
			cats: pii.Categories{pii.CatTailscaleIPs: false},
			check: func(t *testing.T, o flowstore.Observation) {
				if o.SrcAddr != "" || o.DstAddr != "" {
					t.Errorf("raw endpoints = %q/%q, want both empty", o.SrcAddr, o.DstAddr)
				}
				// The port is not the address and stays: it is what the ports table
				// ranks, and it identifies a service, not a device.
				if o.DstPort != "51820" {
					t.Errorf("DstPort = %q, want it retained", o.DstPort)
				}
			},
		},
		{
			name: "everything enabled leaves the observation intact",
			cats: pii.Categories{},
			check: func(t *testing.T, o flowstore.Observation) {
				if o.SrcNode == "" || o.DstUser == "" || o.SrcAddr == "" {
					t.Errorf("observation was redacted with no category disabled: %+v", o)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, fs := storeProcessor(t, flowlog.Options{StorePII: tc.cats})
			rec := telemetrytest.New()

			p.Process(decodeLiveRecord(t), rec.Emitter())

			if len(fs.got) != 1 {
				t.Fatalf("store received %d observations, want 1", len(fs.got))
			}
			tc.check(t, fs.got[0])
		})
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
