# opnsense2otel embedded console - m7kni Design System v2 implementation spec

Template: `internal/webui/templates/page.html.tmpl` (single file, `go:embed` via
`internal/webui/render.go`, inline CSS + vanilla JS, no build step). Gate: `just check`.

The family token block is reused **verbatim** from `implementation-spec.md` §1, and the small-chart
standard **verbatim** from `graph2otel-implementation-spec.md` §3. Do not fork either. This console
contributes one extension back to the family - the row marker in §4 - which is recorded in the family
block file so the other three repos inherit it.

Information architecture is unchanged: one page, the same seven client-side tabs (Overview,
Collectors, API, Cardinality, Devices, ifIndex, Config), the same `showTab` / `#tabs` / `data-target`
pattern, the same `#hash` deep link, the same lazy loads on Devices and ifIndex, the same
`/api/status.json` poll and `window.__refreshMs`.

**The GeoIP attribution stays.** `{{geoipCredit}}` in the footer is a CC BY 4.0 licence requirement
and is built from the `geoip` constants rather than a literal. Restyle it; never remove it, never make
it conditional, never move it out of the footer.

Dash rule: no em dash, no en dash, anywhere in UI copy, comments or docs. A spaced hyphen only. This
console has several: `pct()` returns an em dash for a never-run collector, and the JS twins use `—`
in a dozen places. All become `-`.

---

## 1. What changes, mechanically

1. Replace the three `:root` blocks with the family token block, keeping the compatibility aliases.
   `--pending` maps to `--t2o-fg-muted` as text and `--t2o-bg-track` as a fill.
2. Flip the theme default in `currentTheme()`, exactly as in graph2otel §1.2. The bootstrap script and
   the `opnsense-theme` localStorage key are unchanged.
3. Add the font faces and the `/_static/fonts/` route per family spec §2.
4. Radii: containers to 0, controls to 3px. `border-radius:999px` on `.badge` and `50%` on `.dot` both
   go; see §3.
5. Apply the family table standard (§6 of the family spec), the small-chart standard, and the state
   treatments below.
6. Replace every `—` with `-`, in the Go helpers (`pct`), the template, and the JS twins.

---

## 2. Chrome

`header`, `#healthBadge`, `#upstreamBadge`, `#captureBadge`, `#scrapeAge`, `#healthReasons`,
`#staleBanner`, `nav.tabs`, `.tab-btn`, `#themeToggle`, `#pauseBtn`, `#refreshNow`, `#generatedAt`,
`#updatedAgo`, `#devReload`, `#ifxReload` all keep their IDs and behaviour.

**Three badges, and they stay three.** This is the most important thing the header does. Exporter
health, the box, and the metric capture are independent signals, and the repo's own comments explain
why: during a transport outage the scheduler skips collector polls entirely, so the collector table
stays green through exactly the outage that matters. Merging them would destroy that.

| Badge | Source | States and glyphs |
|---|---|---|
| health | `healthClass` | `healthy` filled circle `--t2o-ok` · `degraded` diamond `--t2o-fail` · anything else `warning` `--t2o-warn` |
| box | `upstreamClass` | `ok` filled circle `--t2o-ok` · `unreachable` / `error` diamond `--t2o-fail` · `pending` / `unknown` hollow ring `--t2o-fg-muted` |
| capture | `captureClass` | `full` filled circle `--t2o-ok` · `partial` `warning` `--t2o-warn` · `never` hollow ring `--t2o-fg-muted` |

All three are 3px outlined chips, 11px/600 uppercase, border and text from the state token, leading
glyph, word always present. The prefixes stay as words rather than punctuation: `box unreachable`,
`capture partial`. Every existing `title` attribute is kept verbatim - they carry the distinctions
the badges compress, and `upstreamClass`'s comment is right that an erroring box is no more usable
than an absent one even though the two are worded differently.

`#scrapeAge` sits at the right of the header in 11px mono `--t2o-fg-muted`, and turns `--t2o-warn`
when the capture is not `full`. It reads `as of 3s ago`.

`#healthReasons`: `--t2o-accent-soft` fill, `warning-circle` in the state colour, reasons in 11px mono
`--t2o-fg-soft`. State colour on `--t2o-accent-soft` is icon-only, never text.

`#staleBanner`: same treatment as graph2otel - `--t2o-surface` strip, `warning` icon in
`--t2o-warn`, message in `--t2o-fg-soft`, no saturated fill.

