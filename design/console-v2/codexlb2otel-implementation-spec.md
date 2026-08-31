# codexlb2otel live monitor - m7kni Design System v2 implementation spec

File: `internal/live/ui/index.html` (445 lines, single static file, inline CSS + vanilla JS, served
byte-for-byte via `go:embed`, SSE-fed). Gate: `make check` - this repo uses a Makefile, not a
justfile.

The family token block is reused **verbatim** from `implementation-spec.md` §1. This console was the
family outlier: it wore its own unrelated light-first palette (`#2c5fd0` accent, `#7aa2f7` in dark,
plus a purple and an orange for roles). All of it goes. One thing about it survives and is promoted:
its light-first stance, and its reasoning for it, is now the family default.

Information architecture is unchanged. One page, the live tree, the same SSE-driven updates, the same
`/api/stream` and `/api/threads` and `/api/threads/{id}` calls, the same `open` / `detail` / `closed`
/ `targetId` / `quiet` state kept outside the render so a 5s snapshot never undoes what the user just
did, the same `#thread=` fragment with `replaceState`, the same single grid for the whole tree.

Dash rule: no em dash, no en dash, anywhere in UI copy, comments or docs. A spaced hyphen only.

---

## 1. What survives, verbatim, and why

Four decisions in the current file are correct and the restyle must not disturb them. They are called
out first because a CSS replacement is exactly the kind of change that breaks them by accident.

1. **One grid for the whole tree, not one per row.** `main` is the grid; `.row` is
   `display: contents`. Per-row grids size their columns independently, so status and figures land at
   a different x on every line and the eye has nothing to run down. Keep `display: contents`.
2. **`textContent`, never `innerHTML`.** Every string rendered is text a model wrote, and this page
   exists to display exactly that untrusted content. The restyle adds no `innerHTML` anywhere,
   including for icons - inline SVG is built with `createElementNS`, or cloned from a `<template>`.
3. **The caret stops propagation.** Collapsing a subtree and expanding a row's turns are different
   intents on the same row. Keep `ev.stopPropagation()`.
4. **The caret is a fixed-width box.** `width: 1.1ch` whether or not a row has children, because a
   caret that only sometimes takes space shifts every sibling.

Two bugs to fix while in there, both pre-existing:

- `renderThread` reads `isTarget` on the line that builds `row`, one statement before
  `var isTarget = ...`. Under `var` hoisting that is `undefined`, so `.target` never applies and the
  `#thread=` deep link never highlights its row. Move the `isTarget` declaration above the `row`
  assignment.
- `renderTurns` renders `t.messages` and `t.tool_calls` with a `<br>` between entries inside a grid
  cell. With the new `.turn` grid that is fine, but the `say ` / tool-name prefix should be a styled
  span per line rather than one prefix for the block.

---

## 2. Type: this page is mostly machine text, but not entirely

The current file sets `font: 13px/1.45 ui-monospace` on `body` and everything inherits it. Under the
family type roles that is nearly right and wrong in one specific place.

| Content | Family | Why |
|---|---|---|
| thread names (task paths), thread ids, model names, token counts, turn counts, durations, timestamps, tool names, tool input, error codes | `--t2o-mono` | identifiers and figures |
| `latest_message`, `ask` | `--t2o-sans` | prose a model wrote, read as prose |
| badges, tags, column labels, empty-state copy | `--t2o-sans` | UI chrome |

Setting the model's own message in Hanken Grotesk rather than mono is the one type change worth
arguing for: it is the longest text on the page, it is sentences, and mono makes a paragraph 20
percent wider for no gain. Everything that lines up in a column stays mono.

Sizes: rows 12.5px, figures and sub-lines 11px, tags 10px, message text 12.5px/1.55, the `ask` line
12px. Header title 14px/600.

---

## 3. Near-complete replacement CSS

This replaces the whole `<style>` block. The family token block is assumed above it (elided here as
`/* family token block */` - paste `implementation-spec.md` §1 verbatim, including the font faces from
§2).

