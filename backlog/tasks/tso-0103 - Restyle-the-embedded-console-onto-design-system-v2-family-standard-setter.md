---
id: TSO-0103
title: Restyle the embedded console onto design system v2 (family standard-setter)
status: Done
assignee: []
created_date: '2026-08-31 12:12'
updated_date: '2026-09-01 19:10'
labels:
  - design-system
milestone: m-9
dependencies: []
priority: high
ordinal: 104000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The complete v2 design is committed at design/console-v2/: Console v2 canvas (plus a recreation of today's console for orientation), the family canvases for the sibling repos, implementation-spec.md (THE FAMILY SPEC - its section 1 is the shared token block, byte-identical across tailscale2otel, opnsense2otel, graph2otel and codexlb2otel; copy it, never edit it per repo), per-sibling specs, and internal/ holding draft restyled templates including this repo's statushtml and eventshtml pages - treat drafts as reference, not as finished code. Read the family spec in full before any code change.

Scope: Go html/template + inline CSS/vanilla JS + go:embed stays; no framework, no build step, no CDN, no external network request of any kind. Fonts self-hosted (hanken-grotesk-latin/-ext woff2 + JetBrains Mono variable, latin preloaded). Light default honouring prefers-color-scheme, existing data-theme toggle and localStorage key kept and winning. Underline tabs, word+shape health badge in the header, dense-table standard (hairline rules, sticky header, right-aligned mono numerics, row hover, 36px token rows). tx/rx uses the accent-vs-neutral-ink lightness pair with inline labels, never hue alone. Page-scoped exports stay with their tables; support bundle moves to Config; pause/refresh stays in shell chrome. ARIA tab roles preserved. Log tail remains endpoint-only (spec carries a treatment note for a future screen). This repo sets the family standard: land it first, and any extension to the shared token block made here must be flagged so the sibling repos inherit it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 all three pages (status console, flow explorer, events explorer) render on the family token block, light and dark, light default
- [x] #2 the shared token block matches spec section 1 byte-for-byte
- [x] #3 tx/rx treatment is lightness+label based per spec; AA pairs hold in both themes
- [x] #4 no external network requests; fonts self-hosted; ARIA tab roles intact
- [x] #5 diagnostic actions relocated per spec (exports with tables, support bundle in Config)
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
- [x] #4 just check green
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
- Preserve the family-spec shared token block byte-for-byte while adapting the reference design to the real embedded console templates.
- Restyle every embedded console page with self-hosted fonts, retained theme/storage behavior and ARIA tabs, and accessible tx/rx labels.
- Add contract tests, negative-test guards, run targeted render checks, and report any shared-token extension explicitly without committing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented the v2 family console across status, flow, and events templates with the shared token block byte-identical to design/console-v2/implementation-spec.md section 1. Embedded Hanken Grotesk latin/latin-ext and JetBrains Mono WOFF2 assets are served through a fixed-name allowlist at /_static/fonts/, with font/woff2, CSP font-src self, no external requests, retained theme/localStorage precedence, ARIA tabs, and labelled lightness-distinct tx/rx treatment. Diagnostic actions remain page-scoped and support bundle remains under Config.

Negative guards: changing one family token made TestFamilyTokenBlockMatchesTheSpec fail; accepting an unknown font made TestFontAllowsOnlyThePublishedConsoleFiles fail; widening the narrow rollup made TestConsoleV2PagesKeepTheOfflineThemeAndFontContract fail; removing the route/CSP integration made the root route/header tests fail. Each break was restored. Focused race tests passed. CodeRabbit findings for narrow rollup, tx/rx label collision, RX legend distinction, color-scheme, and banner distinction were fixed; two subsequent sharded reviews completed with zero finding events. just check passed. Shared token extension: none.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Restyled all three embedded console pages onto design system v2 with byte-identical shared tokens, self-hosted fonts, offline CSP, retained theme and ARIA contracts, accessible labelled tx/rx treatment, and relocated diagnostics. Negative guards, focused race tests, two clean sharded reviews and just check passed; shared token extension: none.
<!-- SECTION:FINAL_SUMMARY:END -->