**Tab bar**: underline tabs, per family §5. Seven tabs wrap on a narrow viewport; keep
`flex-wrap: wrap` and `position: sticky; top: 0`. `ifIndex` gains a 10px mono count with the diamond
shape in `--t2o-fail` when `v.Disagreements > 0`, set by `loadIfIndex()` after its fetch. Because that
tab loads lazily, the count appears on first open rather than first paint; that is correct and better
than a fetch on load, which is the thing the lazy design exists to avoid.

---

## 3. Badges and dots

`.dot` / `.dot-ok` / `.dot-bad` / `.dot-warn` (the 8px circles in the collector State column) become
the family status shapes: filled circle for `ok`, diamond for `failing`, `warning` triangle for
anything else, each followed by the state word. A bare coloured circle carries the state in hue
alone, which is the one thing the family contract forbids.

`.badge` loses its pill radius and its translucent fill. It becomes a 3px outlined chip: 1px border in
the state colour, text in the state colour, transparent background, 11px/600 uppercase, 1px 6px. The
freshness badge keeps its `title` (`last clean poll 4s ago` / `never fully succeeded`) and its `no
data` variant, which becomes a neutral chip with a hollow ring.

---

## 4. FAMILY EXTENSION: the row marker

The family table standard forbids tinted rows, and this console has three: `tr.bad`,
`tr.ifx-disagrees` and `tr.ifx-override`. The last two are the reason an extension is needed rather
than a deletion - an operator override is genuinely notable and a contradicted mapping is genuinely
urgent, and the repo's comment on those rules is correct that a contradicted mapping is the one thing
an operator must not scan past.

```css
:root{ --t2o-row-marker:2px; }
/* Applied as an inset box-shadow so it costs no layout and survives sticky headers. */
.t2o-row--alert{ box-shadow: inset var(--t2o-row-marker) 0 0 var(--t2o-fail); }
.t2o-row--note { box-shadow: inset var(--t2o-row-marker) 0 0 var(--t2o-accent); }
```

Replaces:

| Old | New |
|---|---|
| `tr.bad>td{background:rgba(248,81,73,.07)}` | `.t2o-row--alert` |
| `tr.ifx-disagrees>td{background:rgba(248,81,73,.10)}` | `.t2o-row--alert` + a `disagrees` chip in Notes |
| `tr.ifx-override>td{background:rgba(68,147,248,.07)}` | `.t2o-row--note` + an `override` chip in Source |
| `tr.err-row td{color:var(--err)}` | keep the colour, drop the row-wide mono override |

The marker is never the only signal: every marked row also carries a chip or a word in the cell that
caused it. tailscale2otel and graph2otel inherit this for their own failing rows.

---

## 5. Devices tab

The differentiator. A live firewall read merged from the ARP table plus the DHCPv4 and DHCPv6 leases,
lazily loaded on tab-open, bounded by an 8s cache, single-flight and a 20s deadline.

- **The note line** keeps its content and gains a bordered `--t2o-surface` strip with an `info` icon,
  12.5px `--t2o-fg-soft`, and the `reload` link promoted to a secondary 3px outlined button on the
  right. Add the cache age in 11px mono beside the match count ("8 of 142 · cached 4s ago") so a
  refresh-mashing operator can see why nothing changed.
- **Table**: family standard. Columns unchanged - IP, MAC, Hostname, Interface, Manufacturer, Source,
  Expiry. IP and MAC and Hostname and Expiry are mono; Interface and Manufacturer are sans, because
  an OPNsense interface description and an OUI vendor name are prose, not identifiers.
- `Manufacturer` falls back to the word `unknown` in `--t2o-fg-muted`, as today. Not a dash: an
  unresolved OUI is a known unknown, and the word says so.
- `Source` (`arp` / `dhcp4` / `dhcp6`) becomes a plain neutral 3px outlined mono chip. It is a
  provenance label with no severity, so it takes no colour.
- `Hostname` and `Expiry` fall back to `-`. A DHCPv6 lease carries no hostname by design (DUID
  replaces it upstream), so that blank is expected and needs no annotation.
- **Error callout** (`#devError`): today a `.reasons` strip with the borders stripped off. It becomes a
  proper failure block per family §9: 1px `--t2o-fail` border, `warning-circle`, a 13.5px/600 title
  ("Could not load devices"), a sentence stating the blast radius, and the error in 11px mono. The
  blast-radius sentence matters here and is worth writing out: this tab is the only route that
  reaches the firewall, and it never touches the collectors, so a failure here does not mean metric
  collection is affected. Keep `Retry-After: 2` behaviour and the 2s failure TTL.
