# tailscale2otel embedded console - m7kni Design System v2 implementation spec

Applies to the three embedded pages in `internal/app/`:

| Page | Template | Notes |
|---|---|---|
| Status console `/` | `internal/app/statushtml/page.html.tmpl` | seven tabs, client-side switching |
| Flow Explorer `/flows` | `internal/app/flowhtml/page.html.tmpl` | shell rendered server-side, data from `/api/flows.json` |
| Events Explorer `/events` | `internal/app/eventshtml/page.html.tmpl` | shell + `/api/events.json` |

Constraints this spec respects: Go `html/template`, inline CSS and vanilla JS, `go:embed`, no build
step, no framework, no CDN, no icon font, no external network request of any kind.

**Family contract.** Section 1 is the shared token block. It is byte-identical across
`tailscale2otel`, `opnsense2otel`, `graph2otel` and `codexlb2otel`. Copy it, do not edit it per
repo. Everything after section 1 is per-page and repo-local.

**Dash rule.** No em dash, no en dash, anywhere in UI copy, comments or docs. A spaced hyphen is
the only dash.

---

## 1. FAMILY TOKEN BLOCK - shared verbatim across all four consoles

Paste as the first rule of every console's `<style>` block. Values are hex so the block has no
colour-space dependency; the oklch source each was generated from is in the comment beside it.

The theme contract is unchanged from today except for the flip: **light is the default**, the
`prefers-color-scheme` media query still applies when the user has not chosen, the explicit
`data-theme` attribute still wins, and the persisted key is still `ts2otel-theme`.

```css
/* ─────────────────────────────────────────────────────────────────────────────
   m7kni Design System v2 - 2otel console family token block.
   SHARED VERBATIM across tailscale2otel, opnsense2otel, graph2otel,
   codexlb2otel. Do not edit in one repo only. Generated from
   packages/tokens (DTCG) - hex values are the resolved sRGB of the oklch
   sources noted per line.
   ───────────────────────────────────────────────────────────────────────── */
:root{
  /* Type */
  --t2o-sans:"Hanken Grotesk",system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  --t2o-mono:"JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  --t2o-fs-2xs:10px;   /* uppercase micro labels */
  --t2o-fs-xs:11px;    /* mono sub-values, notes, badges */
  --t2o-fs-sm:12.5px;  /* table cells, controls, machine text */
  --t2o-fs-md:13.5px;  /* body copy */
  --t2o-fs-lg:15px;    /* page title, card values */
  --t2o-fs-xl:18px;    /* posture values */
  --t2o-fs-2xl:24px;   /* rare, one per page at most */
  --t2o-fw-regular:400;
  --t2o-fw-medium:500;
  --t2o-fw-semibold:600;
  --t2o-tracking-label:0.13em;

  /* Space and shape */
  --t2o-space-1:4px;  --t2o-space-2:8px;  --t2o-space-3:12px;
  --t2o-space-4:16px; --t2o-space-5:20px; --t2o-space-6:24px;
  --t2o-radius-control:3px;    /* buttons, inputs, chips, badges */
  --t2o-radius-container:0px;  /* panels, cards, tables - square */
  --t2o-radius-overlay:6px;    /* tooltips, popovers */
  --t2o-row:36px;              /* table row height */
  --t2o-dur-fast:120ms; --t2o-dur-base:160ms;

  /* Light theme - the default */
  --t2o-bg:#eef3f6;            /* oklch(0.962 0.007 227) */
  --t2o-surface:#f6fafb;       /* oklch(0.982 0.004 227) */
  --t2o-raised:#ffffff;        /* oklch(1 0 0) - table headers, inputs */
  --t2o-hover:#e7eef1;         /* oklch(0.945 0.009 227) */
  --t2o-selected:#dee8ec;      /* oklch(0.925 0.012 227) */
  --t2o-track:#c7cfd3;         /* oklch(0.85 0.01 227) - bar troughs */
  --t2o-fg:#192124;            /* oklch(0.24 0.012 227) */
  --t2o-fg-soft:#384146;       /* oklch(0.37 0.015 227) */
  --t2o-fg-muted:#59666b;      /* oklch(0.5 0.018 227) */
  --t2o-fg-faint:#869196;      /* oklch(0.65 0.015 227) - placeholder/disabled ONLY */
  --t2o-accent:#1d6a8a;        /* petrol */
  --t2o-accent-hover:#175874;
  --t2o-accent-soft:#e4eef4;   /* banner and selected-row wash */
  --t2o-on-accent:#ffffff;
  --t2o-line:#d9dfe2;          /* oklch(0.9 0.008 227) */
  --t2o-line-strong:#bec5c9;   /* oklch(0.82 0.01 227) */
  --t2o-ok:#2f7d4f;
  --t2o-warn:#8f6410;
  --t2o-fail:#a83a2e;

  /* Direction pair - see section 7 */
  --t2o-tx:var(--t2o-accent);
  --t2o-rx:var(--t2o-fg);
}

/* No stored preference: follow the OS. Explicit data-theme always wins. */
@media (prefers-color-scheme: dark){
  :root:not([data-theme]){
    --t2o-bg:#101618; --t2o-surface:#161d20; --t2o-raised:#1e2529;
    --t2o-hover:#1b2225; --t2o-selected:#222a2e; --t2o-track:#373f42;
    --t2o-fg:#e1e5e8; --t2o-fg-soft:#b7bfc3; --t2o-fg-muted:#95a1a6; --t2o-fg-faint:#697478;
    --t2o-accent:#66aecb; --t2o-accent-hover:#7cbcd6; --t2o-accent-soft:#1f2f38;
    --t2o-on-accent:#0e161b;
    --t2o-line:#2a3235; --t2o-line-strong:#40494e;
    --t2o-ok:#5fae7f; --t2o-warn:#c9a04a; --t2o-fail:#d97b64;
  }
}
:root[data-theme="light"]{
  --t2o-bg:#eef3f6; --t2o-surface:#f6fafb; --t2o-raised:#ffffff;
  --t2o-hover:#e7eef1; --t2o-selected:#dee8ec; --t2o-track:#c7cfd3;
  --t2o-fg:#192124; --t2o-fg-soft:#384146; --t2o-fg-muted:#59666b; --t2o-fg-faint:#869196;
  --t2o-accent:#1d6a8a; --t2o-accent-hover:#175874; --t2o-accent-soft:#e4eef4;
  --t2o-on-accent:#ffffff;
  --t2o-line:#d9dfe2; --t2o-line-strong:#bec5c9;
  --t2o-ok:#2f7d4f; --t2o-warn:#8f6410; --t2o-fail:#a83a2e;
}
:root[data-theme="dark"]{
  --t2o-bg:#101618; --t2o-surface:#161d20; --t2o-raised:#1e2529;
  --t2o-hover:#1b2225; --t2o-selected:#222a2e; --t2o-track:#373f42;
  --t2o-fg:#e1e5e8; --t2o-fg-soft:#b7bfc3; --t2o-fg-muted:#95a1a6; --t2o-fg-faint:#697478;
  --t2o-accent:#66aecb; --t2o-accent-hover:#7cbcd6; --t2o-accent-soft:#1f2f38;
  --t2o-on-accent:#0e161b;
  --t2o-line:#2a3235; --t2o-line-strong:#40494e;
  --t2o-ok:#5fae7f; --t2o-warn:#c9a04a; --t2o-fail:#d97b64;
}

/* ── Compatibility aliases ───────────────────────────────────────────────────
   The three templates already reference --bg/--panel/--line/... in hundreds of
   places, including inside JS that writes `fill:"var(--rx)"` into SVG. Keeping
   these aliases means the restyle lands as a palette swap plus targeted
   per-page edits, not a rename sweep. New CSS should use the --t2o-* names.
   ───────────────────────────────────────────────────────────────────────── */
:root{
  --bg:var(--t2o-bg);
  --panel:var(--t2o-surface);
  --panel2:var(--t2o-raised);
  --line:var(--t2o-line);
  --fg:var(--t2o-fg);
  --muted:var(--t2o-fg-muted);
  --accent:var(--t2o-accent);
  --ok:var(--t2o-ok);
  --warn:var(--t2o-warn);
  --err:var(--t2o-fail);
  --pending:var(--t2o-fg-muted);
  --grid:var(--t2o-line);
  --tx:var(--t2o-tx);
  --rx:var(--t2o-rx);
  --mono:var(--t2o-mono);
}
```

