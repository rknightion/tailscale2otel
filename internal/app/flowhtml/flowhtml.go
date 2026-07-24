// Package flowhtml renders the built-in flow view at /flows.
//
// The template is embedded and entirely self-contained — no CDN, no webfonts, no
// remote images, no external scripts — because the page has to render on an
// isolated tailnet with no route to the internet. The topology graph, the
// timeline and the bar charts are all drawn as SVG by hand for the same reason.
//
// The server renders only the shell. Every number on the page arrives from
// /api/flows.json, so a large or empty store never delays the first paint, and
// the page keeps working across a re-poll without a full reload.
package flowhtml

import (
	"embed"
	"html/template"
	"io"

	"github.com/rknightion/tailscale2otel/v2/internal/app/flowsdata"
)

//go:embed page.html.tmpl
var files embed.FS

// tmpl is parsed once at init; a malformed template panics here (and is caught
// by any test importing this package), never at request time.
var tmpl = template.Must(template.New("page.html.tmpl").ParseFS(files, "page.html.tmpl"))

// Render writes the flow view's HTML shell for p to w.
func Render(w io.Writer, p flowsdata.Page) error {
	return tmpl.Execute(w, p)
}
