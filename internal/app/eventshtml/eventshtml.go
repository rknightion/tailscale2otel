// Package eventshtml renders the built-in audit/webhook event explorer at
// /events (#300).
//
// The template is embedded and entirely self-contained — no CDN, no
// webfonts, no remote images, no external scripts — for the same reason as
// internal/app/flowhtml: it has to render on an isolated tailnet with no
// route to the internet, and the admin server's CSP (default-src 'none',
// #322) permits nothing else.
//
// The server renders only the shell. Every event on the page arrives from
// /api/events.json, so a large or empty store never delays the first paint,
// and the page keeps working across a re-poll without a full reload.
package eventshtml

import (
	"embed"
	"html/template"
	"io"

	"github.com/rknightion/tailscale2otel/v3/internal/app/eventsdata"
)

//go:embed page.html.tmpl
var files embed.FS

// tmpl is parsed once at init; a malformed template panics here (and is
// caught by any test importing this package), never at request time.
var tmpl = template.Must(template.New("page.html.tmpl").ParseFS(files, "page.html.tmpl"))

// Render writes the event explorer's HTML shell for p to w.
func Render(w io.Writer, p eventsdata.Page) error {
	return tmpl.Execute(w, p)
}
