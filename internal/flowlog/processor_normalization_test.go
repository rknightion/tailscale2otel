package flowlog_test

import (
	"slices"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestProcess_NetworkTypeUsesFirstParseableEndpoint(t *testing.T) {
	tests := []struct {
		name string
		src  string
		dst  string
		want string
	}{
		{name: "source IPv4", src: "100.64.0.1:443", dst: "[fd7a:115c:a1e0::2]:443", want: semconv.NetworkTypeIPv4},
		{name: "destination after malformed source", src: "not-an-endpoint", dst: "[fd7a:115c:a1e0::2]:443", want: semconv.NetworkTypeIPv6},
		{name: "IPv4 mapped IPv6 is IPv4", src: "[::ffff:100.64.0.1]:443", dst: "[fd7a:115c:a1e0::2]:443", want: semconv.NetworkTypeIPv4},
		{name: "neither endpoint parseable omits type", src: "not-an-endpoint", dst: "also-not-an-endpoint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{})
			p.Process(flowlog.FlowLog{VirtualTraffic: []flowlog.ConnectionCounts{{
				Proto: protoTCP, Src: tc.src, Dst: tc.dst, TxBytes: 1,
			}}}, rec.Emitter())

			logs := rec.LogRecords()
			if len(logs) != 1 {
				t.Fatalf("log records = %d, want 1", len(logs))
			}
			if got, ok := logs[0].Attrs[semconv.NetworkType]; got != tc.want || ok != (tc.want != "") {
				t.Errorf("network.type = %q, present=%v; want %q, present=%v", got, ok, tc.want, tc.want != "")
			}
		})
	}
}

func TestProcess_CanonicalizesFlowTagsAcrossAllConsumers(t *testing.T) {
	srcTags := []string{" tag:servers ", "", "tag:prod", "tag:servers", "  "}
	wantOriginalTags := slices.Clone(srcTags)
	flow := flowlog.FlowLog{
		SrcNode: &flowlog.NodeRef{
			NodeID: "src", Addresses: []string{"100.64.0.1"}, Tags: srcTags,
		},
		DstNodes: []flowlog.NodeRef{{
			NodeID: "dst", Addresses: []string{"100.64.0.2"},
		}},
		VirtualTraffic: []flowlog.ConnectionCounts{{
			Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "100.64.0.2:443", TxBytes: 1,
		}},
	}
	store := &fakeStore{}
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		IdentityDims: true,
		Store:        store,
		Policy:       policyStore(t, `{"grants":[{"src":["tag:servers"],"dst":["*"],"ip":["tcp:443"]}]}`),
	})
	p.Process(flow, rec.Emitter())

	const want = "tag:prod,tag:servers"
	if !slices.Equal(flow.SrcNode.Tags, wantOriginalTags) {
		t.Errorf("decoded source tags mutated to %q, want %q", flow.SrcNode.Tags, wantOriginalTags)
	}
	metric := findPoint(t, rec.MetricPoints(flowlog.MetricIO), map[string]string{
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if got := metric.Attrs[semconv.AttrSrcTags]; got != want {
		t.Errorf("metric source tags = %q, want %q", got, want)
	}
	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs[semconv.AttrSrcTags]; got != want {
		t.Errorf("log source tags = %q, want %q", got, want)
	}
	if len(store.got) != 1 {
		t.Fatalf("store observations = %d, want 1", len(store.got))
	}
	if got := store.got[0].SrcTags; got != want {
		t.Errorf("store source tags = %q, want %q", got, want)
	}
	if got := store.got[0].Verdict; got != "permitted" {
		t.Errorf("policy verdict = %q, want permitted", got)
	}
}