- Empty: "No devices found." Loading: "Loading…" both in `--t2o-fg-muted`, centred, per the existing
  `.empty` cell.

---

## 6. ifIndex tab

Mono-dominant, and the one table in the family designed to be read straight down against another
program's output. Everything here serves that.

- **The explanatory paragraph stays verbatim.** It is the only place the product explains that a
  NetFlow ifIndex is a 1-based position over `ifinfo` output, that nothing pins it, and that adding
  any interface renumbers everything after it. Set it at 12.5px/1.65 `--t2o-fg-soft`, capped at about
  940px, with the code spans in mono. Do not tighten it; the mapping was silently wrong in production
  for months precisely because nothing showed it.
- **Disagreement callout** (`#ifxError`): promoted from a stripped `.reasons` line to an
  `--t2o-accent-soft` block with a 1px `--t2o-fail` border and a `warning-circle`. Message keeps its
  meaning, reworded to point at the marker rather than a tint: "2 interfaces resolve to an index the
  firewall states differently. Check the marked rows against `ifinfo` before trusting any
  per-interface flow series."
- **Six cards** become one bordered container split by hairline verticals, matching the family posture
  strip: Entries, Overridden, Conflicts, Disagreements, Unmapped lookups, Built. Values 15px mono;
  Overridden takes `--t2o-accent`, Conflicts and Unmapped take `--t2o-warn`, Disagreements takes
  `--t2o-fail`, the rest `--t2o-fg`.
- **Table**: ifIndex right-aligned mono (numeric order, not lexical - keep `sortedIfIndexRows`, and
  keep its comment: index 10 is exactly where the production off-by-one began, and a string sort
  scatters the divergence instead of parking it at the first row that differs). Device mono.
  Description sans. Source a chip: `derived` neutral with a small dot, `override` outlined
  `--t2o-warn` with a `pencil` icon. API states right-aligned mono, `--t2o-fail` when it disagrees.
  Notes: `disagrees` as `warning-circle` plus the word in `--t2o-fail`, otherwise `-`.
- **The 404 is not an error.** When `s.deps.IfIndexMap == nil` the endpoint 404s deliberately, because
  an empty 200 would read as "the map resolved to nothing", which is a very different operational
  statement from "NetFlow is switched off". Render it as the family's switched-off state: neutral
  border, hollow ring, "NetFlow is not enabled", then the sentence explaining that there is no map to
  resolve and that this is configuration rather than failure. Never the failure block.
- Keep `.scroll`'s 460px cap. Twelve interfaces fit; forty do not, and the sticky header is what makes
  the overflow readable.

---

## 7. Cardinality tab

The family dense-table standard, unchanged in structure.

- Four cards: Total series, Review at or above warn, Alerts at or above crit, Series budget. The warn
  and crit cards take a 1px border in their state colour and a leading `warning` / `warning-circle`.
- **Series budget keeps its `off` reading.** A budget of 0 disables the check entirely and must read
  as `off`, never as `0%`, which would look like headroom. Keep that branch exactly as it is. When a
  budget is set, show `97%` with `24,182 of 25,000` beneath, and the `over` chip as an outlined
  `--t2o-fail` chip when `OverBudget`.
- Top metrics: Metric, Level, Series, Labels. `Level` is promoted out of the metric name into its own
  column - today a `crit` badge sits inline after the name, which pushes the name's ellipsis around.
  `crit` takes `warning-circle` `--t2o-fail` and the row marker; `warn` takes `warning`
  `--t2o-warn`; `ok` takes a neutral dot.
- High-cardinality labels and Growth: same standard. Growth keeps its signed values (`+18.4`, `0.0`)
  so direction is readable without colour, with `--t2o-warn` for positive and `--t2o-fg-muted` for
  flat or negative.
- Keep the export links line, restyled to 11px mono `--t2o-fg-muted`.

---

## 8. Tabs not drawn, and the pattern each follows

| Tab | Pattern |
|---|---|
| Collectors | Family table standard §6, plus §3's shape-and-word states and §4's row marker. Thirteen columns; keep `Data age` and `Last attempt` as two separate columns and keep both `title` attributes - the distinction between the age of the metric buffer being served and the age of the last poll attempt is the whole point of having two. Sparkline and outcome strip per the small-chart standard §3.6. |
| API | Family table standard. The three summary cards become the fixed 3-column grid. `Auth` becomes word plus glyph. The permission-failure table keeps all six columns including `Grant this privilege` and the `(no narrower ACL exists)` suffix, and keeps its per-row `title` hint. |
| Config | Family Config pattern (family spec §8.1): two-column key / value rows at `220px 1fr`, keys mono `--t2o-fg-muted`, values mono `--t2o-fg`, one hairline per row. `.muted` on secret rows is replaced by a `set` / `unset` word chip; a greyed row implies the setting is inactive, which is not what a secret row means. Each `options.ConfigSection` keeps its own `sub-h` heading. |