Two notes for whoever applies this:

- `--pending` was a distinct grey; it now maps to `--t2o-fg-muted`, which is AA on every surface. A
  pending badge is therefore neutral text plus the word "pending", never a colour-only cue.
- `--panel2` was a *darker* fill in dark mode and a *lighter* one in light mode. In v2 `--t2o-raised`
  is one step further from the canvas in both themes, so table headers and inputs read as raised in
  both. Anywhere the old CSS used `--panel2` as a low-emphasis wash rather than a raised fill, switch
  it to `--t2o-hover`.

### Theme bootstrap - unchanged, keep as is

```html
<script>
  // Apply the persisted theme BEFORE first paint. Shared key across all four consoles.
  (function(){try{var t=localStorage.getItem('ts2otel-theme');if(t)document.documentElement.setAttribute('data-theme',t);}catch(e){}})();
</script>
```

`toggleTheme()` keeps its current behaviour, with one change: `currentTheme()` must fall back to
**light** when no attribute is set and the OS expresses no preference.

```js
function currentTheme(){
  var t = document.documentElement.getAttribute('data-theme');
  if(t) return t;
  return (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light';
}
```

---

## 2. Fonts - self-hosted, zero external requests

No Google Fonts link, no CDN, no `@import` from a remote origin. These consoles run
air-gapped-adjacent and the admin server's CSP is `default-src 'none'`.

Vendor the same files the docs fleet vendors, embedded through `go:embed` and served from the admin
server under `/_static/fonts/`:

| File | Family | Notes |
|---|---|---|
| `hanken-grotesk-latin.woff2` | Hanken Grotesk | variable 100-900, **preload** |
| `hanken-grotesk-latin-ext.woff2` | Hanken Grotesk | variable 100-900, not preloaded |
| `JetBrainsMono-Variable.woff2` | JetBrains Mono | variable, machine text |

```css
@font-face{
  font-family:"Hanken Grotesk"; font-style:normal; font-weight:100 900; font-display:swap;
  src:url("/_static/fonts/hanken-grotesk-latin.woff2") format("woff2");
  unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+2000-206F,U+2074,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD;
}
@font-face{
  font-family:"Hanken Grotesk"; font-style:normal; font-weight:100 900; font-display:swap;
  src:url("/_static/fonts/hanken-grotesk-latin-ext.woff2") format("woff2");
  unicode-range:U+0100-024F,U+0259,U+1E00-1EFF,U+2020,U+20A0-20AB,U+20AD-20CF,U+2113,U+2C60-2C7F,U+A720-A7FF;
}
@font-face{
  font-family:"JetBrains Mono"; font-style:normal; font-weight:100 800; font-display:swap;
  src:url("/_static/fonts/JetBrainsMono-Variable.woff2") format("woff2");
}
```

Add to `<head>` of all three pages:

```html
<link rel="preload" href="/_static/fonts/hanken-grotesk-latin.woff2" as="font" type="font/woff2" crossorigin>
```

Every font stack ends in a real system fallback (`system-ui`, `ui-monospace`), so a deployment that
declines to serve the woff2 files still renders correctly, only less distinctively. The CSP needs
`font-src 'self'` adding; nothing else changes.

