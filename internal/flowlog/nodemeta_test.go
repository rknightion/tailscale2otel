package flowlog_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// liveRecordJSON reproduces the record shape observed in a live capture of
// GET /tailnet/{tailnet}/logging/network: every record carries srcNode, and
// 99% carry dstNodes. Field presence varies per node — os and user are absent
// on some — so decoding must tolerate any subset.
const liveRecordJSON = `{
  "logged": "2026-07-24T07:00:09.000000000Z",
  "nodeId": "nSrcCNTRL",
  "start":  "2026-07-24T07:00:00.000000000Z",
  "end":    "2026-07-24T07:00:05.000000000Z",
  "srcNode": {
    "nodeId": "nSrcCNTRL",
    "name": "camden.example.ts.net",
    "addresses": ["100.64.0.1", "fd7a:115c:a1e0::1"],
    "tags": ["tag:servers", "tag:sshrecorder"]
  },
  "dstNodes": [
    {
      "nodeId": "nDstCNTRL",
      "name": "mbp16.example.ts.net",
      "addresses": ["100.64.0.2"],
      "os": "macOS",
      "user": "rob@example.com"
    },
    {
      "nodeId": "nBareCNTRL",
      "name": "jules.example.ts.net",
      "addresses": ["100.64.0.3"],
      "tags": ["tag:servers"]
    }
  ],
  "virtualTraffic": [
    {"proto": 6, "src": "100.64.0.1:443", "dst": "100.64.0.2:51820",
     "txPkts": 10, "txBytes": 1000, "rxPkts": 8, "rxBytes": 800}
  ]
}`

func decodeLiveRecord(t *testing.T) flowlog.FlowLog {
	t.Helper()
	var fl flowlog.FlowLog
	if err := json.Unmarshal([]byte(liveRecordJSON), &fl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return fl
}

func TestFlowLog_DecodesEmbeddedNodeIdentity(t *testing.T) {
	fl := decodeLiveRecord(t)

	if fl.SrcNode == nil {
		t.Fatal("SrcNode is nil")
	}
	if fl.SrcNode.NodeID != "nSrcCNTRL" || fl.SrcNode.Name != "camden.example.ts.net" {
		t.Errorf("SrcNode identity = %+v", fl.SrcNode)
	}
	if len(fl.SrcNode.Addresses) != 2 {
		t.Errorf("SrcNode.Addresses = %v, want 2 entries", fl.SrcNode.Addresses)
	}
	if len(fl.SrcNode.Tags) != 2 {
		t.Errorf("SrcNode.Tags = %v, want 2 entries", fl.SrcNode.Tags)
	}
	// srcNode carries no os in the observed shape — absent, not an error.
	if fl.SrcNode.OS != "" {
		t.Errorf("SrcNode.OS = %q, want empty", fl.SrcNode.OS)
	}

	if len(fl.DstNodes) != 2 {
		t.Fatalf("DstNodes = %d, want 2", len(fl.DstNodes))
	}
	if fl.DstNodes[0].OS != "macOS" || fl.DstNodes[0].User != "rob@example.com" {
		t.Errorf("DstNodes[0] = %+v", fl.DstNodes[0])
	}
	// The second dst node has neither os nor user; both must decode as empty.
	if fl.DstNodes[1].OS != "" || fl.DstNodes[1].User != "" {
		t.Errorf("DstNodes[1] should have empty os/user, got %+v", fl.DstNodes[1])
	}
}

// The whole point of decoding the embedded identity: enrichment must no longer
// depend on the devices collector having run.
func TestProcess_ResolvesNamesWithEmptyDeviceCache(t *testing.T) {
	cache := enrich.NewDeviceCache() // deliberately empty: devices collector disabled
	p := flowlog.NewProcessor(cache, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	// Enriched from the record itself — and MARKED, because with no devices
	// collector nothing has confirmed these names (GHSA-pjfv-prc8-4fc9).
	if got, want := recs[0].Attrs[semconv.AttrSrcNode], unverifiedName("camden"); got != want {
		t.Errorf("%s = %q, want %q — embedded identity did not enrich", semconv.AttrSrcNode, got, want)
	}
	if got, want := recs[0].Attrs[semconv.AttrDstNode], unverifiedName("mbp16"); got != want {
		t.Errorf("%s = %q, want %q — embedded identity did not enrich", semconv.AttrDstNode, got, want)
	}
}

// Per-flow identity is the data behind the by-user / by-tag / by-OS views. Logs
// carry full detail by design, so these are unconditional there.
func TestProcess_LogCarriesPerFlowIdentity(t *testing.T) {
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	attrs := recs[0].Attrs
	for k, want := range map[string]string{
		semconv.AttrSrcTags: "tag:servers,tag:sshrecorder",
		semconv.AttrDstUser: "rob@example.com",
		semconv.AttrDstOS:   "macOS",
	} {
		if got := attrs[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// srcNode carries no user in this record: omit rather than emit empty.
	if _, ok := attrs[semconv.AttrSrcUser]; ok {
		t.Errorf("%s present despite absent source user", semconv.AttrSrcUser)
	}
}

// Identity is low-cardinality but is PII-adjacent and off the default metric
// surface, matching how ports and dst service are handled.
func TestProcess_IdentityMetricDimsAreOptIn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    flowlog.Options
		wantKey bool
	}{
		{name: "default: absent", opts: flowlog.Options{}},
		{name: "opt-in: present", opts: flowlog.Options{IdentityDims: true}, wantKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), tc.opts)
			rec := telemetrytest.New()
			p.Process(decodeLiveRecord(t), rec.Emitter())

			pts := rec.MetricPoints(flowlog.MetricIO)
			if len(pts) == 0 {
				t.Fatal("no metric points emitted")
			}
			_, got := pts[0].Attrs[semconv.AttrDstUser]
			if got != tc.wantKey {
				t.Errorf("%s present = %v, want %v (attrs %v)", semconv.AttrDstUser, got, tc.wantKey, pts[0].Attrs)
			}
		})
	}
}