```css
/* family token block goes here, verbatim */

*{box-sizing:border-box}
body{
  margin:0;background:var(--t2o-bg);color:var(--t2o-fg);
  font-family:var(--t2o-sans);font-size:var(--t2o-fs-sm);line-height:1.45;
}
a{color:var(--t2o-accent);text-decoration:none}
a:hover{color:var(--t2o-accent-hover);text-decoration:underline}

/* ── Header ─────────────────────────────────────────────────────────────── */
header{
  display:flex;align-items:center;gap:12px;flex-wrap:wrap;
  padding:10px 16px;background:var(--t2o-surface);
  border-bottom:1px solid var(--t2o-line);
  position:sticky;top:0;z-index:5;
}
h1{margin:0;font-size:14px;font-weight:var(--t2o-fw-semibold);letter-spacing:-0.01em;
   display:inline-flex;align-items:center;gap:7px}
.meta{color:var(--t2o-fg-muted);font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs)}
.meta.warn{color:var(--t2o-warn)}
/* Connection state: an outlined chip with a word, not a bare dot. */
.conn{display:inline-flex;align-items:center;gap:5px;padding:1px 7px;
  border:1px solid currentColor;border-radius:var(--t2o-radius-control);
  font-size:var(--t2o-fs-2xs);font-weight:var(--t2o-fw-semibold);
  letter-spacing:.06em;text-transform:uppercase}
.conn.up{color:var(--t2o-ok)}
.conn.retry{color:var(--t2o-fail)}
.conn.wait{color:var(--t2o-fg-muted)}
#theme{
  margin-left:auto;display:inline-flex;align-items:center;gap:6px;
  background:transparent;border:1px solid var(--t2o-line-strong);
  border-radius:var(--t2o-radius-control);color:var(--t2o-fg-soft);
  font-family:var(--t2o-sans);font-size:12px;padding:3px 9px;cursor:pointer;
}
#theme:hover{color:var(--t2o-fg);border-color:var(--t2o-accent)}

/* ── The tree: ONE grid ─────────────────────────────────────────────────── */
main{
  padding:6px 0 48px;
  display:grid;grid-template-columns:1fr auto auto;
  column-gap:22px;align-items:baseline;
}
.row{display:contents;cursor:pointer}
.cell{padding:3px 0}
.cell:first-child{padding-left:16px}
.cell:last-child{padding-right:16px}
/* Hover LIFTS the row rather than tinting it - see the family AA note. */
.row:hover>.cell{background:var(--t2o-raised)}
.row.open>.cell{background:var(--t2o-raised)}
.row.target>.cell{background:var(--t2o-raised);box-shadow:inset 2px 0 0 var(--t2o-accent)}
/* A row that just arrived over SSE. */
.row.arrived>.cell{box-shadow:inset 2px 0 0 var(--t2o-accent);animation:arrive 240ms ease-out}
.under{grid-column:1 / -1}

.lead{display:flex;align-items:baseline;gap:6px;min-width:0}
.tree{font-family:var(--t2o-mono);font-size:var(--t2o-fs-sm);
  color:var(--t2o-fg-muted);white-space:pre;flex:none}
.caret{font-family:var(--t2o-mono);font-size:var(--t2o-fs-sm);
  color:var(--t2o-fg-muted);width:1.2ch;text-align:center;flex:none}
.caret.has{cursor:pointer}
.caret.has:hover{color:var(--t2o-fg)}
.nm{font-family:var(--t2o-mono);font-size:var(--t2o-fs-sm);
  font-weight:var(--t2o-fw-semibold);color:var(--t2o-fg);
  white-space:nowrap;overflow:hidden;text-overflow:ellipsis}

/* Tags: outlined chips, word always present, no role hues. */
.tag{display:inline-flex;align-items:center;gap:4px;flex:none;
  padding:0 5px;border:1px solid var(--t2o-line-strong);
  border-radius:var(--t2o-radius-control);
  font-size:var(--t2o-fs-2xs);letter-spacing:.06em;text-transform:uppercase;
  color:var(--t2o-fg-muted)}
.tag.sub{color:var(--t2o-fg-muted)}
.tag.fork{color:var(--t2o-fg-soft)}
.tag.err{border-color:var(--t2o-fail);color:var(--t2o-fail);
  font-family:var(--t2o-mono);text-transform:none;letter-spacing:0}
.tag.stall{border:2px solid var(--t2o-fail);color:var(--t2o-fail);
  font-weight:var(--t2o-fw-semibold)}

/* Status column: capped and clipped, or the longest tool call on screen
   pushes the figures column off the right edge for every row. */
.status{display:inline-flex;align-items:center;gap:6px;
  font-family:var(--t2o-mono);font-size:12px;color:var(--t2o-fg-muted);
  white-space:nowrap;max-width:42ch;overflow:hidden;text-overflow:ellipsis}
.status.live{color:var(--t2o-ok)}
.status.stalled{color:var(--t2o-fail)}
.figs{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);
  color:var(--t2o-fg-muted);white-space:nowrap;font-variant-numeric:tabular-nums}

/* The agent's own newest message: prose, the primary content. */
.say{padding:0 16px 4px 0;font-size:var(--t2o-fs-sm);line-height:1.55;
  color:var(--t2o-fg);max-width:105ch;overflow-wrap:anywhere}
.say .q{color:var(--t2o-fg-faint)}
.say.warn{color:var(--t2o-fg-muted)}
.ask{padding:0 16px 3px 0;font-size:12px;line-height:1.55;
  color:var(--t2o-fg-soft);max-width:100ch;overflow-wrap:anywhere}
.ask b{color:var(--t2o-fg-muted);font-weight:var(--t2o-fw-regular)}
.spawned{padding:0 16px 3px 0;font-family:var(--t2o-mono);
  font-size:var(--t2o-fs-xs);color:var(--t2o-fg-muted)}

/* Expanded detail */
.detail{margin:2px 16px 8px;background:var(--t2o-surface);
  border-left:2px solid var(--t2o-accent);padding:4px 0 4px 10px}
.detail-h{display:flex;align-items:center;gap:8px;margin-bottom:4px;
  font-size:var(--t2o-fs-2xs);font-weight:var(--t2o-fw-semibold);
  letter-spacing:var(--t2o-tracking-label);text-transform:uppercase;
  color:var(--t2o-fg-muted)}
.turn{display:grid;grid-template-columns:5.5rem 1fr;gap:10px;
  padding:2px 0;align-items:baseline}
.turn .when{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);
  color:var(--t2o-fg-muted);white-space:nowrap;font-variant-numeric:tabular-nums}
.turn .what{font-size:12px;line-height:1.5;overflow-wrap:anywhere}
.turn .what .k{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);color:var(--t2o-accent)}
.turn .what .tool{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);color:var(--t2o-fg-soft)}
.turn .what .e{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);color:var(--t2o-fail)}

/* Empty, loading and failure blocks */
.state{display:flex;align-items:flex-start;gap:10px;padding:18px 16px}
.state-b{display:flex;flex-direction:column;gap:5px}
.state-t{font-size:var(--t2o-fs-md);font-weight:var(--t2o-fw-semibold)}
.state-d{font-size:var(--t2o-fs-sm);line-height:1.6;color:var(--t2o-fg-soft);max-width:820px}
.state-m{font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs);color:var(--t2o-fg-muted)}
.loading,.empty{color:var(--t2o-fg-muted);padding:6px 16px;
  font-family:var(--t2o-mono);font-size:var(--t2o-fs-xs)}

/* Motion. Neither animation carries information on its own. */
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.45}}
@keyframes arrive{from{opacity:0;transform:translateY(-2px)}to{opacity:1;transform:none}}
.pulse{animation:pulse 2s ease-in-out infinite}
@media (prefers-reduced-motion: reduce){
  .pulse{animation:none}
  .row.arrived>.cell{animation:none}
}
```

