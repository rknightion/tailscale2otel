package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// The acceptance criterion of #302, end to end: reorder the tailnet's policy
// midway through a retained window and prove that neither half of the window is
// attributed to the other half's rules.
//
// Before this change both halves carried a bare index 0, the store summed them,
// and the API named whichever rule sat at index 0 at read time — so swapping two
// grants silently moved every historical byte onto the other rule.

// Two grants, distinguishable by their destination port. Reordering them swaps
// which one index 0 names, and nothing else about the policy changes.
const (
	reorderDocBefore = `{"grants":[
		{"src":["*"],"dst":["*"],"ip":["443"]},
		{"src":["*"],"dst":["*"],"ip":["5432"]}
	]}`
	reorderDocAfter = `{"grants":[
		{"src":["*"],"dst":["*"],"ip":["5432"]},
		{"src":["*"],"dst":["*"],"ip":["443"]}
	]}`
)

func flowTo(port string, at time.Time) flowlog.FlowLog {
	raw := `{"logged":"` + at.Format(time.RFC3339) + `",` +
		`"nodeId":"n1","start":"` + at.Format(time.RFC3339) + `","end":"` + at.Format(time.RFC3339) + `",` +
		`"virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"100.64.0.2:` + port + `",` +
		`"txPkts":1,"txBytes":100,"rxPkts":1,"rxBytes":100}]}`
	var fl flowlog.FlowLog
	if err := json.Unmarshal([]byte(raw), &fl); err != nil {
		panic(err)
	}
	return fl
}

func TestPolicyReorderMidWindowDoesNotCrossAttribute(t *testing.T) {
	var policy aclpolicy.Store
	if err := policy.SetDocument([]byte(reorderDocBefore)); err != nil {
		t.Fatalf("compile first policy: %v", err)
	}
	before := policy.Policy().Version()

	store := flowstore.NewMemory(0, flowstore.WithMaxFutureSkew(7*24*time.Hour))
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		Store:   store,
		Policy:  &policy,
		LogMode: "off",
	})
	e := telemetrytest.New().Emitter()

	// First half of the window: HTTPS traffic, permitted by the rule at index 0.
	p.Process(flowTo("443", time.Now().Add(-2*time.Minute)), e)

	// The operator reorders their ACL. Index 0 now names the 5432 grant.
	if err := policy.SetDocument([]byte(reorderDocAfter)); err != nil {
		t.Fatalf("compile reordered policy: %v", err)
	}
	after := policy.Policy().Version()
	if before == after {
		t.Fatal("the reorder did not change the policy version; the rest of this test proves nothing")
	}

	// Second half: the same HTTPS traffic, now permitted by the rule at index 1.
	p.Process(flowTo("443", time.Now().Add(-1*time.Minute)), e)

	res := store.Query(flowstore.Query{})
	rows := exercisedRules(&policy, res.Rules)
	if len(rows) != 2 {
		t.Fatalf("got %d exercised rows, want 2 (one per policy version): %+v\n"+
			"One row means the two halves were folded together, and the whole window is now "+
			"attributed to whichever rule sits at that index today.", len(rows), rows)
	}

	// Every row must name the 443 grant, because that is the traffic that was
	// actually sent — under BOTH policies, at two different indexes.
	byVersion := map[string]flowsdataRow{}
	for _, r := range rows {
		if !r.Available {
			t.Fatalf("row for version %s is unavailable, but both snapshots are retained", r.PolicyVersion)
		}
		byVersion[r.PolicyVersion] = flowsdataRow{index: r.Index, source: r.Source}
	}
	for _, v := range []string{before, after} {
		row, ok := byVersion[v]
		if !ok {
			t.Fatalf("no exercised row for policy version %s (have %v)", v, byVersion)
		}
		if !contains(row.source, "443") {
			t.Errorf("policy version %s attributes the traffic to rule %d (%q), but every "+
				"connection in this test was to port 443. The index moved with the reorder and the "+
				"row followed the index instead of the rule.", v, row.index, row.source)
		}
	}
	// And the indexes must differ, which is what makes this a reorder rather than
	// two identical policies.
	if byVersion[before].index == byVersion[after].index {
		t.Errorf("both versions report index %d; the reorder should have moved the 443 grant from "+
			"0 to 1", byVersion[before].index)
	}
}

type flowsdataRow struct {
	index  int
	source string
}
