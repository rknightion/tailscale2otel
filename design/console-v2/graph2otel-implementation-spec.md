# graph2otel embedded admin console - m7kni Design System v2 implementation spec

Template: `internal/admin/page.html.tmpl` (single file, `go:embed`, inline CSS + vanilla JS, no build
step). Gate: `just check`.

The family token block is reused **verbatim** from `implementation-spec.md` §1. Do not fork it, do
not add a local `:root` colour. This console contributes one extension back to the family - the
small-chart standard in §3 - which is recorded in the family block file so opnsense2otel,
tailscale2otel and codexlb2otel inherit it.

Information architecture is unchanged: one page, four client-side tabs (Overview, Collectors, Config,
Cardinality), the same `showTab` / `#tabs` / `data-target` pattern, the same `#hash` deep link, the
same `/api/status.json` poll and `window.__refreshMs`. No new endpoint, no new tab, no reordering.

Dash rule: no em dash, no en dash, anywhere in UI copy, comments or docs. A spaced hyphen only.

---

## 1. What changes, mechanically

The restyle is a palette swap plus targeted per-block edits, in this order:

1. Replace the three `:root` blocks with the family token block, keeping the compatibility aliases.
   `--info` and `--pending` have no family equivalent; see §5.
2. Flip the theme default. `currentTheme()` currently falls back to **dark**:
   ```js
   return (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches)?'light':'dark';
   ```
   becomes
   ```js
   return (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)?'dark':'light';
   ```
   The bootstrap script and the `graph2otel-theme` localStorage key are unchanged.
3. Add the font faces and the `/_static/fonts/` route per family spec §2. Same three woff2 files,
   same preload, same `font-src 'self'`.
4. Retire `border-radius:10px` on containers, `999px` on badges, `8px` on tabs and `7px` on buttons
   in favour of `--t2o-radius-container: 0` and `--t2o-radius-control: 3px`.
5. Apply the table standard (family §6), the chart standard (§3 below), and the state treatments (§4).

Everything below is the detail of steps 4 and 5.

---

## 2. Chrome

`header`, `#healthBadge`, `#readinessBadge`, `#healthReasons`, `#staleBanner`, `nav.tabs`,
`.tab-btn`, `#themeToggle`, `#pauseBtn`, `#refreshNow`, `#generatedAt`, `#updatedAgo` all keep their
IDs and behaviour.

- **Header**: `--t2o-surface`, 12px 20px, one hairline bottom border. `graph2otel` at 15px/600.
  Version, Go version, uptime and start time in 11px mono `--t2o-fg-muted`.
- **Two badges, not one merged signal.** Health and readiness are genuinely independent here -
  readiness returns 503 until the first successful collection and then stays ready while partial
  failures remain visible as degraded health. Both are 3px outlined chips, 11px/600 uppercase, with a
  leading shape: filled circle for `healthy` / `ready`, diamond for `degraded` / `not ready`, hollow
  ring with a dashed border for `starting`. The word is always present.
- **Reasons line** (`#healthReasons`): only when non-empty. `--t2o-accent-soft` fill, a
  `warning-circle` icon in the state colour, reasons in 11px mono `--t2o-fg-soft`. State colour on
  `--t2o-accent-soft` is icon-only, never text (see §7).
- **Stale banner** (`#staleBanner`): drop the saturated `rgba(210,153,34,.16)` fill. It becomes a
  `--t2o-surface` strip with a `warning` icon in `--t2o-warn`, the message in `--t2o-fg-soft`, and a
  secondary `Retry now` button on the right. Wording keeps its meaning: "Disconnected. Last
  successful update 46s ago."
- **Tab bar**: underline tabs. 9px 12px, 12.5px, inactive `--t2o-fg-muted`/500 with a transparent 2px
  bottom border, active `--t2o-fg`/600 with a 2px `--t2o-accent` bottom border. Keep
  `position:sticky;top:0`.