---

## 3. Type roles

Machine text dominates these pages: node names, IPs, ports, metric names, label values,
timestamps, byte counts. All of it is `--t2o-mono`. Prose, labels and headings are `--t2o-sans`.

| Role | Family | Size | Weight | Colour | Used for |
|---|---|---|---|---|---|
| Page title | sans | 15px | 600 | `--t2o-fg` | `tailscale2otel` in the header |
| Section label | sans | 10px | 600, tracking 0.13em, uppercase | `--t2o-fg-muted` | panel headers, card keys, table headers |
| Body | sans | 13.5px | 400 | `--t2o-fg-soft` | explanatory copy, notes |
| Tab | sans | 12.5px | 500, active 600 | muted / `--t2o-fg` | tab bar |
| Machine value | mono | 12.5px | 400, tabular-nums | `--t2o-fg` | table cells, IPs, metric names |
| Machine sub-value | mono | 11px | 400 | `--t2o-fg-muted` | second line in a cell, card subs |
| Stat value | mono | 15px | 500, tabular-nums | `--t2o-fg` | card values |
| Posture value | mono | 18px | 500, tabular-nums | state colour | the six posture cells |
| Badge / state | sans | 11px | 600, tracking 0.06em, uppercase | state colour | health, verdicts, levels |

`font-variant-numeric:tabular-nums` on every numeric cell. Minimum size anywhere is 10px, and 10px
is only ever an uppercase label, never a value.

---

## 4. Phosphor icons - inline SVG, sparing

Eight icons total, Phosphor **regular**, pasted as inline SVG with `fill="currentColor"` so they
take the surrounding token colour. No icon font, no sprite sheet, no remote fetch. Sizes: 11px
inline with 11px text, 13px in controls, 15-16px in state blocks.

| Icon | Where |
|---|---|
| `warning` (triangle) | advisories, warn severity, warn cardinality level |
| `warning-circle` | failing collector, critical cardinality level, degraded banner |
| `info` | "no data yet" state, collector info affordance |
| `magnifying-glass` | filter inputs |
| `download-simple` | support bundle, export CSV, export JSON |
| `caret-down` | selects |
| `arrow-right` | cross-page links (Flows, Events, Status) |
| `circle-half` | theme toggle |

```html
<!-- warning-circle -->
<svg viewBox="0 0 256 256" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm0,192a88,88,0,1,1,88-88A88.1,88.1,0,0,1,128,216Zm-8-80V80a8,8,0,0,1,16,0v56a8,8,0,0,1-16,0Zm20,36a12,12,0,1,1-12-12A12,12,0,0,1,140,172Z"/></svg>
<!-- warning -->
<svg viewBox="0 0 256 256" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M236.8,188.09,149.35,36.22h0a24.76,24.76,0,0,0-42.7,0L19.2,188.09a23.51,23.51,0,0,0,0,23.72A24.35,24.35,0,0,0,40.55,224h174.9a24.35,24.35,0,0,0,21.33-12.19A23.51,23.51,0,0,0,236.8,188.09ZM222.93,203.8a8.5,8.5,0,0,1-7.48,4.2H40.55a8.5,8.5,0,0,1-7.48-4.2,7.59,7.59,0,0,1,0-7.72L120.52,44.21a8.75,8.75,0,0,1,15,0l87.45,151.87A7.59,7.59,0,0,1,222.93,203.8ZM120,144V104a8,8,0,0,1,16,0v40a8,8,0,0,1-16,0Zm20,36a12,12,0,1,1-12-12A12,12,0,0,1,140,180Z"/></svg>
<!-- info -->
<svg viewBox="0 0 256 256" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm0,192a88,88,0,1,1,88-88A88.1,88.1,0,0,1,128,216Zm16-40a8,8,0,0,1-8,8,16,16,0,0,1-16-16V128a8,8,0,0,1,0-16,16,16,0,0,1,16,16v40A8,8,0,0,1,144,176ZM112,84a12,12,0,1,1,12,12A12,12,0,0,1,112,84Z"/></svg>
<!-- magnifying-glass -->
<svg viewBox="0 0 256 256" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M229.66,218.34l-50.07-50.06a88.11,88.11,0,1,0-11.31,11.31l50.06,50.07a8,8,0,0,0,11.32-11.32ZM40,112a72,72,0,1,1,72,72A72.08,72.08,0,0,1,40,112Z"/></svg>
<!-- download-simple -->
<svg viewBox="0 0 256 256" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M224,144v64a8,8,0,0,1-8,8H40a8,8,0,0,1-8-8V144a8,8,0,0,1,16,0v56H208V144a8,8,0,0,1,16,0Zm-101.66,5.66a8,8,0,0,0,11.32,0l40-40a8,8,0,0,0-11.32-11.32L136,124.69V32a8,8,0,0,0-16,0v92.69L93.66,98.34a8,8,0,0,0-11.32,11.32Z"/></svg>
<!-- caret-down -->
<svg viewBox="0 0 256 256" width="11" height="11" fill="currentColor" aria-hidden="true"><path d="M213.66,101.66l-80,80a8,8,0,0,1-11.32,0l-80-80A8,8,0,0,1,53.66,90.34L128,164.69l74.34-74.35a8,8,0,0,1,11.32,11.32Z"/></svg>
<!-- arrow-right -->
<svg viewBox="0 0 256 256" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M221.66,133.66l-72,72a8,8,0,0,1-11.32-11.32L196.69,136H40a8,8,0,0,1,0-16H196.69L138.34,61.66a8,8,0,0,1,11.32-11.32l72,72A8,8,0,0,1,221.66,133.66Z"/></svg>
<!-- circle-half -->
<svg viewBox="0 0 256 256" width="14" height="14" fill="currentColor" aria-hidden="true"><path d="M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm8,16.37a86.4,86.4,0,0,1,16,3V212.67a86.4,86.4,0,0,1-16,3Zm32,9.26a87.81,87.81,0,0,1,16,10.54V195.83a87.81,87.81,0,0,1-16,10.54ZM40,128a88.11,88.11,0,0,1,80-87.63V215.63A88.11,88.11,0,0,1,40,128Zm160,50.54V77.46a87.82,87.82,0,0,1,0,101.08Z"/></svg>
```

