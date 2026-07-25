package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// flowsPolicyDoc permits the connection seedFlows produces (camden, tag:servers,
// reaching mbp16 on 443) and adds a rule nothing will exercise.
const flowsPolicyDoc = `{"grants": [
	{"src": ["tag:servers"], "dst": ["*"],        "ip": ["tcp:443"]},
	{"src": ["tag:ntp"],     "dst": ["tag:ntp"],  "ip": ["udp:123"]}
]}`

// loadPolicy publishes a policy on the first runtime, as the acl collector does.
func loadPolicy(t *testing.T, a *App, doc string) {
	t.Helper()
	if err := a.runtimes[0].policy.SetDocument([]byte(doc)); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
}

// The page needs the rule list to say which rules went unexercised: the store
// counts rule INDEXES, and only the compiled policy can turn one back into
// something an operator recognizes.
func TestFlowsJSON_CarriesTheCompiledRuleCatalogue(t *testing.T) {
	a := flowsTestApp(t, nil)
	loadPolicy(t, a, flowsPolicyDoc)

	_, got := getFlows(t, a, "")
	if !got.Policy.Available {
		t.Fatal("Policy.Available = false with a compiled policy")
	}
	if len(got.Policy.Rules) != 2 {
		t.Fatalf("rules = %+v, want 2", got.Policy.Rules)
	}
	first := got.Policy.Rules[0]
	if first.Index != 0 || first.Kind != "grant" {
		t.Errorf("first rule = %+v, want index 0 of kind grant", first)
	}
	// The source text is what the operator sees next to a finding, so it has to
	// be the rule they wrote.
	if !strings.Contains(first.Source, "tag:servers") {
		t.Errorf("rule source = %q, does not show the rule it came from", first.Source)
	}
}

// A tailnet whose ACL has not been collected yet must be distinguishable from
// one whose policy explains everything — otherwise the page reports a clean bill
// of health for a check that never ran.
func TestFlowsJSON_PolicyAbsentBeforeCollection(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	_, got := getFlows(t, a, "")
	if got.Policy.Available {
		t.Error("Policy.Available = true before the acl collector has run")
	}
	if len(got.Policy.Rules) != 0 {
		t.Errorf("rules = %+v, want none", got.Policy.Rules)
	}
	if len(got.Result.Verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none — nothing was evaluated", got.Result.Verdicts)
	}
}

// A policy that will not compile is an operator-visible problem: the section
// would otherwise sit empty with no explanation.
func TestFlowsJSON_PolicyCompileErrorIsReported(t *testing.T) {
	a := flowsTestApp(t, nil)
	if err := a.runtimes[0].policy.SetDocument([]byte(`{"grants": "not a list"}`)); err == nil {
		t.Fatal("SetDocument accepted a malformed policy")
	}

	_, got := getFlows(t, a, "")
	if got.Policy.Available {
		t.Error("Policy.Available = true with nothing compiled")
	}
	if got.Policy.Error == "" {
		t.Error("Policy.Error is empty; the page has nothing to explain the empty section with")
	}
}

// End to end: a real record through the real processor, reconciled against a
// real policy, aggregated by the real store, out through the real handler.
func TestFlowsJSON_ReconciliationReachesTheAPI(t *testing.T) {
	a := flowsTestApp(t, nil)
	loadPolicy(t, a, flowsPolicyDoc)
	seedFlows(t, a)

	_, got := getFlows(t, a, "")
	verdicts := map[string]int64{}
	for _, v := range got.Result.Verdicts {
		verdicts[v.Label] = v.Counts.Flows
	}
	if verdicts[flowstore.VerdictPermitted] != 1 {
		t.Errorf("verdicts = %v, want the seeded connection permitted", verdicts)
	}
	if len(got.Result.Unexplained) != 0 {
		t.Errorf("unexplained = %+v, want none", got.Result.Unexplained)
	}
	// Rule 0 covered it; rule 1 saw nothing, which is what the unexercised list
	// is derived from.
	if len(got.Result.Rules) != 1 || got.Result.Rules[0].Rule != 0 {
		t.Errorf("rules = %+v, want only rule 0 exercised", got.Result.Rules)
	}
}

// The unexplained relationship is the finding the section exists for, and it has
// to survive the whole path rather than only the store's unit tests.
func TestFlowsJSON_UnexplainedRelationshipReachesTheAPI(t *testing.T) {
	a := flowsTestApp(t, nil)
	loadPolicy(t, a, `{"grants": [{"src": ["tag:ntp"], "dst": ["tag:ntp"], "ip": ["udp:123"]}]}`)
	seedFlows(t, a)

	_, got := getFlows(t, a, "")
	if len(got.Result.Unexplained) != 1 {
		t.Fatalf("unexplained = %+v, want 1 relationship", got.Result.Unexplained)
	}
	u := got.Result.Unexplained[0]
	if u.Src != "tag:servers" {
		t.Errorf("src = %q, want tag:servers — the endpoint's tags name it", u.Src)
	}
	if u.Transport != "tcp" || u.Port != "443" {
		t.Errorf("transport/port = %q/%q, want tcp/443", u.Transport, u.Port)
	}
}

// Each tailnet is reconciled against its OWN policy. Reporting one tailnet's
// traffic against another's rules would invent findings out of nothing.
func TestFlowsJSON_PolicyIsPerTailnet(t *testing.T) {
	a := twoTailnetFlowApp(t)
	loadPolicy(t, a, flowsPolicyDoc)

	_, first := getFlows(t, a, "?tailnet=one.example.com")
	if !first.Policy.Available {
		t.Error("the tailnet whose policy was loaded reports none")
	}
	_, second := getFlows(t, a, "?tailnet=two.example.com")
	if second.Policy.Available {
		t.Error("a tailnet with no policy of its own reported one")
	}
}

// The page and the documented jq one-liners iterate the rule list
// unconditionally, so it is an array even when there is no policy.
func TestFlowsJSON_PolicyRulesAreAlwaysAnArray(t *testing.T) {
	a := flowsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, loopbackReq(http.MethodGet, "/api/flows.json"))
	if strings.Contains(w.Body.String(), `"rules": null`) {
		t.Error(`policy.rules marshaled as null; it must be []`)
	}
}