- **Failing count in the tab**: `Collectors` gains a 10px mono count with the diamond shape in
  `--t2o-fail`, summed from `t.failing_count` across tenants in `updateRollup`. `Cardinality` gains the
  same from `len .Cardinality.Offenders`. No new data.

---

## 3. FAMILY EXTENSION: the small-chart standard

This is the wave's one substantive addition to the family block. graph2otel is where it is defined
because the trend charts are this console's differentiator; the tokens below belong in the shared
block, not here.

### 3.1 Tokens to add to the family block

```css
:root{
  /* Charts - family-wide. Series colours are derived from the petrol accent
     plus the neutral ink already in the block; no new hues are introduced. */
  --t2o-chart-grid:var(--t2o-line);        /* the three gridlines */
  --t2o-chart-axis:var(--t2o-fg-muted);    /* 9px mono value labels */
  --t2o-series-1:var(--t2o-accent);        /* solid 1.5px */
  --t2o-series-2:var(--t2o-fg);            /* dashed 4 3, 1.5px */
  --t2o-series-3:var(--t2o-warn);          /* dotted 1 3, 1.6px */
  --t2o-chart-area:0.10;                   /* area opacity, series 1 only */
  --t2o-chart-h:110px;                     /* in-card line chart */
  --t2o-chart-h-tall:150px;                /* full-width line chart */
  --t2o-spark-w:100px;
  --t2o-spark-h:22px;
}
```

### 3.2 Geometry, frozen

`padL 44 · padR 8 · padT 8 · padB 14`. The 44px left gutter exists so the three value labels sit
**outside** the plot area. That is load bearing, not cosmetic: `--t2o-chart-axis` over the area fill
measures 4.05 in dark and would fail AA, while over the plain surface it measures 6.42. Never move a
label inside the plot.

Three gridlines only, at min / mid / max, drawn in `--t2o-chart-grid`. Value labels at 9px in
`--t2o-mono`, `text-anchor: start`, `x=4`.

`viewBox` is set from the measured `getBoundingClientRect()` with `preserveAspectRatio="none"`, as
today, and `redrawCharts()` still runs on tab switch, theme toggle, poll and resize. Keep all of it -
a chart in a hidden section measures 0 wide.

### 3.3 Series

Three is the ceiling. A fourth series means the chart is answering two questions and should be two
charts.

| Slot | Colour | Stroke | Width |
|---|---|---|---|
| 1 | `--t2o-series-1` | solid | 1.5px |
| 2 | `--t2o-series-2` | `stroke-dasharray: 4 3` | 1.5px |
| 3 | `--t2o-series-3` | `stroke-dasharray: 1 3` | 1.6px |

Slot order is fixed per chart so the same measure keeps the same stroke on every console. Every
series is labelled in an inline legend above the chart: a 14px swatch matching its stroke pattern,
then the series name, then its current value, all 11px mono. Colour is never the only carrier -
measured against each other the strokes are 2.72 (light) and 1.96 (dark), which is why the dash
patterns and labels are mandatory.

**Area fill**: only when the chart has exactly one series, only on slot 1, at
`--t2o-chart-area`. Two translucent overlapping areas cannot be told apart without hue, so a
multi-series chart is lines only. Replace the current unconditional `if(si===0)` area with a
`series.length === 1` test.

### 3.4 Hover

Keep the existing crosshair and tooltip. Crosshair: 1px `--t2o-fg-muted`, `stroke-dasharray 2 2`,
full plot height. Tooltip: `--t2o-raised` fill, 1px `--t2o-line-strong` border,
`--t2o-radius-overlay` (6px), 4px 8px, 11px mono, one `series name value` per series joined by a
middle dot. A 2.5px dot marks the sampled point on slot 1.

### 3.5 The two non-chart states

Both are text in the 44px-gutter position, at 11px mono `--t2o-fg-muted`, with a leading icon:

- **Fewer than two points**: `info` icon, "collecting, 1 of 2 samples". A single point is not a
  chart; never draw a dot and imply a trend. This replaces today's bare `collecting…`.
- **Nothing to chart**: hollow ring, and the switch that is off, e.g. "self-observability off". Never
  a flat line at zero, which claims a measurement that was not taken.