Three status **shapes** are not Phosphor. They are 16x16 primitives drawn inline so the shape, not
the colour, carries the state: filled circle = healthy / ok, diamond = degraded / failed, hollow ring
= starting / unknown.

```html
<svg viewBox="0 0 16 16" width="9" height="9" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="currentColor"/></svg>
<svg viewBox="0 0 16 16" width="9" height="9" aria-hidden="true"><path d="M8 0.5 15.5 8 8 15.5 0.5 8Z" fill="currentColor"/></svg>
<svg viewBox="0 0 16 16" width="9" height="9" aria-hidden="true"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2"/></svg>
```

---

## 5. Chrome - header, health, tab bar

Structure, IDs and ARIA are unchanged. `#themeToggle`, `#tabs`, `#healthBadge`, `#healthReasons`,
`#staleBanner`, `#updateBadge`, `role="tablist"`, `role="tab"`, `aria-selected`, the roving
`tabindex`, `handleTabsKeydown` and the `#hash` deep link all stay exactly as they are.

- **Header**: `--t2o-surface` fill, 12px 20px, one hairline `--t2o-line` bottom border. Product name
  15px/600. Version, Go version and tailnet in 11px mono `--t2o-fg-muted`, no separators beyond a
  middle dot.
- **Health badge** (`#healthBadge`): 3px-radius outlined chip, `border` and `color` from the state
  token, 11px/600 uppercase, and a leading shape. `healthy` = filled circle + `--t2o-ok`;
  `degraded` = diamond + `--t2o-fail`; `starting` = hollow ring + `--t2o-fg-muted` with a dashed
  border. The word is always present, so colour is never the only signal. Extend
  `healthClass` with the shape choice rather than adding a colour.
- **Reasons line** (`#healthReasons`): only rendered when non-empty. `--t2o-accent-soft` fill,
  `warning-circle` in `--t2o-fail`, reasons themselves in 11px mono `--t2o-fg-soft`. It is
  information, not an alarm bar; no full-bleed red.
- **Cross-page links**: `/flows` is a 3px outlined button with `arrow-right`; `/events` stays a plain
  text link. The old solid-accent `a.cta` is retired - a solid fill on this page now means one thing
  only, and that is the support bundle on Config.
- **Tab bar**: underline tabs. 9px 12px, 12.5px, inactive `--t2o-fg-muted`/500 with a transparent
  2px bottom border, active `--t2o-fg`/600 with a 2px `--t2o-accent` bottom border. Container keeps
  the hairline bottom border and stays `position:sticky;top:0`. Boxed tabs are gone.
- **Failing count in the tab**: when a tab's panel holds failures, append a 10px mono count with the
  diamond shape in `--t2o-fail` inside the tab button. Derive it from data already in the template
  (`.Fleet.Failing` for Collectors, `len .Cardinality.Alerts` for Cardinality); no new endpoint.

Minimum control target on these pages is a pointer target, not a touch target, so 26px square icon
buttons are acceptable. Do not go below 26px.

---

## 6. Table standard - the family reference

Applies to every table in all four consoles: cardinality, collectors, API endpoints, capability
matrix, metrics catalog, devices, flows, events.

```css
.t2o-table{width:100%;border-collapse:collapse;background:var(--t2o-surface);
  border:1px solid var(--t2o-line);border-radius:var(--t2o-radius-container)}
.t2o-table th{position:sticky;top:0;z-index:1;background:var(--t2o-raised);
  border-bottom:1px solid var(--t2o-line-strong);
  padding:7px 12px;text-align:left;font-family:var(--t2o-sans);font-size:var(--t2o-fs-2xs);
  font-weight:600;letter-spacing:var(--t2o-tracking-label);text-transform:uppercase;
  color:var(--t2o-fg-muted);white-space:nowrap}
.t2o-table td{padding:8px 12px;height:var(--t2o-row);vertical-align:top;
  border-bottom:1px solid var(--t2o-line);
  font-family:var(--t2o-mono);font-size:var(--t2o-fs-sm);font-variant-numeric:tabular-nums}
.t2o-table tbody tr:last-child td{border-bottom:0}
.t2o-table tbody tr:hover{background:var(--t2o-raised)}   /* raised, NOT hover - see §10 */
.t2o-table td.num{text-align:right;white-space:nowrap}
.t2o-table td .sub{display:block;font-size:var(--t2o-fs-xs);color:var(--t2o-fg-muted);margin-top:1px}
```

Rules:

- Hairline row rules only. **No zebra striping.**
- Sticky header, on a `--t2o-raised` fill so it stays legible over scrolled rows.
- Every numeric column is right-aligned mono with tabular figures.
- Row hover LIFTS the row to `--t2o-raised`; it does not tint it with `--t2o-hover`. This is a
  correction the later consoles in the wave forced: `--t2o-ok` on `--t2o-hover` measures 4.30 in
  light, which fails AA for the 11px state words that sit in hovered rows, while `--t2o-ok` on
  `--t2o-raised` measures 5.04. `--t2o-hover` stays as the control hover fill, where its content is
  `--t2o-fg` at 14.00. Selected rows (flow topology filter, matrix cell) use `--t2o-selected`, not an
  accent wash.
- A bad row is **not** a tinted row. Replace `tr.bad{background:rgba(248,81,73,.07)}` with a state
  badge in the Status column plus the error text in the Last error column. Colour never carries a
  row's meaning on its own.
