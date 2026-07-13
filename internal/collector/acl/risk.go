package acl

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// Attribute keys for the ACL risk metrics. Collector-local keys follow the
// acl.go pattern (attrSection lives in acl.go).
const (
	attrPosition     = "tailscale.acl.position"          // src|dst
	attrApproverKind = "tailscale.acl.autoapprover_kind" // routes|exit_node|services
	attrRule         = "tailscale.acl.rule"              // offending src/dst (free-text; redactable)
)

// ruleEntry is the subset of an ACL/grant rule the risk pass inspects. Grants
// have no "action" (always allow) -> Action == "". srcPosture is decoded loosely
// (string|array per HuJSON) as raw to test presence only.
type ruleEntry struct {
	Action     string          `json:"action"`
	Src        []string        `json:"src"`
	Dst        []string        `json:"dst"`
	Users      []string        `json:"users"`
	Ports      []string        `json:"ports"`
	SrcPosture json.RawMessage `json:"srcPosture"`
}

// autoApproversDoc is the subset of the autoApprovers section we measure depth on.
type autoApproversDoc struct {
	Routes   map[string]json.RawMessage `json:"routes"`
	ExitNode []string                   `json:"exitNode"`
	Services map[string]json.RawMessage `json:"services"`
}

// emitRiskScores analyzes the parsed policy and emits structural risk/hygiene
// gauges. Each section's metrics are emitted only when the section is present
// (and decodable), including value 0 ("present but clean").
func (c *Collector) emitRiskScores(e telemetry.Emitter, top map[string]json.RawMessage) {
	emitRuleRisk(e, top, "acls")
	emitRuleRisk(e, top, "grants")
	emitSSHRisk(e, top)
	emitAutoApproverRisk(e, top)
}

// emitRuleRisk emits wildcard_rules{src,dst}, unrestricted_rules, and
// posture_gated_rules for one rule-bearing section (acls or grants).
func emitRuleRisk(e telemetry.Emitter, top map[string]json.RawMessage, section string) {
	raw, ok := top[section]
	if !ok {
		return
	}
	var entries []ruleEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return
	}

	var wildSrc, wildDst, unrestricted, posture int
	for _, en := range entries {
		ws := slices.ContainsFunc(srcElems(en), isWildcardSrc)
		wd := slices.ContainsFunc(dstElems(en), isWildcardDst)
		if en.Action != "deny" {
			if ws {
				wildSrc++
			}
			if wd {
				wildDst++
			}
			if ws && wd {
				unrestricted++
				// Emit a per-rule log event so operators can identify which
				// rule is unrestricted, not just how many exist.
				// Body is generic (section only) to keep the log body PII-safe;
				// the full src/dst content is in attrRule where pii_filter
				// (free_text_details category) can gate it.
				rule := fmt.Sprintf("src=%v dst=%v", srcElems(en), dstElems(en))
				e.LogEvent(telemetry.Event{
					Name:     EventRiskyRule,
					Severity: telemetry.SeverityWarn,
					Body:     fmt.Sprintf("Unrestricted ACL rule in section %q", section),
					Attrs:    telemetry.Attrs{attrSection: section, attrRule: rule},
				})
			}
		}
		// posture coverage counts all rules regardless of action (a deny rule
		// can legitimately carry a posture condition), unlike the wildcard
		// counters above which gauge over-broad allow access only.
		if hasPosture(en.SrcPosture) {
			posture++
		}
	}

	e.Gauge(docACLWildcardRules.Name, docACLWildcardRules.Unit, docACLWildcardRules.Description,
		float64(wildSrc), telemetry.Attrs{attrSection: section, attrPosition: "src"})
	e.Gauge(docACLWildcardRules.Name, docACLWildcardRules.Unit, docACLWildcardRules.Description,
		float64(wildDst), telemetry.Attrs{attrSection: section, attrPosition: "dst"})
	e.Gauge(docACLUnrestricted.Name, docACLUnrestricted.Unit, docACLUnrestricted.Description,
		float64(unrestricted), telemetry.Attrs{attrSection: section})
	e.Gauge(docACLPostureGated.Name, docACLPostureGated.Unit, docACLPostureGated.Description,
		float64(posture), telemetry.Attrs{attrSection: section})
}

// emitSSHRisk counts Tailscale SSH rules with a wildcard source or destination.
// SSH uses src/dst for access; its "users" field is login usernames (not source
// identities) and is deliberately ignored here.
func emitSSHRisk(e telemetry.Emitter, top map[string]json.RawMessage) {
	raw, ok := top["ssh"]
	if !ok {
		return
	}
	var entries []ruleEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return
	}
	var wild int
	for _, en := range entries {
		if slices.ContainsFunc(en.Src, isWildcardSrc) || slices.ContainsFunc(en.Dst, isWildcardDst) {
			wild++
		}
	}
	e.Gauge(docACLSSHWildcard.Name, docACLSSHWildcard.Unit, docACLSSHWildcard.Description,
		float64(wild), nil)
}

// emitAutoApproverRisk emits the depth of each auto-approver kind. When the
// autoApprovers section is present, all three kinds are emitted (0 when empty),
// so "present but none" is an explicit signal.
func emitAutoApproverRisk(e telemetry.Emitter, top map[string]json.RawMessage) {
	raw, ok := top["autoApprovers"]
	if !ok {
		return
	}
	var aa autoApproversDoc
	if err := json.Unmarshal(raw, &aa); err != nil {
		return
	}
	e.Gauge(docACLAutoApprovers.Name, docACLAutoApprovers.Unit, docACLAutoApprovers.Description,
		float64(len(aa.Routes)), telemetry.Attrs{attrApproverKind: "routes"})
	e.Gauge(docACLAutoApprovers.Name, docACLAutoApprovers.Unit, docACLAutoApprovers.Description,
		float64(len(aa.ExitNode)), telemetry.Attrs{attrApproverKind: "exit_node"})
	e.Gauge(docACLAutoApprovers.Name, docACLAutoApprovers.Unit, docACLAutoApprovers.Description,
		float64(len(aa.Services)), telemetry.Attrs{attrApproverKind: "services"})
}

// srcElems returns the source identities of a rule: src if present, else the
// legacy users field.
func srcElems(en ruleEntry) []string {
	if len(en.Src) > 0 {
		return en.Src
	}
	return en.Users
}

// dstElems returns the destinations of a rule: dst if present, else the legacy
// ports field.
func dstElems(en ruleEntry) []string {
	if len(en.Dst) > 0 {
		return en.Dst
	}
	return en.Ports
}

func isWildcardSrc(s string) bool { return s == "*" }

// isWildcardDst reports whether a destination matches any host. Handles bare
// "*" and "host:port" forms ("*:*", "*:80"), while not misfiring on IPv6
// literals ("[fd7a:...]:443") or tagged hosts ("tag:k8s:443").
func isWildcardDst(s string) bool {
	if s == "*" {
		return true
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i] == "*"
	}
	return false
}

// hasPosture reports whether a rule carries a non-empty srcPosture condition.
func hasPosture(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "[]" && s != "{}"
}