### 3.6 Sparkline

`svg.spark` becomes 100x22, no axis, no area, `stroke: var(--t2o-series-1)`, 1.5px,
`vector-effect: non-scaling-stroke`. The outcome strip beside it keeps 4px x 13px cells at
`--t2o-radius-container` (square), `--t2o-ok` for pass and `--t2o-fail` for fail, followed by a
plain-text count ("2 of 12 failed") so the strip is not the only carrier.

---

## 4. Collectors tab

The widest table in the wave and the reason the family table standard has a sticky header. Applies
family §6 with three additions.

### 4.1 Availability states

`availabilityClass()` maps nine states onto four badge classes today, which loses the distinction
between "blocked by permission" and "the call errored". Keep the mapping for colour, add a shape or
icon per state so each reads without colour:

| State | Colour | Glyph | Means |
|---|---|---|---|
| `healthy` | `--t2o-ok` | filled circle | running, data returned |
| `healthy_empty` | `--t2o-fg-muted` | filled circle | running, tenant has none |
| `limited` | `--t2o-warn` | `warning` | partial by source design |
| `degraded` | `--t2o-warn` | `warning` | emitting, quality reduced |
| `blocked` | `--t2o-fail` | `prohibit` | permission or licence |
| `failed` | `--t2o-fail` | `warning-circle` | the call returned an error |
| `startup_failed` | `--t2o-fail` | `warning-circle` | never started |
| `covered` | `--t2o-fg-soft` | `arrows-split` | another collector owns it |
| `disabled` | `--t2o-fg-muted` | hollow ring | switched off |
| `starting` | `--t2o-fg-muted` | hollow ring | no run yet |

Skip categories keep their own reading: `license` takes `warning` in `--t2o-warn`, `experimental`
takes `info` in `--t2o-fg-soft`, anything else is a plain neutral chip. `--info` (the purple used for
`covered` and `experimental` today) is retired; see §5.

State cells are word plus glyph at 11px/600 uppercase, with the availability reason and any
limitations on a second line at 10.5px mono `--t2o-fg-muted`.

### 4.2 Rows

- Drop `tr.bad{background:rgba(248,81,73,.07)}` and `tr.skip{color:var(--muted)}`. A failing row gets
  a **2px inset left marker** in `--t2o-fail` (`box-shadow: inset 2px 0 0 var(--t2o-fail)`), which is
  the family's new row-marker extension. A skipped row needs no marker at all: its state badge and
  its collapsed `colspan` already say so.
- The durable-checkpoint cursor line (`.cursor`) stays exactly where it is, under the collector name,
  at 10.5px mono `--t2o-fg-muted`. It is the highest-value text in the table and the restyle must not
  shrink it further.
- `Next run` shows `overdue` in `--t2o-warn` as a word, not a filled badge. `due` stays plain.
- `Last error` is 11px mono, `--t2o-fail` when present and `--t2o-fg-muted` for the empty marker,
  capped at 300px with `overflow-wrap: anywhere`.
- `.wide` (the 92vw break-out) is kept. This table has eleven columns and needs it.

### 4.3 Filter

`.filter` becomes the family filter control: 3px radius, `--t2o-raised`, 1px `--t2o-line-strong`,
12.5px, leading `magnifying-glass` in `--t2o-fg-muted`, placeholder at `--t2o-fg-faint`. Add a match
count beside it in 11px mono ("8 of 170"), computed in `filterAll()` from the rows it just showed.

A legend under the table lists the nine states with their glyphs. It is the only place a reader can
learn that `covered` is not a fault.

---

## 5. `--info` and `--pending` retirement

The family block has no purple and no second grey. Both current uses map onto existing tokens:

| Old | New | Where |
|---|---|---|
| `--info` as a badge | `--t2o-fg-soft` + `arrows-split` glyph | `covered` collectors |
| `--info` as a badge | `--t2o-fg-soft` + `info` glyph | `experimental` skip category |
| `--pending` as text | `--t2o-fg-muted` | `starting`, `unknown` |
| `--pending` as a fill | `--t2o-bg-track` | nothing in this console; keep the alias for parity |

