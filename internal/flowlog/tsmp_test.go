package flowlog_test

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// protoTSMP is the Tailscale Message Protocol — tailscaled's own "why that
// failed" messages, which it sends between nodes inside the WireGuard tunnel
// and never hands to the host network stack. Upstream picked 99, which IANA
// reserves for "any private encryption scheme"; in a Tailscale flow log it is
// always TSMP, because nothing else can put that number on the wire here.
const protoTSMP = 99

// protoUnassigned is a number IANA has not assigned. It must keep falling back
// to its decimal string: naming a protocol we cannot identify would be a claim,
// and the fallback is what keeps the label bounded to 256 possible values.
const protoUnassigned = 253

// A TSMP flow is the most diagnostic row on the page — it names the node that
// REJECTED something and the node whose traffic was rejected — and it is worth
// nothing if it reads as a bare number. This drives the record through the
// processor rather than calling transportName, so it pins the label an operator
// actually sees, on both the metric and the log.
func TestProcess_NamesTheProtocolOnEveryTransport(t *testing.T) {
	tests := []struct {
		proto int
		want  string
	}{
		{protoTCP, "tcp"},
		{protoUDP, "udp"},
		{1, "icmp"},
		{protoTSMP, "tsmp"},
		{protoUnassigned, fmt.Sprint(protoUnassigned)},
		// Physical entries report 0 on live data, which is "the record did not
		// say" rather than a protocol.
		{0, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{FlowMetricsMode: "all"})

			p.Process(flowlog.FlowLog{
				NodeID: "nReporter",
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: tc.proto, Src: "100.64.0.1:0", Dst: "100.64.0.2:0", TxBytes: 53, TxPkts: 1},
				},
			}, rec.Emitter())

			pts := rec.MetricPoints(flowlog.MetricIO)
			if len(pts) == 0 {
				t.Fatal("no io points emitted")
			}
			if got := pts[0].Attrs[semconv.NetworkTransport]; got != tc.want {
				t.Errorf("metric %s = %q, want %q", semconv.NetworkTransport, got, tc.want)
			}
			logs := rec.LogRecords()
			if len(logs) != 1 {
				t.Fatalf("log records = %d, want 1", len(logs))
			}
			if got := logs[0].Attrs[semconv.NetworkTransport]; got != tc.want {
				t.Errorf("log %s = %q, want %q", semconv.NetworkTransport, got, tc.want)
			}
		})
	}
}