- `.scroll` keeps its 460px cap and `.scroll.tall` its 75vh; only the border and radius tokens change.
- Filter inputs: 3px radius, `--t2o-raised` fill, 1px `--t2o-line-strong` border, 12.5px, leading
  `magnifying-glass` at `--t2o-fg-muted`, placeholder at `--t2o-fg-faint`. Show the match count
  beside the input in 11px mono, e.g. `9 of 308`.

Level and verdict cells are a shape or icon plus a word: `crit` with `warning-circle`, `warn` with
`warning`, `ok` with a filled dot, `capped` as a plain outlined chip. No bare colour swatches.

---

## 7. tx / rx derivation - colourblind safe

The pair is separated by **lightness plus form plus label**, never by hue.

```css
:root{
  --t2o-tx:var(--t2o-accent);  /* light #1d6a8a · dark #66aecb */
  --t2o-rx:var(--t2o-fg);      /* light #192124 · dark #e1e5e8 */
}
```

Derivation: `tx` is the accent; `rx` is the neutral ink already used for body text, which sits at the
far end of the lightness scale from the accent in each theme (light: L 0.45 accent against L 0.24
ink; dark: L 0.72 against L 0.92). Both are AA against every surface, so either can be read as text.

Because on a dark canvas both members must be light, their contrast **against each other** is
1.96:1 in dark and 2.72:1 in light - below the 3:1 adjacent-graphic guidance. The redundant cues are
therefore load bearing, not decorative:

| Context | tx | rx |
|---|---|---|
| Timeline chart | solid 1.8px stroke, `--t2o-tx` | dotted `stroke-dasharray:2 3`, 1.6px, `--t2o-rx` |
| Legend swatch | filled 9px square | 1.5px outlined 9px square |
| Table cells | filled 7px square before the number | outlined 7px square before the number |
| Every use | literal label `tx` | literal label `rx` |

The old area fills at `fill-opacity:.14` are dropped: two translucent overlapping areas cannot be
told apart without hue. Lines only.

Bar breakdowns (traffic type, transport, OS, users, tags) use a single `--t2o-accent` fill on a
`--t2o-track` trough. They are one series, so they need no pair.

---

## 8. Per-page structural notes

### 8.1 Status console (`statushtml/page.html.tmpl`)

- **Overview**: `#rollup` becomes the posture strip - one bordered container, six equal cells split
  by hairline verticals, each with a 10px uppercase key, an 18px mono value in its state colour, and
  an 11px sans sub-line. `.cards` grid becomes a fixed 4-column grid with 12px gaps, square corners,
  `--t2o-surface` fill, 10px 12px padding. `.card .k` is the section-label role, `.card .v` the mono
  stat role, `.card .sub` the mono sub-value role. `.card.crit` / `.card.warn2` keep their border
  colour swap and gain nothing else. Sparklines keep `svg.chart` and their `data-chart-name` hooks;
  stroke becomes `var(--t2o-accent)`, guide lines `var(--t2o-line)`, chart height drops to 72px.
- **Collectors**: table standard. Drop `tr.bad`; the Status badge plus Last error carry the fault.
  Keep the `.collector-info` affordance and its `.cinfo-tip`, restyled with
  `--t2o-radius-overlay`, `--t2o-raised` fill and a `--t2o-line-strong` border. Swap the `&#9432;`
  glyph for the inline `info` icon.
- **API**, **Capabilities**, **Inventory and Catalog**: table standard, no structural change. The
  capability matrix keeps `.scroll.tall` and its four summary cards.
- **Cardinality**: table standard, and this is the reference implementation. Three summary cards
  (total, alerts, trend), the filter input with its match count, then Top metrics, High-cardinality
  labels and Growth. Growth deltas keep `+`/`-` signs so the direction is readable without colour.
- **Config**: read-only, mono dominant. Effective configuration becomes a two-column key / value
  list, `220px 1fr`, keys in mono `--t2o-fg-muted`, values in mono `--t2o-fg`, one hairline per row.
  Durable state and Advisories stay tables; advisory keys get the `warning` icon in `--t2o-warn`.
  Add a **Diagnostics** panel at the end holding the primary support-bundle button
  (`/api/support-bundle.zip`), a secondary link to `/api/config.json`, and one sentence stating what
  the bundle contains and what it omits.
- **Footer**: unchanged content (`#generatedAt`, `#pauseBtn`, `#refreshNow`, `#updatedAgo`), restyled
  to 11px mono `--t2o-fg-muted` with 3px outlined buttons.
- **noscript**: unchanged. Stacked sections, hidden tab bar.

### 8.2 Flow Explorer (`flowhtml/page.html.tmpl`)

- Control bar: one bordered strip, `--t2o-surface`. `.seg` becomes 3px-radius with
  `--t2o-line-strong` dividers; the pressed segment is `--t2o-accent` with `--t2o-on-accent` text.
  `#liveBtn` is an outlined accent chip with a filled dot and the word `live`.
- `#cards`: same 4-column grid as Overview. The Traffic card carries the tx / rx legend inline, with
  swatch shapes as in section 7.
- `#timeline`: lines only, tx solid and rx dotted, end labels `tx` and `rx`. Brush selection rect
  uses `--t2o-accent` at `fill-opacity:.18` - it is a single element, so opacity is fine there.
- `#topo`: nodes `--t2o-accent`, external nodes `--t2o-fg-faint`, selected node keeps a
  `--t2o-fg` 2.5px stroke, edges `--t2o-fg-muted` at .35 opacity, labels mono 10px with a
  `--t2o-surface` paint-order stroke. Dimming stays as is.
- Matrix: cell fill ramps from `--t2o-track` to `--t2o-accent`; the hot end takes
  `--t2o-on-accent` text. Zero cells stay at `opacity:.35` and are not clickable. Every cell keeps
  its numeric label, so the ramp is decoration rather than the data.
