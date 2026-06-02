package flowlog_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/flowlog"
)

// Sample shaped per the Tailscale GET /tailnet/{tailnet}/logging/network
// response. The real API encodes proto as a NUMBER (6=tcp, 17=udp), and some
// entries omit rxPkts/rxBytes entirely (one-directional physical traffic).
const networkBody = `{
  "logs": [
    {
      "logged": "2024-06-06T15:27:26.583893Z",
      "nodeId": "nABC",
      "start": "2024-06-06T15:25:26Z",
      "end": "2024-06-06T15:26:26Z",
      "virtualTraffic": [
        {"proto":6,"src":"100.64.0.1:443","dst":"100.64.0.2:51820","txPkts":10,"txBytes":1000,"rxPkts":8,"rxBytes":800}
      ],
      "exitTraffic": [
        {"proto":17,"src":"100.64.0.1:53","dst":"8.8.8.8:53","txPkts":1,"txBytes":60,"rxPkts":1,"rxBytes":120}
      ],
      "physicalTraffic": [
        {"src":"100.64.0.1:0","dst":"10.0.0.183:57532","txPkts":53,"txBytes":8708}
      ]
    }
  ]
}`

func TestDecodeNetworkResponse(t *testing.T) {
	var resp flowlog.NetworkResponse
	if err := json.Unmarshal([]byte(networkBody), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(resp.Logs))
	}
	l := resp.Logs[0]
	if l.NodeID != "nABC" {
		t.Fatalf("nodeId = %q, want nABC", l.NodeID)
	}
	if l.Start.IsZero() || l.End.IsZero() {
		t.Fatalf("start/end not parsed: %v / %v", l.Start, l.End)
	}
	if len(l.VirtualTraffic) != 1 {
		t.Fatalf("virtualTraffic = %d, want 1", len(l.VirtualTraffic))
	}
	cc := l.VirtualTraffic[0]
	if cc.Proto != 6 || cc.Src != "100.64.0.1:443" || cc.Dst != "100.64.0.2:51820" {
		t.Fatalf("connection 5-tuple wrong: %+v", cc)
	}
	if cc.TxBytes != 1000 || cc.RxBytes != 800 || cc.TxPkts != 10 || cc.RxPkts != 8 {
		t.Fatalf("byte/packet counts wrong: %+v", cc)
	}
	if len(l.ExitTraffic) != 1 || l.ExitTraffic[0].Dst != "8.8.8.8:53" || l.ExitTraffic[0].Proto != 17 {
		t.Fatalf("exitTraffic wrong: %+v", l.ExitTraffic)
	}
	// Physical entry omits rxPkts/rxBytes: they must default to zero, not error.
	if len(l.PhysicalTraffic) != 1 {
		t.Fatalf("physicalTraffic = %d, want 1", len(l.PhysicalTraffic))
	}
	ph := l.PhysicalTraffic[0]
	if ph.Proto != 0 {
		t.Fatalf("physical proto = %d, want 0 (absent)", ph.Proto)
	}
	if ph.TxBytes != 8708 || ph.RxBytes != 0 || ph.RxPkts != 0 {
		t.Fatalf("physical counts wrong (rx should be zero): %+v", ph)
	}
}
