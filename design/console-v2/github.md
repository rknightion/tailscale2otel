repo: rknightion/tailscale2otel
branch: main
path: internal/app

Additional sources read and rebuilt from in this project (one design system, four consoles):
rknightion/graph2otel (main, internal/admin), rknightion/opnsense2otel (main, internal/webui),
rknightion/codexlb2otel (main, internal/live/ui).

## Last sync

date: 2026-08-31T12:01:52Z

### Updated in this project

- Applied the 2otel family v2 design to all four consoles: tailscale2otel, graph2otel, opnsense2otel and codexlb2otel.
- Added the family small-chart standard (defined in graph2otel) and the row-marker extension (defined in opnsense2otel).
- Corrected the family table standard: row hover lifts to the raised surface rather than tinting, which fixes an AA failure on state words in hovered rows.
- Wrote a per-repo implementation spec for each console, each with its own Phosphor set, AA measurements for new pairs, and assumptions.

Icon paths are copied verbatim from phosphor-icons/core@main `assets/regular/` and recorded in
`implementation-spec.md` §12.5; none is hand-written.

## Screen map

| Project screen | Repo files |
|---|---|
| Console today (recreation) - status console | tailscale2otel internal/app/statushtml/page.html.tmpl, statushtml.go |
| Console today (recreation) - flow explorer | tailscale2otel internal/app/flowhtml/page.html.tmpl, flowhtml.go |
| Console today (recreation) - events explorer | tailscale2otel internal/app/eventshtml/page.html.tmpl, eventshtml.go |
| Console v2 - 1a shell and tab bar | tailscale2otel internal/app/statushtml/page.html.tmpl |
| Console v2 - 1b Overview | tailscale2otel internal/app/statushtml/page.html.tmpl |
| Console v2 - 1c Cardinality | tailscale2otel internal/app/statushtml/page.html.tmpl |
| Console v2 - 1d Config | tailscale2otel internal/app/statushtml/page.html.tmpl |
| Console v2 - 1e Flow Explorer | tailscale2otel internal/app/flowhtml/page.html.tmpl |
| Console v2 - 1f Events Explorer | tailscale2otel internal/app/eventshtml/page.html.tmpl |
| Console v2 - 1g empty, error and action states | tailscale2otel internal/app/statushtml/page.html.tmpl |
| graph2otel Console v2 - 2a shell | graph2otel internal/admin/page.html.tmpl |
| graph2otel Console v2 - 2b Overview | graph2otel internal/admin/page.html.tmpl |
| graph2otel Console v2 - 2c small-chart standard | graph2otel internal/admin/page.html.tmpl (drawChart, drawSpark, registerChart) |
| graph2otel Console v2 - 2d Collectors | graph2otel internal/admin/page.html.tmpl (collectorRow, availabilityClass, skipClass) |
| opnsense2otel Console v2 - 3a shell | opnsense2otel internal/webui/templates/page.html.tmpl, internal/webui/render.go |
| opnsense2otel Console v2 - 3b Overview | opnsense2otel internal/webui/templates/page.html.tmpl, internal/webui/runtime.go, trend.go |
| opnsense2otel Console v2 - 3c Devices | opnsense2otel internal/webui/devices.go, oui.go, templates/page.html.tmpl |
| opnsense2otel Console v2 - 3d ifIndex | opnsense2otel internal/webui/ifindex.go, templates/page.html.tmpl |
| opnsense2otel Console v2 - 3e Cardinality and footer | opnsense2otel internal/webui/cardinality.go, growth.go, render.go (geoipCredit) |
| codexlb2otel Live monitor v2 - 4a the page | codexlb2otel internal/live/ui/index.html |
| codexlb2otel Live monitor v2 - 4b request states | codexlb2otel internal/live/ui/index.html (renderThread, quietOf) |
| codexlb2otel Live monitor v2 - 4c empty states | codexlb2otel internal/live/ui/index.html (render, connect, loadDetail) |
| implementation-spec.md (family block) | tailscale2otel internal/app, plus _ds tokens |
| graph2otel-implementation-spec.md | graph2otel internal/admin/page.html.tmpl |
| opnsense2otel-implementation-spec.md | opnsense2otel internal/webui |
| codexlb2otel-implementation-spec.md | codexlb2otel internal/live/ui/index.html |