- Recent connections: table standard. Columns unchanged - Time, Source, Destination, Verified
  reporter, Service, Type, Policy, Tx, Rx. Verdict cells are icon plus word:
  `permitted` filled dot `--t2o-ok`, `no_rule` `warning` `--t2o-warn`, `undetermined` hollow ring
  `--t2o-fg-muted`, `permitted_reverse` filled dot `--t2o-accent`. `.pill` type chips become plain
  11px mono in `--t2o-fg-soft`; the type is a word, it never needed a colour.
- Export: `#exportCsv` / `#exportJson` stay immediately below the table beside `#flowLoadMore`, as
  secondary outlined buttons with `download-simple`. `updateExportLinks()` is untouched. The
  paragraph explaining that export shares the table's window and filters stays.

### 8.3 Events Explorer (`eventshtml/page.html.tmpl`)

- Stats become a five-cell strip matching the Overview posture strip. `#statEvictedCard` keeps its
  `warn2` border swap and shows the value in `--t2o-warn`.
- Filter bar: same control language as the flow page. `#btnApply` is the only accent-outlined
  control; `#btnClear` is neutral.
- Table standard. `.sev-info` becomes a filled dot plus the word in `--t2o-fg-muted`; `.sev-warn`
  becomes `warning` plus the word in `--t2o-warn`. `.src` becomes a 3px outlined mono chip.
  `td.details` keeps its 420px cap, 11px mono, `--t2o-fg-muted`.
- `#errBanner` and `#truncBanner`: `--t2o-accent-soft` fill with a leading icon and state-coloured
  icon, text in `--t2o-fg-soft`. No saturated full-width bar.

---

## 9. Empty, disabled and error states

Three distinct states, three distinct treatments. Never render one as another.

| State | Border | Icon | Copy shape |
|---|---|---|---|
| Nothing yet | `--t2o-line` | `info`, `--t2o-fg-muted` | what will appear, and when. "The node_metrics collector has not completed its first run." |
| Switched off | `--t2o-line` | hollow ring, `--t2o-fg-muted` | what is off, the consequence, and the config key in mono |
| Failing | `--t2o-fail` | `warning-circle`, `--t2o-fail` | what failed, the blast radius, the error in mono, and that retries continue |

Each block is a bordered panel on `--t2o-surface`, 14px 16px, with a 13.5px/600 title, an optional
11px uppercase tag ("waiting", "disabled", "4 consecutive failures"), 12.5px body copy capped at
about 760px, and an 11px mono technical line. A disabled collector is never styled as a fault.

Action treatment: one primary per page, solid `--t2o-accent` with `--t2o-on-accent` text, and on the
status console that primary is the support bundle. Everything else is a 3px outlined secondary. A
disabled action states why in its own label ("Export CSV, nothing to export") rather than going
silent, and a failed action turns its own border and label `--t2o-fail`.

---

## 10. AA measurements

Computed WCAG 2.1 contrast ratios, sRGB, from the hex values in section 1. AA needs 4.5:1 for text
under 18.66px and 3:1 for large text and meaningful non-text.

| Pair | Light | Dark |
|---|---|---|
| `--t2o-fg` on `--t2o-bg` | 14.72 | 14.43 |
| `--t2o-fg` on `--t2o-surface` | 15.60 | 13.49 |
| `--t2o-fg` on `--t2o-raised` | 16.41 | 12.25 |
| `--t2o-fg` on `--t2o-hover` | 14.00 | 12.79 |
| `--t2o-fg-soft` on `--t2o-surface` | 9.87 | 9.16 |
| `--t2o-fg-muted` on `--t2o-surface` | 5.67 | 6.42 |
| `--t2o-fg-muted` on `--t2o-raised` | 5.97 | 5.82 |
| `--t2o-fg-muted` on `--t2o-bg` | 5.35 | 6.86 |
| `--t2o-accent` on `--t2o-bg` | 5.41 | 7.38 |
| `--t2o-accent` on `--t2o-surface` | 5.73 | 6.89 |
| `--t2o-accent` on `--t2o-raised` | 6.03 | 6.26 |
| `--t2o-on-accent` on `--t2o-accent` | 6.03 | 7.38 |
| `--t2o-accent-hover` on `--t2o-surface` | 7.43 | 8.15 |
| `--t2o-ok` on `--t2o-surface` | 4.79 | 6.38 |
| `--t2o-warn` on `--t2o-surface` | 4.99 | 7.00 |
| `--t2o-fail` on `--t2o-surface` | 6.03 | 5.66 |
| `--t2o-ok` on `--t2o-bg` | 4.52 | 6.82 |
| `--t2o-warn` on `--t2o-bg` | 4.71 | 7.49 |
| `--t2o-fail` on `--t2o-bg` | 5.69 | 6.06 |
| `--t2o-fg` on `--t2o-accent-soft` | 13.94 | 10.91 |
| `--t2o-fg-soft` on `--t2o-accent-soft` | 8.82 | 7.40 |

Every text pair passes AA in both themes, and the tightest one is `--t2o-ok` on `--t2o-bg` at 4.52.

Deliberate exceptions, all non-text:

- `--t2o-fg-faint` on `--t2o-surface`: 3.06 light, 3.53 dark. Placeholder and disabled text only,
  never readable copy. Every disabled control repeats its meaning in an adjacent AA-compliant label.
- `--t2o-line` on `--t2o-surface`: 1.28 light, 1.30 dark; `--t2o-line-strong` 1.66 / 1.86. Hairlines
  are decorative separation. Table structure is also carried by the raised sticky header, column
  alignment and row height, so nothing depends on a border being seen.
- `--t2o-tx` against `--t2o-rx`: 2.72 light, 1.96 dark. See section 7 - form and label carry the
  distinction, and each line is AA against its own background.