Saying so here is deliberate. Drawing them again would produce three more screens that differ from
the family reference only in their column headers.

---

## 9. AA measurements, new pairs

Family pairs are in `implementation-spec.md` §10; chart pairs in
`graph2otel-implementation-spec.md` §7. New to this console:

| Pair | Light | Dark |
|---|---|---|
| row marker `--t2o-accent` on `--t2o-surface` | 5.73 | 6.89 |
| row marker `--t2o-fail` on `--t2o-surface` | 6.03 | 5.66 |
| `--t2o-warn` on `--t2o-surface` (override chip) | 4.99 | 7.00 |
| `--t2o-fail` on `--t2o-surface` (API states) | 6.03 | 5.66 |
| `--t2o-ok` on `--t2o-raised` (hovered row) | 5.04 | 5.79 |
| `--t2o-warn` on `--t2o-raised` (hovered row) | 5.25 | 6.35 |
| `--t2o-fail` on `--t2o-raised` (hovered row) | 6.35 | 5.14 |
| `--t2o-fg-muted` on `--t2o-raised` | 5.97 | 5.82 |
| `--t2o-fg` on `--t2o-bg-selected` | 13.19 | 11.49 |
| `--t2o-accent` on `--t2o-bg-selected` | 4.85 | 5.87 |

All pass AA. The row marker is a non-text graphic measured against the surface it sits on and clears
3:1 by a wide margin in both themes, which is what lets it replace a fill.

One exception, non-text: `--t2o-bg-track` on `--t2o-surface` at 1.50 / 1.58, used for bar troughs.
Every bar prints its value as text.

Add these pairs to `tools/contrast_check.py`.

---

## 10. Phosphor set for this repo

Inline SVG, `fill="currentColor"`, no icon font, no remote fetch. Eight icons:

`warning`, `warning-circle`, `info`, `magnifying-glass`, `caret-down`, `circle-half` (theme toggle),
`pencil` (an operator override), `arrow-clockwise` (the two reload links, if they become buttons).

Plus the three family status shapes: filled circle, diamond, hollow ring.

`pencil` is new to the family; add it to the shared inventory with the meaning "an operator set this
by hand", which is exactly what it means on the ifIndex Source column.

---

## 11. Assumptions

1. **Static font route.** No static-asset route exists today; assumed a new `go:embed`-backed
   `/_static/fonts/`, auth-exempt like `/healthz`, and `font-src 'self'` added to the CSP. Note this
   console's auth is opt-in (`--web.config.file`), so the font route must not be the thing that
   accidentally becomes the only unauthenticated path - mount it beside `/healthz` under the same
   rule.
2. **Disagreement count in the ifIndex tab** appears on first tab-open, not first paint, because the
   endpoint is lazy. Accepted deliberately rather than adding the count to `/api/status.json`, which
   would put a resolved-map read on the 5s poll.
3. **`ifIndex` cards** are assumed to keep their current six fields. `Built` is rendered with
   `toLocaleTimeString()` today; the spec leaves that alone rather than forcing UTC, since every other
   clock on this page is relative.
4. **Interface and Manufacturer set in sans** is a judgement, not a rule the family states. If an
   operator reports the Devices table harder to scan, move both to mono and note it as a per-repo
   exception rather than changing the family type roles.
5. **`sortedIfIndexRows` and the devices cache are untouched.** Nothing in this restyle changes a
   handler, a TTL, a deadline or a sort. If a mockup implies otherwise, the mockup is wrong.
6. **OUI table** (`oui_data.txt`, 154KB embedded) is unchanged and unstyled; only its rendered vendor
   string is affected.
7. **GeoIP attribution wording** comes from `geoipCredit()` and the `geoip` package constants. The
   mockup renders "IP geolocation data by DB-IP (CC BY 4.0)" with both link targets; the real string
   is whatever the constants say, and the constants win.
8. **Sample data** in the mockups is fabricated but shaped like real output: `ixl0` / `pppoe0` /
   `vlan01` device names, real OUI vendors, 1,006 metric families, 65 collectors, an off-by-one
   disagreement starting at index 9. No screen implies a field the status DTO does not carry.