Dropped entirely: `--shadow` and every `box-shadow` used for depth (the family has no elevation
shadows), `--run` / `--sub` / `--fork` (role hues, see §5), `--hover` as a row tint (see §6),
`border-radius:4px` on the theme button, `border-radius:50%` on the dot, `.nm.sub` / `.nm.forked`
colour overrides.

---

## 4. Header

```html
<header>
  <h1><span id="dot"></span>codexlb2otel live</h1>
  <span class="conn wait" id="conn">connecting</span>
  <span class="meta" id="counts"></span>
  <span class="meta" id="lag"></span>
  <button id="theme" type="button">dark</button>
</header>
```

- `#dot` becomes an inline SVG family status shape rather than a CSS circle: filled circle in
  `--t2o-ok` when connected, diamond in `--t2o-fail` when reconnecting, hollow ring in
  `--t2o-fg-muted` before the first frame. Built with `createElementNS`, swapped by replacing its
  child.
- `#status` becomes `#conn`, an outlined chip carrying the word: `connected` / `reconnecting` /
  `connecting`. The current implementation puts the connection state in a bare colour-only dot plus a
  lowercase word with an ellipsis; the chip makes it a first-class signal, which is right for a page
  whose entire value is being live.
- `#counts` keeps its text exactly: `18 threads · 4 running · 1 stalled`, and keeps
  `className = stalled ? 'meta warn' : 'meta'`.
- `#lag` keeps `refresh 5s`.
- `#theme` gains the `circle-half` icon and keeps its label as the theme it will switch TO, which is
  the existing behaviour and the less confusing of the two conventions.

