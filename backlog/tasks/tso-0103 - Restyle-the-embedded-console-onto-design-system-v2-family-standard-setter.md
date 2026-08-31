---
id: TSO-0103
title: Restyle the embedded console onto design system v2 (family standard-setter)
status: To Do
assignee: []
created_date: '2026-08-31 12:12'
labels:
  - design-system
dependencies: []
ordinal: 104000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The complete v2 design is committed at design/console-v2/: Console v2 canvas (plus a recreation of today's console for orientation), the family canvases for the sibling repos, implementation-spec.md (THE FAMILY SPEC - its section 1 is the shared token block, byte-identical across tailscale2otel, opnsense2otel, graph2otel and codexlb2otel; copy it, never edit it per repo), per-sibling specs, and internal/ holding draft restyled templates including this repo's statushtml and eventshtml pages - treat drafts as reference, not as finished code. Read the family spec in full before any code change.

Scope: Go html/template + inline CSS/vanilla JS + go:embed stays; no framework, no build step, no CDN, no external network request of any kind. Fonts self-hosted (hanken-grotesk-latin/-ext woff2 + JetBrains Mono variable, latin preloaded). Light default honouring prefers-color-scheme, existing data-theme toggle and localStorage key kept and winning. Underline tabs, word+shape health badge in the header, dense-table standard (hairline rules, sticky header, right-aligned mono numerics, row hover, 36px token rows). tx/rx uses the accent-vs-neutral-ink lightness pair with inline labels, never hue alone. Page-scoped exports stay with their tables; support bundle moves to Config; pause/refresh stays in shell chrome. ARIA tab roles preserved. Log tail remains endpoint-only (spec carries a treatment note for a future screen). This repo sets the family standard: land it first, and any extension to the shared token block made here must be flagged so the sibling repos inherit it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 all three pages (status console, flow explorer, events explorer) render on the family token block, light and dark, light default
- [ ] #2 the shared token block matches spec section 1 byte-for-byte
- [ ] #3 tx/rx treatment is lightness+label based per spec; AA pairs hold in both themes
- [ ] #4 no external network requests; fonts self-hosted; ARIA tab roles intact
- [ ] #5 diagnostic actions relocated per spec (exports with tables, support bundle in Config)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
- [ ] #4 just check green
<!-- DOD:END -->