Add these pairs to `tools/contrast_check.py` so the gate covers the console palette in both themes.

---

## 11. Assumptions made where the repo and the brief left gaps

1. **Font path.** No static-asset route exists on the admin server today; every asset is inlined or a
   data URI. I assume a new `go:embed`-backed `/_static/fonts/` route, auth-exempt like `/healthz`,
   and `font-src 'self'` added to the CSP. If a static route is unacceptable, base64 the two
   preloaded faces into the CSS and accept the page-weight increase.
2. **Failing counts in tabs.** Assumed derivable from data already in the status DTO
   (`Fleet.Failing`, `len Cardinality.Alerts`). If a tab has no such field, omit the count for that
   tab rather than adding an endpoint.
3. **Log tailing has no UI.** `admin_logtail.go` is reachable only as an endpoint, and no template
   references it. Out of scope for this wave by request. When it is designed, it should be a
   Diagnostics-panel affordance on Config next to the support bundle, using the mono machine-text
   role at 11px with the same empty and error states as section 9.
4. **Support bundle contents.** The copy states the bundle carries a redacted status snapshot,
   effective config, collector health and a log tail. Confirm against `admin_bundle.go` before
   shipping the wording; the design assumes no credentials and no unredacted PII.
5. **Density.** The `--row-table: 36px` token is kept rather than the tighter 28px the family's
   current CSS implies, on the grounds that these pages are read next to Grafana and 36px matches the
   rest of the design system. Cells reach 36px through padding, not a fixed height.
6. **Update badge.** Kept as a warn-coloured chip in the header, unchanged in behaviour, restyled to
   the outlined badge language. It sits after the health badge.
7. **Multi-tailnet selector.** Unchanged in structure and position; it inherits the new control
   styling. The explanatory sentence beside it is kept verbatim.
8. **Charts.** The hand-drawn SVG timeline, topology, sparklines and matrix stay hand-drawn. No
   library is introduced, and the guide-line count (three) and 10px mono axis labels are unchanged.
9. **`--pending` retirement.** Anywhere the old palette used `--pending` as a *fill*, use
   `--t2o-track`; as *text*, use `--t2o-fg-muted`.
10. **Sample data.** Every value in the mockups is fabricated but shaped like real output (308
    metrics, 16 collectors, `__other__` folding, `nBk3xQ2CNTRL` reporter IDs). No screen implies a
    field the API does not return.


---

## 12. Family-block extensions made during the wave

Three additions and one correction, all recorded here because this file is the family block's home.
Every console in the wave inherits them; none of them is repo-local.

### 12.1 Small-chart standard (defined in graph2otel)

Full definition in `graph2otel-implementation-spec.md` §3. Tokens to add to §1:

```css
:root{
  --t2o-chart-grid:var(--t2o-line);
  --t2o-chart-axis:var(--t2o-fg-muted);
  --t2o-series-1:var(--t2o-accent);   /* solid 1.5px */
  --t2o-series-2:var(--t2o-fg);       /* dashed 4 3, 1.5px */
  --t2o-series-3:var(--t2o-warn);     /* dotted 1 3, 1.6px */
  --t2o-chart-area:0.10;              /* series 1 only, single-series charts only */
  --t2o-chart-h:110px;
  --t2o-chart-h-tall:150px;
  --t2o-spark-w:100px;
  --t2o-spark-h:22px;
}
```

Geometry is frozen at `padL 44 · padR 8 · padT 8 · padB 14`, three gridlines at min / mid / max, and
9px mono value labels inside the 44px gutter - outside the plot area, because an axis label over the
area fill measures 4.05 in dark and fails AA. Three series is the ceiling, slot order is fixed, and
every series is labelled. This supersedes §7's timeline treatment for tailscale2otel: `tx` is slot 1
and `rx` is slot 2, which is the same pair by another name.

### 12.2 Row marker (defined in opnsense2otel)

```css
:root{ --t2o-row-marker:2px; }
.t2o-row--alert{ box-shadow: inset var(--t2o-row-marker) 0 0 var(--t2o-fail); }
.t2o-row--note { box-shadow: inset var(--t2o-row-marker) 0 0 var(--t2o-accent); }
```

Replaces every tinted table row in the family (`tr.bad`, `tr.ifx-disagrees`, `tr.ifx-override`, the
cardinality offender tint). An inset shadow costs no layout and survives a sticky header. The marker
is never the only signal: a marked row always also carries a chip or a word in the cell that caused
it. This makes §6's "a bad row is not a tinted row" rule implementable rather than merely prohibitive.

### 12.3 Live and streaming (defined in codexlb2otel)

```css
:root{
  --t2o-dur-pulse:2s;      /* running-state pulse */
  --t2o-dur-arrive:240ms;  /* a row arriving over SSE */
}
@keyframes t2o-pulse{0%,100%{opacity:1}50%{opacity:.45}}
@keyframes t2o-arrive{from{opacity:0;transform:translateY(-2px)}to{opacity:1;transform:none}}
@media (prefers-reduced-motion: reduce){
  .t2o-pulse{animation:none}
  .t2o-arrive{animation:none}
}
```

A newly arrived row takes `.t2o-row--note` plus `t2o-arrive`, and drops both on the next snapshot, so
the marker says just-arrived rather than important. The pulse trough at .45 opacity takes any status
colour below AA, which is exactly why a live state is a glyph plus words and never a colour: at the
dimmest frame the row must still read.

Two new family status shapes come from this console too: **filled square** for "open but not
progressing", distinct from the **diamond**'s "finished badly".

### 12.4 Correction: row hover lifts, it does not tint

