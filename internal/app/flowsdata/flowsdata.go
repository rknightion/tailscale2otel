// Package flowsdata is the wire and template contract for the built-in flow
// view: Response is what /api/flows.json returns, Page is the server-rendered
// shell of /flows. It is a leaf package so the HTTP handler and the template
// package can share the shapes without either importing the other.
package flowsdata

import "github.com/rknightion/tailscale2otel/v3/internal/flowstore"

// Response is one answer from /api/flows.json. Everything the page draws comes
// from here, so the page can be a static shell that polls.
type Response struct {
	// Tailnet is the tailnet this answer describes; Tailnets lists every one the
	// process observes, so the page can offer a selector without a second call.
	Tailnet  string   `json:"tailnet"`
	Tailnets []string `json:"tailnets"`
	// Window is the requested lookback (already clamped to Retention); the span
	// actually covered by retained data is Result.Window.
	Window    string `json:"window"`
	Retention string `json:"retention"`
	// Stats describes the store itself — how much it holds and how much it had to
	// fold away — so the page can be honest about coverage.
	Stats  flowstore.Stats  `json:"stats"`
	Result flowstore.Result `json:"result"`
	// Recent is the newest raw connections, newest first. Always an array, never
	// null: the page iterates it unconditionally.
	Recent []flowstore.Recent `json:"recent"`
	// Policy is the tailnet's compiled network policy. Result carries what the
	// policy SAID about the window's traffic; this is what it consists of, which
	// is the half needed to name a rule that permitted nothing.
	Policy PolicyInfo `json:"policy"`
	// Exercised is Result.Rules with each row already resolved to the rule TEXT it
	// actually exercised, looked up in the snapshot of the policy version that
	// produced it.
	//
	// It exists so no consumer ever has to join a bare rule index to Policy.Rules.
	// That join is correct only while the ACL has not been edited, and silently
	// wrong the moment it has — which is precisely when someone is looking (#302).
	// Always an array, never null.
	Exercised   []ExercisedRule `json:"exercised"`
	GeneratedAt string          `json:"generated_at"`
}

// ExercisedRule is how much traffic one rule of one policy revision permitted,
// with that rule named.
type ExercisedRule struct {
	PolicyVersion string `json:"policy_version,omitempty"`
	Index         int    `json:"index"`
	// Available reports that the policy version was still retained and the index
	// resolved within it. When it is false Kind and Source are EMPTY and must be
	// rendered as "policy version unavailable" — never filled in from the current
	// policy, which would name a rule this traffic never touched.
	Available bool             `json:"available"`
	Kind      string           `json:"kind,omitempty"`
	Source    string           `json:"source,omitempty"`
	Counts    flowstore.Counts `json:"counts"`
}

// PolicyInfo describes the compiled policy the window's traffic was reconciled
// against. Available distinguishes the two states that would otherwise look
// identical on the page: a policy that explained everything, and no policy at
// all. Only the first is a clean bill of health.
type PolicyInfo struct {
	Available bool `json:"available"`
	// Version identifies the CURRENT rule list. Rules below belong to it and to
	// nothing else — traffic observed under an earlier policy must be read from
	// Response.Exercised, which resolves each row against its own version (#302).
	Version string `json:"version,omitempty"`
	// Error is the most recent compile failure, if any. A policy that will not
	// compile leaves the previous one in force, so Available and Error can both
	// be set — the section is reconciling against something stale.
	Error string `json:"error,omitempty"`
	// Rules is every compiled rule in document order. The index is what the
	// store's per-rule counts join on, so subtracting one from the other yields
	// the rules that permitted nothing in the window.
	Rules []PolicyRule `json:"rules"`
}

// PolicyRule is one rule of the compiled policy, in a form an operator can
// recognize as something they wrote.
type PolicyRule struct {
	Index int `json:"index"`
	// Kind is "grant" (the current syntax) or "acl" (the legacy list).
	Kind string `json:"kind"`
	// Source is the rule's own text, compacted.
	Source string `json:"source"`
}

// Page is the server-rendered shell of /flows. It carries only what must exist
// before the first poll completes — identity, the tailnet list, and the poll
// cadence. All traffic data arrives from Response.
type Page struct {
	ServiceName string
	Version     string
	Tailnets    []string
	Tailnet     string
	Retention   string
	// RefreshMs is how often the page re-polls /api/flows.json, from
	// admin.status_refresh_interval.
	RefreshMs int
}
