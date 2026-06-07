package acl_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// collectDoc runs the collector over an inline HuJSON doc and returns the recorder.
func collectDoc(t *testing.T, hujsonDoc string) *telemetrytest.Recorder {
	t.Helper()
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: hujsonDoc, ETag: "etag-risk"}}
	c := acl.New(api, 0, func() time.Time { return time.Unix(1_000_000, 0).UTC() })
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rec
}

// gaugeBy indexes points of a metric by a single attribute key's value.
func gaugeBy(t *testing.T, rec *telemetrytest.Recorder, metric, attrKey string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, p := range rec.MetricPoints(metric) {
		if p.Kind != "gauge" {
			t.Fatalf("%s kind = %q, want gauge", metric, p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("%s unit = %q, want 1", metric, p.Unit)
		}
		out[p.Attrs[attrKey]] = p.Value
	}
	return out
}

// gaugeByPair indexes points by the concatenation of two attribute values.
func gaugeByPair(t *testing.T, rec *telemetrytest.Recorder, metric, k1, k2 string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, p := range rec.MetricPoints(metric) {
		out[p.Attrs[k1]+"/"+p.Attrs[k2]] = p.Value
	}
	return out
}

func TestWildcardAndUnrestrictedAndPosture(t *testing.T) {
	doc := `{
		"acls": [
			{"action":"accept","src":["*"],"dst":["*:*"]},
			{"action":"accept","src":["group:eng"],"dst":["tag:prod:22"],"srcPosture":["posture:latestMac"]},
			{"action":"deny","src":["*"],"dst":["*:*"]}
		],
		"grants": [
			{"src":["*"],"dst":["tag:x"]},
			{"src":["a@b"],"dst":["*"]}
		]
	}`
	rec := collectDoc(t, doc)

	wild := gaugeByPair(t, rec, "tailscale.acl.wildcard_rules", "tailscale.acl.section", "tailscale.acl.position")
	// acls: rule1 accept *->*:* counts src+dst; rule2 no wildcard; rule3 deny excluded.
	if wild["acls/src"] != 1 || wild["acls/dst"] != 1 {
		t.Fatalf("acls wildcard src/dst = %v/%v, want 1/1", wild["acls/src"], wild["acls/dst"])
	}
	// grants: g1 src*; g2 dst*.
	if wild["grants/src"] != 1 || wild["grants/dst"] != 1 {
		t.Fatalf("grants wildcard src/dst = %v/%v, want 1/1", wild["grants/src"], wild["grants/dst"])
	}

	unr := gaugeBy(t, rec, "tailscale.acl.unrestricted_rules", "tailscale.acl.section")
	if unr["acls"] != 1 {
		t.Fatalf("acls unrestricted = %v, want 1", unr["acls"])
	}
	if unr["grants"] != 0 {
		t.Fatalf("grants unrestricted = %v, want 0", unr["grants"])
	}

	pos := gaugeBy(t, rec, "tailscale.acl.posture_gated_rules", "tailscale.acl.section")
	if pos["acls"] != 1 {
		t.Fatalf("acls posture_gated = %v, want 1", pos["acls"])
	}
	if pos["grants"] != 0 {
		t.Fatalf("grants posture_gated = %v, want 0", pos["grants"])
	}
}

func TestLegacyUsersPortsWildcard(t *testing.T) {
	// Classic default-open rule using legacy users/ports instead of src/dst.
	doc := `{"acls":[{"action":"accept","users":["*"],"ports":["*:*"]}]}`
	rec := collectDoc(t, doc)
	wild := gaugeByPair(t, rec, "tailscale.acl.wildcard_rules", "tailscale.acl.section", "tailscale.acl.position")
	if wild["acls/src"] != 1 || wild["acls/dst"] != 1 {
		t.Fatalf("legacy wildcard src/dst = %v/%v, want 1/1", wild["acls/src"], wild["acls/dst"])
	}
	unr := gaugeBy(t, rec, "tailscale.acl.unrestricted_rules", "tailscale.acl.section")
	if unr["acls"] != 1 {
		t.Fatalf("legacy unrestricted = %v, want 1", unr["acls"])
	}
}

func TestRiskAbsentSectionEmitsNothing(t *testing.T) {
	// Only acls present: grants risk metrics must NOT be emitted.
	doc := `{"acls":[{"action":"accept","src":["group:eng"],"dst":["tag:p:22"]}]}`
	rec := collectDoc(t, doc)
	unr := gaugeBy(t, rec, "tailscale.acl.unrestricted_rules", "tailscale.acl.section")
	if _, ok := unr["grants"]; ok {
		t.Fatal("grants unrestricted emitted for absent section")
	}
	// acls present -> emitted as 0.
	if unr["acls"] != 0 {
		t.Fatalf("acls unrestricted = %v, want 0", unr["acls"])
	}
}

func TestSSHWildcard(t *testing.T) {
	doc := `{
		"ssh": [
			{"action":"check","src":["autogroup:admin"],"dst":["tag:srv"],"users":["root"]},
			{"action":"accept","src":["*"],"dst":["*"],"users":["root"]}
		]
	}`
	rec := collectDoc(t, doc)
	pts := rec.MetricPoints("tailscale.acl.ssh_wildcard")
	if len(pts) != 1 {
		t.Fatalf("ssh_wildcard points = %d, want 1", len(pts))
	}
	if pts[0].Unit != "1" || pts[0].Kind != "gauge" {
		t.Fatalf("ssh_wildcard unit/kind = %q/%q, want 1/gauge", pts[0].Unit, pts[0].Kind)
	}
	// rule1 no wildcard; rule2 src*+dst* -> 1. "users":["root"] must NOT count.
	if pts[0].Value != 1 {
		t.Fatalf("ssh_wildcard = %v, want 1", pts[0].Value)
	}
}

func TestSSHWildcardAbsentSection(t *testing.T) {
	rec := collectDoc(t, `{"acls":[]}`)
	if pts := rec.MetricPoints("tailscale.acl.ssh_wildcard"); len(pts) != 0 {
		t.Fatalf("ssh_wildcard emitted with no ssh section: %d points", len(pts))
	}
}

func TestAutoApproverDepth(t *testing.T) {
	doc := `{
		"autoApprovers": {
			"routes": {"10.0.0.0/24":["tag:r"], "10.1.0.0/16":["tag:r"]},
			"exitNode": ["tag:e"],
			"services": {"svc:x":["tag:s"]}
		}
	}`
	rec := collectDoc(t, doc)
	by := gaugeBy(t, rec, "tailscale.acl.autoapprovers", "tailscale.acl.autoapprover_kind")
	if by["routes"] != 2 {
		t.Fatalf("autoapprovers routes = %v, want 2", by["routes"])
	}
	if by["exit_node"] != 1 {
		t.Fatalf("autoapprovers exit_node = %v, want 1", by["exit_node"])
	}
	if by["services"] != 1 {
		t.Fatalf("autoapprovers services = %v, want 1", by["services"])
	}
}

func TestAutoApproverAbsentSection(t *testing.T) {
	rec := collectDoc(t, `{"acls":[]}`)
	if pts := rec.MetricPoints("tailscale.acl.autoapprovers"); len(pts) != 0 {
		t.Fatalf("autoapprovers emitted with no autoApprovers section: %d points", len(pts))
	}
}

func TestAutoApproverPresentButEmptyEmitsZeros(t *testing.T) {
	rec := collectDoc(t, `{"autoApprovers": {}}`)
	by := gaugeBy(t, rec, "tailscale.acl.autoapprovers", "tailscale.acl.autoapprover_kind")
	for _, kind := range []string{"routes", "exit_node", "services"} {
		if v, ok := by[kind]; !ok || v != 0 {
			t.Fatalf("autoapprovers %s = %v (ok=%v), want 0", kind, v, ok)
		}
	}
}
