// Package statushtml renders the admin status page from a statusdata.Status.
// The template is embedded so the binary stays self-contained (it must render on
// an isolated tailnet with no internet — no CDN/external assets), and all
// dynamic values pass through html/template's contextual auto-escaping.
package statushtml

import (
	"embed"
	"html/template"
	"io"

	"github.com/rknightion/tailscale2otel/v3/internal/apistate"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
)

//go:embed page.html.tmpl
var files embed.FS

// tmpl is parsed once at init; a malformed template panics here (and is caught
// by any test that imports this package), never at request time.
var tmpl = template.Must(template.New("page.html.tmpl").Funcs(funcs).ParseFS(files, "page.html.tmpl"))

var funcs = template.FuncMap{
	// minus subtracts b from a (used to show "discovered = active - static").
	"minus": func(a, b int) int { return a - b },
	// add64 sums two int64 counters (e.g. rdns query success+fail totals).
	"add64": func(a, b int64) int64 { return a + b },
	// healthClass maps a health verdict to its badge CSS class.
	"healthClass": func(state string) string {
		switch state {
		case "healthy":
			return "ok"
		case "degraded":
			return "err"
		default: // "starting" and any unknown state
			return "pending"
		}
	},
	// capCount tallies one dimension of the capability matrix for the summary
	// tiles. Counting here rather than in the DTO keeps statusdata a plain data
	// model with no derived fields for the JSON consumer to disagree with.
	"capCount": func(rows []statusdata.CapabilityRow, what string) int {
		n := 0
		for _, r := range rows {
			switch what {
			case "active":
				if r.Active {
					n++
				}
			case "actionable":
				if r.Actionable {
					n++
				}
			case "scope_gap":
				// Only a RUNNING row with a real gap counts: a disabled collector's
				// missing scope is not a problem anyone needs to fix.
				if r.Active && r.ScopeStatus == statusdata.ScopeInsufficient {
					n++
				}
			}
		}
		return n
	},
	// capStateClass badges an apistate value. "disabled" is deliberately neutral,
	// not an error — a tailnet simply lacking an optional feature is expected.
	"capStateClass": func(state string, actionable bool) string {
		if actionable {
			return "err"
		}
		if state == string(apistate.StateSupported) {
			return "ok"
		}
		return "pending"
	},
	// capScopeClass badges a scope-preflight outcome. Unknown and not_applicable
	// are neutral: neither is a finding.
	"capScopeClass": func(status string) string {
		switch status {
		case statusdata.ScopeSatisfied:
			return "ok"
		case statusdata.ScopeInsufficient:
			return "err"
		default:
			return "pending"
		}
	},
	// capReason renders a non-active reason code as operator-facing prose.
	"capReason": func(reason string) string {
		switch reason {
		case statusdata.CapabilityReasonUnsupported:
			return "unsupported"
		case statusdata.CapabilityReasonConfigDisabled:
			return "disabled in config"
		case statusdata.CapabilityReasonNotRegistered:
			return "not polling"
		default:
			return "inactive"
		}
	},
}

// Render writes the HTML status page for s to w.
func Render(w io.Writer, s statusdata.Status) error {
	return tmpl.Execute(w, s)
}
