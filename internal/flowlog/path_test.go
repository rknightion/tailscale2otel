package flowlog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// physicalRecord builds a record whose single physicalTraffic entry reports one
// peer reached over one underlay endpoint. That is the shape of the real data:
// src is the PEER's overlay address, dst is the endpoint it was reached at.
func physicalRecord(dst string) flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: "nReporter",
		DstNodes: []flowlog.NodeRef{
			{NodeID: "nPeer", Name: "gfmbp.example.ts.net", Addresses: []string{"100.119.137.81"}},
		},
		PhysicalTraffic: []flowlog.ConnectionCounts{
			{Src: "100.119.137.81:0", Dst: dst, TxBytes: 148, TxPkts: 1},
		},
	}
}

// The path a peer was reached over is readable from the underlay endpoint alone:
// 127.3.3.40 is Tailscale's loopback marker for a relayed path, and what sits in
// the port field there is the DERP region ID, not a port. Every other physical
// destination is a real endpoint the two nodes reached directly.
func TestProcess_ClassifiesUnderlayPath(t *testing.T) {
	tests := []struct {
		name       string
		dst        string
		wantPath   string
		wantRegion string
	}{
		{
			name:       "derp marker carries the region in the port field",
			dst:        "127.3.3.40:8",
			wantPath:   flowstore.PathDERP,
			wantRegion: "8",
		},
		{
			name:     "a LAN endpoint is a direct IPv4 path",
			dst:      "10.0.0.5:59879",
			wantPath: flowstore.PathDirectIPv4,
		},
		{
			name:     "a public endpoint is a direct IPv4 path too",
			dst:      "81.187.237.31:41641",
			wantPath: flowstore.PathDirectIPv4,
		},
		{
			name:     "an IPv6 endpoint is a direct IPv6 path",
			dst:      "[2001:8b0:1f05::100e]:41641",
			wantPath: flowstore.PathDirectIPv6,
		},
		{
			// The marker is what identifies a relayed path; an unreadable region
			// must not demote it to direct, which would understate relaying.
			name:     "the marker without a readable region is still relayed",
			dst:      "127.3.3.40:",
			wantPath: flowstore.PathDERP,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, fs := storeProcessor(t, flowlog.Options{})
			rec := telemetrytest.New()

			p.Process(physicalRecord(tc.dst), rec.Emitter())

			o := only(t, fs)
			if o.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", o.Path, tc.wantPath)
			}
			if o.DERPRegion != tc.wantRegion {
				t.Errorf("DERPRegion = %q, want %q", o.DERPRegion, tc.wantRegion)
			}
		})
	}
}

// Only physicalTraffic describes an underlay path. The overlay traffic types are
// what the tailnet carried, not how it was carried, so classifying them would
// invent a path for every connection on the page.
func TestProcess_OnlyPhysicalTrafficCarriesAPath(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	o := only(t, fs)
	if o.TrafficType == "physical" {
		t.Fatal("fixture changed: this record is meant to be overlay traffic")
	}
	if o.Path != "" || o.DERPRegion != "" {
		t.Errorf("Path/DERPRegion = %q/%q on %s traffic, want both empty",
			o.Path, o.DERPRegion, o.TrafficType)
	}
}

// A physical entry with no destination cannot say how the peer was reached.
// Guessing would put it in the direct column and quietly overstate direct
// connectivity, which is the number an operator would act on.
func TestProcess_AbsentUnderlayEndpointHasNoPath(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID: "nReporter",
		PhysicalTraffic: []flowlog.ConnectionCounts{
			{Src: "100.119.137.81:0", TxBytes: 148, TxPkts: 1},
		},
	}, rec.Emitter())

	if o := only(t, fs); o.Path != "" {
		t.Errorf("Path = %q with no underlay endpoint, want empty", o.Path)
	}
}