`covered` losing its colour is deliberate. It was the only state whose hue implied a severity it does
not have: a covered collector is working exactly as designed.

---

## 6. Overview, Config and Cardinality

### 6.1 Overview

- `#rollup` becomes the family posture strip: one bordered container, six equal cells split by
  hairline verticals, each with a 10px uppercase key, an 18px mono value in its state colour, and an
  11px sans sub-line. Health, readiness, tenants, collectors, failing, starting - unchanged content,
  unchanged IDs (`#ruHealth`, `#ruReadiness`, `#ruTenants`, `#ruEnabled`, `#ruFailing`, `#ruPending`).
- **Throttle headroom stays first**, above Service. It keeps `table.rl`'s 640px cap (700px under the
  new scale), and the headroom column takes the state colour by threshold: `--t2o-ok` above 60
  percent, `--t2o-warn` from 20 to 60, `--t2o-fail` below 20. The `sparklineF` trend column takes the
  same colour as its row's headroom value, which is the one place the family permits a sparkline to be
  state-coloured rather than `--t2o-series-1`: the series and the number are the same measure.
- `.cards` becomes a fixed grid rather than `auto-fill minmax(190px,1fr)`: 3 columns for Service, 3
  for Runtime trend, 2 for Throughput and fleet, 2 for Exporter accepted. Auto-fill produced a
  different column count per section at the same viewport, which is what made the page look assembled
  rather than laid out.
- Delivery state badges become word plus glyph in `--t2o-ok` / `--t2o-fail` / `--t2o-fg-muted`. The
  four counter lines collapse to two: attempts / accepted / failed on one, then last success or last
  failure with its code on the next. Flush and shutdown failures move onto the first line only when
  non-zero - a permanent row of zeroes is not information.
- Capacity and Cost derive from the family table standard with no special treatment. The `Attribution`
  column's `badge warn` becomes a plain neutral chip: attribution is a method, not a warning. Keep
  both empty-state sentences verbatim.

### 6.2 Config

Read-only, mono dominant, per family §8.1's Config pattern. `table.rl` two-column setting / value
tables become key / value rows: `220px 1fr`, keys in mono `--t2o-fg-muted`, values in mono
`--t2o-fg`, one hairline per row, no header row. Secret presence stays a word: `set` in `--t2o-ok`
with a filled dot, `unset` in `--t2o-fg-muted` with a hollow ring. Never the value, and never a
masked string that implies a length.

### 6.3 Cardinality

The family dense-table standard, identical to tailscale2otel's reference implementation. Offenders
and per-metric tables share one column set (Metric, Level, Series, Headroom); `uncounted` takes
`warning-circle` in `--t2o-fail`, `near limit` takes `warning` in `--t2o-warn`, `ok` takes a neutral
dot. Drop `tr.bad`; use the row marker. Keep the paragraph explaining that this is output-side active
series after limiting, and that the exporter is push-based, verbatim.

---

## 7. AA measurements, new pairs

Family pairs are measured in `implementation-spec.md` §10 and unchanged. New pairs introduced by the
chart standard and this console:

| Pair | Light | Dark |
|---|---|---|
| `--t2o-series-1` on `--t2o-surface` | 5.73 | 6.89 |
| `--t2o-series-2` on `--t2o-surface` | 15.60 | 13.49 |
| `--t2o-series-3` on `--t2o-surface` | 4.99 | 7.00 |
| `--t2o-chart-axis` on `--t2o-surface` | 5.67 | 6.42 |
| `--t2o-series-1` on its own area fill | 5.25 | 4.36 |
| `--t2o-series-3` on the area fill | 4.57 | 4.42 |
| `--t2o-fg` on the area fill | 14.29 | 8.53 |
| row marker `--t2o-accent` on `--t2o-surface` | 5.73 | 6.89 |
| row marker `--t2o-fail` on `--t2o-surface` | 6.03 | 5.66 |
| `--t2o-ok` on `--t2o-raised` | 5.04 | 5.79 |
| `--t2o-warn` on `--t2o-raised` | 5.25 | 6.35 |
| `--t2o-fail` on `--t2o-raised` | 6.35 | 5.14 |
| `--t2o-fg-soft` on `--t2o-raised` | 10.38 | 8.32 |
| `--t2o-accent` on `--t2o-bg-track` | 3.83 | 4.37 |