Applied to §6 above. `--t2o-ok` on `--t2o-hover` measures 4.30 in light, and the 11px semibold state
words that sit in hovered rows are small text, so that combination failed AA on every console in the
family. `--t2o-ok` on `--t2o-raised` measures 5.04. Table rows therefore hover to `--t2o-raised`, a
lift in both themes; `--t2o-hover` stays the control hover fill, where its content is `--t2o-fg` at
14.00.

A second rule from the same measurement pass: **state colour on `--t2o-accent-soft` is icon-only,
never text** (`--t2o-ok` 4.28, `--t2o-warn` 4.46 in light). Reasons lines and banners set their words
in `--t2o-fg-soft` at 8.82 and let the icon carry the state.

### 12.5 Icon inventory added

Six icons, Phosphor **regular**, taken from `phosphor-icons/core@main` `assets/regular/` and pasted
verbatim. Meanings are fixed family-wide so the same glyph never means two things.

| Icon | Means |
|---|---|
| `prohibit` | blocked by permission or licence |
| `arrows-split` | covered by another collector |
| `pencil` | an operator set this by hand |
| `git-fork` | a forked thread |
| `plugs` | the stream was lost |
| `circle-notch` | waiting for the first frame |

```html
<!-- prohibit -->
<svg viewBox="0 0 256 256" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm88,104a87.56,87.56,0,0,1-20.41,56.28L71.72,60.4A88,88,0,0,1,216,128ZM40,128A87.56,87.56,0,0,1,60.41,71.72L184.28,195.6A88,88,0,0,1,40,128Z"/></svg>
<!-- arrows-split -->
<svg viewBox="0 0 256 256" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M229.66,189.66l-32,32a8,8,0,0,1-11.32,0l-32-32a8,8,0,0,1,11.32-11.32L184,196.69V139.31l-56-56-56,56v57.38l18.34-18.35a8,8,0,0,1,11.32,11.32l-32,32a8,8,0,0,1-11.32,0l-32-32a8,8,0,0,1,11.32-11.32L56,196.69V136a8,8,0,0,1,2.34-5.66L120,68.69V24a8,8,0,0,1,16,0V68.69l61.66,61.65A8,8,0,0,1,200,136v60.69l18.34-18.35a8,8,0,0,1,11.32,11.32Z"/></svg>
<!-- pencil -->
<svg viewBox="0 0 256 256" width="11" height="11" fill="currentColor" aria-hidden="true"><path d="M227.31,73.37,182.63,28.68a16,16,0,0,0-22.63,0L36.69,152A15.86,15.86,0,0,0,32,163.31V208a16,16,0,0,0,16,16H92.69A15.86,15.86,0,0,0,104,219.31L227.31,96a16,16,0,0,0,0-22.63ZM51.31,160,136,75.31,152.69,92,68,176.68ZM48,179.31,76.69,208H48Zm48,25.38L79.31,188,164,103.31,180.69,120Zm96-96L147.31,64l24-24L216,84.68Z"/></svg>
<!-- git-fork -->
<svg viewBox="0 0 256 256" width="10" height="10" fill="currentColor" aria-hidden="true"><path d="M224,64a32,32,0,1,0-40,31v17a8,8,0,0,1-8,8H80a8,8,0,0,1-8-8V95a32,32,0,1,0-16,0v17a24,24,0,0,0,24,24h40v25a32,32,0,1,0,16,0V136h40a24,24,0,0,0,24-24V95A32.06,32.06,0,0,0,224,64ZM48,64A16,16,0,1,1,64,80,16,16,0,0,1,48,64Zm96,128a16,16,0,1,1-16-16A16,16,0,0,1,144,192ZM192,80a16,16,0,1,1,16-16A16,16,0,0,1,192,80Z"/></svg>
<!-- plugs -->
<svg viewBox="0 0 256 256" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M149.66,138.34a8,8,0,0,0-11.32,0L120,156.69,99.31,136l18.35-18.34a8,8,0,0,0-11.32-11.32L88,124.69,69.66,106.34a8,8,0,0,0-11.32,11.32L64.69,124,41.37,147.31a32,32,0,0,0,0,45.26l5.38,5.37-28.41,28.4a8,8,0,0,0,11.32,11.32l28.4-28.41,5.37,5.38a32,32,0,0,0,45.26,0L132,191.31l6.34,6.35a8,8,0,0,0,11.32-11.32L131.31,168l18.35-18.34A8,8,0,0,0,149.66,138.34Zm-52.29,65a16,16,0,0,1-22.62,0L52.69,181.25a16,16,0,0,1,0-22.62L76,135.31,120.69,180Zm140.29-185a8,8,0,0,0-11.32,0l-28.4,28.41-5.37-5.38a32.05,32.05,0,0,0-45.26,0L124,64.69l-6.34-6.35a8,8,0,0,0-11.32,11.32l80,80a8,8,0,0,0,11.32-11.32L191.31,132l23.32-23.31a32,32,0,0,0,0-45.26l-5.38-5.37,28.41-28.4A8,8,0,0,0,237.66,18.34Zm-34.35,79L180,120.69,135.31,76l23.32-23.31a16,16,0,0,1,22.62,0l22.06,22A16,16,0,0,1,203.31,97.37Z"/></svg>
<!-- circle-notch -->
<svg viewBox="0 0 256 256" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M232,128a104,104,0,0,1-208,0c0-41,23.81-78.36,60.66-95.27a8,8,0,0,1,6.68,14.54C60.15,61.59,40,93.27,40,128a88,88,0,0,0,176,0c0-34.73-20.15-66.41-51.34-80.73a8,8,0,0,1,6.68-14.54C208.19,49.64,232,87,232,128Z"/></svg>
```

Every path above is copied from the Phosphor source, not written by hand. Do not retype one: a
plausible-looking path renders as an off-centre fragment, and the only way to notice is `getBBox()`.
All Phosphor regular glyphs fill roughly `x24 y24 w208 h208` of the `0 0 256 256` box.
