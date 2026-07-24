package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// dumpPolicy has the shape of a real tailnet policy: tag-to-tag grants, a group,
// an exit-node grant, a host alias, and a svc: rule the evaluator cannot resolve
// (which is what produces an undecidable verdict).
const dumpPolicy = `{
  "groups": {"group:eng": ["rob@example.com"]},
  "hosts":  {"router": "10.0.0.1"},
  "grants": [
    {"src": ["tag:servers"], "dst": ["tag:db"],            "ip": ["tcp:5432"]},
    {"src": ["group:eng"],   "dst": ["tag:servers"],       "ip": ["tcp:22"]},
    {"src": ["tag:servers"], "dst": ["autogroup:internet"],"ip": ["*"]},
    {"src": ["*"],           "dst": ["tag:ntp"],           "ip": ["udp:123"]},
    {"src": ["tag:mon"],     "dst": ["router"],            "ip": ["tcp:9100"]},
    {"src": ["svc:argocd"],  "dst": ["tag:db"],            "ip": ["tcp:443"]}
  ]
}`

// TestDumpFlowsJSON is a development aid, skipped unless FLOWSJSON_DUMP names a
// file. It drives a realistic tailnet through the real processor, store and
// handler and writes what /api/flows.json actually returns, so the page can be
// exercised against real output rather than a hand-written fixture:
//
//	FLOWSJSON_DUMP=/tmp/flows.json go test ./internal/app -run TestDumpFlowsJSON
//	FLOWHTML_DUMP=/tmp/page.html  go test ./internal/app/flowhtml -run TestRender_Injects
//
// then extract the page's last <script> block and run it under a DOM stub with
// that JSON as the fetch response. Two real bugs have been found this way that
// no Go test would have caught: an empty store rendering "covering 31 Dec,
// 23:58" (Go's zero time), and an unexplained relationship naming its endpoint
// "external" instead of the LAN address that made it actionable.
//
// The traffic below is chosen to produce all four verdicts plus unevaluated
// underlay traffic, and to leave several rules unexercised.
func TestDumpFlowsJSON(t *testing.T) {
	dst := os.Getenv("FLOWSJSON_DUMP")
	if dst == "" {
		t.Skip("set FLOWSJSON_DUMP to write a sample response")
	}
	a := flowsTestApp(t, nil)
	loadPolicy(t, a, dumpPolicy)

	rec := telemetrytest.New()
	now := time.Now().UTC()
	conn := func(proto int, src, dst string, tx, rx int64) flowlog.ConnectionCounts {
		return flowlog.ConnectionCounts{Proto: proto, Src: src, Dst: dst, TxBytes: tx, RxBytes: rx, TxPkts: tx / 100, RxPkts: rx / 100}
	}
	for i := range 12 {
		at := now.Add(-time.Duration(i) * 4 * time.Minute)
		a.runtimes[0].flowProc.Process(flowlog.FlowLog{
			NodeID: "nSrc", Start: at, End: at.Add(5 * time.Second), Logged: at.Add(7 * time.Second),
			SrcNode: &flowlog.NodeRef{NodeID: "nSrc", Name: "camden.example.ts.net",
				Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:servers"}},
			DstNodes: []flowlog.NodeRef{
				{NodeID: "nDb", Name: "pg.example.ts.net", Addresses: []string{"100.64.0.2"}, OS: "linux", Tags: []string{"tag:db"}},
				{NodeID: "nMbp", Name: "mbp16.example.ts.net", Addresses: []string{"100.64.0.3"}, OS: "macOS", User: "rob@example.com"},
			},
			VirtualTraffic: []flowlog.ConnectionCounts{
				conn(6, "100.64.0.1:41000", "100.64.0.2:5432", 90000, 400000), // permitted
				conn(6, "100.64.0.1:22", "100.64.0.3:52000", 12000, 3000),     // permitted in reverse
				conn(17, "100.64.0.1:44000", "10.0.0.254:53", 800, 4200),      // unexplained
				conn(6, "100.64.0.1:45000", "10.0.0.5:8080", 5000, 90),        // unexplained
				conn(1, "100.64.0.1:0", "100.64.0.3:0", 64, 64),               // icmp, unexplained
				conn(6, "100.64.0.1:46000", "100.64.0.2:443", 700, 900),       // svc: rule -> undecidable
			},
			ExitTraffic: []flowlog.ConnectionCounts{conn(6, "100.64.0.1:0", "", 300000, 1200000)},
			// The underlay, in all three shapes: a peer reached directly over each
			// IP family, and one that had to be relayed through DERP region 8.
			// Physical src is the PEER's overlay address and dst is the endpoint it
			// was reached at, which is what the path classification reads.
			PhysicalTraffic: []flowlog.ConnectionCounts{
				conn(0, "100.64.0.2:0", "10.0.0.5:41641", 40000, 40000),
				conn(0, "100.64.0.2:0", "[2001:db8::5]:41641", 9000, 9000),
				conn(0, "100.64.0.3:0", "127.3.3.40:8", 148, 96),
				conn(0, "100.64.0.3:0", "10.0.0.3:59879", 500, 500),
			},
		}, rec.Emitter())
	}

	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/flows.json?window=1h&recent=500", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var pretty any
	if err := json.Unmarshal(w.Body.Bytes(), &pretty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := os.WriteFile(dst, w.Body.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %d bytes to %s", w.Body.Len(), dst)
}
