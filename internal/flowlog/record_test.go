package flowlog_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/flowlog"
)

// Sample shaped per the Tailscale GET /tailnet/{tailnet}/logging/network response.
const networkBody = `{
  "logs": [
    {
      "logged": "2024-06-06T15:27:26.583893Z",
      "nodeId": "nABC",
      "start": "2024-06-06T15:25:26Z",
      "end": "2024-06-06T15:26:26Z",
      "virtualTraffic": [
        {"proto":"tcp","src":"100.64.0.1:443","dst":"100.64.0.2:51820","txPkts":10,"txBytes":1000,"rxPkts":8,"rxBytes":800}
      ],
      "exitTraffic": [
        {"proto":"udp","src":"100.64.0.1:53","dst":"8.8.8.8:53","txPkts":1,"txBytes":60,"rxPkts":1,"rxBytes":120}
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
	if cc.Proto != "tcp" || cc.Src != "100.64.0.1:443" || cc.Dst != "100.64.0.2:51820" {
		t.Fatalf("connection 5-tuple wrong: %+v", cc)
	}
	if cc.TxBytes != 1000 || cc.RxBytes != 800 || cc.TxPkts != 10 || cc.RxPkts != 8 {
		t.Fatalf("byte/packet counts wrong: %+v", cc)
	}
	if len(l.ExitTraffic) != 1 || l.ExitTraffic[0].Dst != "8.8.8.8:53" {
		t.Fatalf("exitTraffic wrong: %+v", l.ExitTraffic)
	}
}