`applyTheme` keeps its shape, with one change: the family default is light, so `applyTheme(null)`
removing the attribute now lands on light rather than on the OS preference. That matches this page's
original intent - the comment explaining why light is unconditional was right, and the family adopted
it. Keep the `try`/`catch` around `localStorage`; a theme toggle is not worth taking the page down
for, and the key stays `codexlb2otel-theme`.

---

## 5. Roles without role hues

The current page uses purple for a subagent and orange for a fork, applied to the thread name itself.
The family block has neither hue, and adding two would be the largest possible extension for the
smallest gain. Both become structural:

| Role | Was | Now |
|---|---|---|
| subagent | name in `--sub` (purple) | tree depth already says it; plus a neutral `sub` chip |
| fork | name in `--fork` (orange) + `fork` tag | `fork` chip with the Phosphor `git-fork` icon, `--t2o-fg-soft` |

The fork chip keeps its `title`, and it is worth keeping verbatim: a fork inherits its parent's entire
history, so anything it says about its own past is really the parent's. That is a claim about data
provenance, not a severity, which is exactly why it should not be coloured like a warning.

Thread names are all `--t2o-fg` at 12.5px/600 mono. Depth, the box-drawing prefix and the chips carry
the rest.

---

## 6. Request states

Four states, each a glyph plus a word plus a figure. Colour is never alone.

| State | Glyph | Colour | Text |
|---|---|---|---|
| running | filled circle, pulsing | `--t2o-ok` | the activity, e.g. `apply_patch index.html` |
| idle | hollow ring | `--t2o-fg-muted` | `idle 4m` |
| stalled | filled square | `--t2o-fail` | `no frames for 14m` |
| failed | diamond | `--t2o-fail` | `3 err · last 429 rate_limited` |
| content disabled | hollow ring | `--t2o-fg-muted` | `content withheld` |

- **The glyph moves out of the string, it is not added alongside it.** The current page bakes literal
  characters into the status text: `el("span","status live","● "+(t.activity||"running"))` and
  `"◼ no frames for "+quietOf(...)`. Strip both prefixes when the SVG shape is introduced, or the row
  renders the glyph twice. The status cell becomes an inline-flex of `[shape SVG][text]` with a 6px
  gap, and the text is the activity alone.
- **stalled says the measurement, not the verdict.** Keep `quietOf()` and keep the existing `title`,
  which explains that the row is still open but silent for longer than the configured stall threshold,
  measured on the archive's clock rather than wall clock. The threshold is a setting the reader cannot
  see, so the word `stalled` on its own is a claim they cannot check. The current `◼` glyph becomes
  the family's filled square, which is the same idea done with SVG.
- **content disabled** is currently rendered as `(content disabled)` in the message slot. Promote it
  into the status column as a state: the operator switched it off and nothing is broken, so it should
  not read as a missing message.
- A failed thread keeps its figures. It is still a thread that used tokens.

**The live-row treatment.** A row that appears in a snapshot it was not in before gets `.arrived`: a
2px `--t2o-accent` inset left marker and a 240ms fade with a 2px lift. The marker is dropped on the
next snapshot, so it says just-arrived rather than important. Implementation: keep a `seen` set of
`thread_id` beside `open` and `closed`, add the class when a row's id is not in it, and add the id
after render. Both animations stop under `prefers-reduced-motion`, and neither is the only carrier of
anything.

---

## 7. Empty, loading and failure states

Three different nothings, three different treatments. The current page has one message for all of
them (`waiting for the first snapshot…`, then `no activity in the retention window.`), which is close
but under-specified.

| State | Glyph | Title | Body |
|---|---|---|---|
| before the first frame | `circle-notch`, `--t2o-fg-muted` | Waiting for the first snapshot | The REST route is fetched once for the poll interval, then the page lives on the stream. Nothing has arrived yet, so there is nothing to show and nothing is wrong. |
| connected, nothing retained | `info`, `--t2o-fg-muted` | No activity in the retention window | The stream is live and the archive is readable; no conversation has been recorded inside the window. Rows appear on their own as traffic arrives, with no reload. |
| stream lost | `plugs`, `--t2o-fail` | Lost the stream | The tree below is the last snapshot received and is no longer being updated. EventSource retries on its own, so there is no action to take and no button to press; the header returns to connected when a frame lands. |

The third is the one that matters and the one the page currently under-states with
`reconnecting…` in the header alone. Stale-but-shown is the most dangerous state a live monitor has,
because it looks identical to a quiet system. Say it in the body, and keep the tree visible rather
than clearing it - clearing would lose the operator's expanded panels for a blip.

