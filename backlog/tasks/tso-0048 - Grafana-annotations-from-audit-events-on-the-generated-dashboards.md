---
id: TSO-0048
title: Grafana annotations from audit events on the generated dashboards
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:27'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-2
dependencies: []
priority: low
ordinal: 51000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Audit logs already land in Loki; add annotation queries to the generated dashboards (deploy/grafana/gen) overlaying key config-change events - ACL changed, device added/deleted, key created - on the tailnet graphs. Pure dashboard-generator work: no exporter changes. Verify the annotation query shape works in the Grafana v2 schema the generator emits, and regenerate artifacts (gen-dashboards + counts + promqlcheck leg).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Generated dashboards carry Loki annotation queries for at least ACL change, device add/delete and key creation
- [x] #2 Artifacts regenerated; drift and promqlcheck gates green
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Read `internal/annotations/rules.go` and `internal/annotations/catalog.go` to learn how an existing category is classified, tagged and counted. Mirror it exactly; do not invent a second shape.
2. Add the three categories to `AnnotationCategories` in `internal/config/annotations.go`, defaulted per the table below, and to `config.example.yaml:759-766`. Then the four config seams and `just gen-config-schema gen-envref gen-helm`.
   - `policy_change`: enabled true, rollup false (an ACL edit is rare and individually meaningful; rolling it up hides which revision).
   - `inventory`: enabled true, rollup TRUE (device churn is the highest-volume of the three).
   - `risk`: enabled true, rollup false (a new risk finding is rare and each one matters).
3. Add one `tag_annotation(...)` layer per category in `build.py:79 annotation_layers()`, following the existing three exactly - same colour discipline, same `enable`/`hide` posture. Read the comment at build.py:57-69 first: it explains why each layer carries BOTH the root tag and its category tag, and why `matchAny` must stay false.
4. Wire the emit sites. They belong to other lanes (TSO-0044/0045 policy, TSO-0047 device changes, TSO-0049 risk), so this lane defines the category + classification + layer and RETURNS the exact call-site signature each of those lanes must call. Do not edit their files.
5. Regenerate `just gen-dashboards gen-counts` and confirm the drift gate is clean.
6. Verification: the annotation path is not exercised by `just check` end to end. Prove the classification with a unit test in `internal/annotations` for each new category (an event of that shape produces an annotation with the expected tags), and prove the layer with the generator drift gate. Say explicitly that no live Grafana write was performed.
7. AC#1 on this task names Loki annotation queries and is now WRONG. When finalizing, note in the final summary that AC#1 was satisfied by the annotation-store route instead, per the owner decision above, and check it - do not silently leave it unchecked or quietly reinterpret it without saying so.

Wave 2 root freeze plan: add policy_change, inventory, and risk annotation category config with owner-frozen defaults, then regenerate the four config-derived artifact families.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Direction changed by owner decision, 2026-08-30 (Wave 2 planning)

**The task as filed is superseded in its MECHANISM but kept in its INTENT.** Do not add Loki annotation queries to the generated dashboards.

Research at 3e9b937: `deploy/grafana/gen/build.py:51-96` already emits three annotation layers, and they read **Grafana own annotation STORE**, not a datasource. Its own comment says so: "tailscale2otel pushes the markers itself when `grafana_annotations.url` is configured (internal/annotations, #518), so there is no PromQL or LogQL here and nothing to promqlcheck." The layers today are `Tailnet events` (tag `tailscale2otel`), `- config changes only` (`category:config_change`) and `- key expiry only` (`category:expiry`).

The existing `config_change` category already covers this task AC#1 list verbatim - `config.example.yaml:761` describes it as "ACL edits, device approval and churn, key lifecycle, user role changes, DNS and tailnet settings - the curated audit-log subset".

Adding a parallel Loki-query route would double-mark every one of those events on the same dashboard, and would lose the dedupe set, the rollup bucketing and the token-bucket rate limit that `internal/annotations` provides. Owner decision (2026-08-30): **keep one delivery mechanism and grow its coverage instead.**

## Revised scope

Add annotation CATEGORIES for the state-change events Wave 2 introduces, so the new signals get markers through the mechanism that already exists:

- `policy_change` - ACL revision observed (and, once TSO-0045 lands, the diff)
- `inventory` - device added / removed / materially changed (from TSO-0047)
- `risk` - a new ACL risk finding appears (from TSO-0049)

Each follows the existing `{enabled, rollup}` shape at `config.example.yaml:759-766`, and each needs its `rules.go` classification, its tag, and a `tag_annotation(...)` layer in `build.py:79 annotation_layers()`.

Note `build.py` is a SHARED generator file - `deploy/grafana/gen/build.py` is on the single-owner list. This lane owns it for the run; no other lane may edit it concurrently.

Latitude deviation: the goal described six hand-maintained config files, but the live TestDocsConfigurationMentionsEveryKey gate proved docs/configuration.md is a seventh required config surface. Added the affected reference entries rather than weakening or bypassing the guard.

Frozen-decision interpretation: AC#1's Loki-query wording is superseded. The implementation extends Grafana annotation-store categories with policy_change, inventory, and risk so it retains existing dedupe, rollup, and rate-limit behavior and avoids double-marking. Negative-tested the Go classifier guard by changing the inventory event seam: TestWave2RulesClassifyStoreEvents/device_added failed because EventName was tailscale.device.change.negative-test instead of tailscale.device.change; restored and the focused test passed.

Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Reinterpreted AC1 through the existing annotation-store pipeline rather than duplicate Loki queries: added policy_change, inventory, and risk categories with tagging, deduplication, and generated layers. The task premise was superseded by the already-shipped annotation store. Verified by classification/layer tests, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
