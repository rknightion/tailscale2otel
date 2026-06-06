// Package statushtml renders the admin status page from a statusdata.Status.
// The template is embedded so the binary stays self-contained (it must render on
// an isolated tailnet with no internet — no CDN/external assets), and all
// dynamic values pass through html/template's contextual auto-escaping.
package statushtml

import (
	"embed"
	"html/template"
	"io"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
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
}

// Render writes the HTML status page for s to w.
func Render(w io.Writer, s statusdata.Status) error {
	return tmpl.Execute(w, s)
}