`loadDetail`'s failure keeps its message shape (`could not load turns: 404`) at
`.loading`/`.empty` styling. `renderTurns`'s `no retained turns` is unchanged.

---

## 8. AA measurements, new pairs

Family pairs are in `implementation-spec.md` §10. New to this console:

| Pair | Light | Dark |
|---|---|---|
| tree glyph `--t2o-fg-muted` on `--t2o-bg` | 5.35 | 6.86 |
| tree glyph `--t2o-fg-muted` on `--t2o-raised` (hovered row) | 5.97 | 5.82 |
| `--t2o-ok` on `--t2o-bg` (running status) | 4.52 | 6.82 |
| `--t2o-ok` on `--t2o-raised` (hovered running row) | 5.04 | 5.79 |
| `--t2o-fail` on `--t2o-bg` (stalled status) | 5.69 | 6.06 |
| `--t2o-fail` on `--t2o-raised` | 6.35 | 5.14 |
| `--t2o-fg` on `--t2o-bg` (message text) | 14.72 | 14.43 |
| `--t2o-fg-soft` on `--t2o-bg` (ask line) | 9.42 | 9.86 |
| `--t2o-accent` on `--t2o-surface` (turn `say` prefix, detail border) | 5.73 | 6.89 |
| `--t2o-fg-soft` on `--t2o-surface` (detail panel) | 9.87 | 9.16 |
| arrival marker `--t2o-accent` on `--t2o-bg` | 5.41 | 7.38 |

All pass AA, including the tightest, `--t2o-ok` on `--t2o-bg` at 4.52 in light.

The pulse animation drops opacity to .45 at its midpoint, which takes `--t2o-ok` on `--t2o-bg` to
roughly 2.0 at the trough. That is why the running state is a glyph plus the activity text and not a
colour: at the dimmest frame the row is still readable as running from its shape and its words. State
that in the code comment beside the keyframe so nobody later "fixes" the animation by deepening it.

One exception, non-text: `--t2o-fg-faint` for the message's opening and closing quotation marks, at
3.06 / 3.53. They are typographic punctuation around text that is itself `--t2o-fg` at 14.72.

Add these pairs to `tools/contrast_check.py`.

---

## 9. Phosphor set for this repo

Inline SVG built with `createElementNS`, never `innerHTML`. Six icons:

`git-fork` (a forked thread), `info` (nothing retained), `plugs` (stream lost), `circle-notch`
(waiting for the first frame), `circle-half` (theme toggle), `warning-circle` (the error count chip).

Plus four family status shapes: filled circle (running), hollow ring (idle, waiting), filled square
(stalled), diamond (failed, reconnecting). The filled square is new to the family; add it to the
shared inventory with the meaning "open but not progressing", distinct from the diamond's "finished
badly".

---

## 10. Assumptions

1. **Static font route.** This repo serves one embedded HTML file and has no static-asset route.
   Assumed a `/_static/fonts/` route beside `/api/stream`, honouring the same `token` query-param auth
   the other routes use, or exempt if the deployment is loopback-only. If neither is acceptable,
   base64 the two preloaded faces into the CSS - this file is already 19KB and self-contained, and
   another 60KB of woff2 is consistent with how it is shipped.
2. **`seen` set for arrival markers** is new client state. It lives beside `open` / `closed` / `quiet`
   outside the render, and is not persisted; a page reload therefore shows no arrival markers, which is
   correct.
3. **The `isTarget` hoisting bug** is assumed to be a bug rather than intentional. If `.target`
   highlighting is deliberately disabled, drop the `.row.target` rule instead of fixing the order.
4. **Stall threshold** is read from `snap.stall_after_ms` as today. The mockup shows 10m; the real
   value is whatever the archive reports.
5. **Turn cap** stays at 40, newest first, per `renderTurns`. The detail header states `4 of 40 shown`
   in the mockup; wire it to the real counts.
6. **`content` flag.** `snap.content` false currently produces `(content disabled)` in the message
   slot for every row. Moving it to the status column assumes it is a global switch rather than
   per-thread, which is how the current code reads it.
7. **No `just` target.** Everything here is CSS and JS inside one embedded file, so `make check` covers
   it. If the repo gains a contrast gate, it should point at the family
   `tools/contrast_check.py` rather than growing its own.
8. **Sample data** in the mockups is fabricated but shaped like real output: task-path thread names,
   `429 rate_limited` error codes, token counts in the 1.8M range, box-drawing prefixes at three
   levels of depth. No screen implies a field the snapshot does not carry.
