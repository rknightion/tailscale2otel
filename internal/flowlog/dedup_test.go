package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
)

func TestConnectionKeyIncludesTrafficTypeAndUTCWindow(t *testing.T) {
	start := time.Date(2026, 7, 26, 10, 11, 12, 123, time.FixedZone("BST", 3600))
	end := start.Add(time.Minute)
	got := flowlog.ConnectionKey(flowlog.FlowLog{
		NodeID: "node-1",
		Start:  start,
		End:    end,
	}, "virtual", flowlog.ConnectionCounts{
		Proto: 6,
		Src:   "100.64.0.1:1234",
		Dst:   "100.64.0.2:443",
	})
	want := "node-1|2026-07-26T09:11:12.000000123Z|2026-07-26T09:12:12.000000123Z|virtual|6|100.64.0.1:1234|100.64.0.2:443"
	if got != want {
		t.Fatalf("ConnectionKey() = %q, want %q", got, want)
	}
}

func TestConnectionKeyBoundsUnknownTrafficType(t *testing.T) {
	got := flowlog.ConnectionKey(flowlog.FlowLog{}, "untrusted", flowlog.ConnectionCounts{})
	if got != "|0001-01-01T00:00:00Z|0001-01-01T00:00:00Z|unknown|0||" {
		t.Fatalf("ConnectionKey() = %q, want bounded unknown traffic type", got)
	}
}