Every text pair passes AA. Two findings worth recording, both of which changed the family block:

1. **Row hover must lift to `--t2o-raised`, not tint with `--t2o-hover`.** `--t2o-ok` on
   `--t2o-hover` measures 4.30 in light, and the 11px semibold state words in hovered rows are small
   text, so that fails. On `--t2o-raised` the same pair measures 5.04. Family §6 is corrected
   accordingly; `--t2o-hover` remains the control hover fill, where its content is `--t2o-fg` at
   14.00.
2. **State colour on `--t2o-accent-soft` is icon-only.** `--t2o-ok` on `--t2o-accent-soft` measures
   4.28 and `--t2o-warn` 4.46 in light. Icons are non-text and clear 3:1 comfortably; the words in a
   reasons line or banner take `--t2o-fg-soft` at 8.82.

Non-text exceptions, deliberate: `--t2o-chart-grid` on `--t2o-surface` at 1.28 / 1.30 and
`--t2o-bg-track` on `--t2o-surface` at 1.50 / 1.58. Both are structure, and in both cases the value
is also printed as text. Series strokes measured against each other are 2.72 / 1.96, mitigated by
dash pattern and label as above.

Add every pair in this table to `tools/contrast_check.py`.

---

## 8. Phosphor set for this repo

Inline SVG, `fill="currentColor"`, no icon font, no remote fetch. Nine icons:

`warning`, `warning-circle`, `info`, `prohibit` (blocked), `arrows-split` (covered),
`magnifying-glass`, `caret-down`, `circle-half` (theme toggle), `download-simple` (the three footer
JSON links, if they become buttons - otherwise omit).

Plus the three family status shapes, which are not Phosphor: filled circle, diamond, hollow ring.

`prohibit` and `arrows-split` are new to the family; add them to the shared icon inventory so
opnsense2otel and tailscale2otel can use them for the same meanings.

---

## 9. Assumptions

1. **Static font route.** As with tailscale2otel, no static-asset route exists today. Assumed a new
   `go:embed`-backed `/_static/fonts/`, auth-exempt like `/healthz`, and `font-src 'self'` in the CSP.
   Base64 into the CSS if a route is unacceptable.
2. **Failing counts in tabs** come from `t.failing_count` already summed in `updateRollup` and from
   `len .Cardinality.Offenders`. No new field, no new endpoint.
3. **Headroom thresholds** (60 / 20 percent) for the throttle colour ramp are invented here; the repo
   states no threshold. Confirm against whatever the alert rules use before shipping, and prefer the
   alert rule's boundary if they differ.
4. **`sparklineF`** is referenced by the template but was not read; assumed to be the float twin of
   `sparkline` with the same output shape. If its markup differs, the standard applies to its output
   rather than the reverse.
5. **Cost and Capacity** are styled from the family standards and were not drawn as mockups. They are
   tables and cards with no novel state, so nothing in them needs a decision this spec does not
   already make.
6. **`--info` retirement** assumes no operator has been trained to read purple as "covered". If that
   is a real risk, keep the glyph change and delay the colour change by one release rather than
   introducing a family purple.
7. **Chart height** drops from 120px to 110px in cards. If the trend cards look cramped once the new
   type scale is in, raise `--t2o-chart-h` in the family block rather than overriding locally.
8. **Sample data** in the mockups is fabricated but shaped like real output: 170 collectors, three
   tenants, GUID tenant IDs, blob cursors in bytes, `insights-logs-signinlogs` blob paths, real Graph
   permission strings. No screen implies a field the status DTO does not carry.
