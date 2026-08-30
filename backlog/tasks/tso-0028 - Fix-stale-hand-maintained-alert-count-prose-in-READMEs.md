---
id: TSO-0028
title: Fix stale hand-maintained alert-count prose in READMEs
status: To Do
assignee: []
created_date: '2026-08-30 08:44'
updated_date: '2026-08-30 10:03'
labels: []
milestone: m-1
dependencies: []
priority: medium
type: bug
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
README.md:192 claims "77 of 78" alert rules link a canonical dashboard panel and deploy/alerts/README.md:138 claims "92 of the 100"; a direct count of deploy/alerts/grafana-managed/*.json finds 96 rules carrying __panelId__. Both sentences are hand-maintained in a repo that drift-gates every other generated fact, and they disagree with each other and with the artifacts. Found during a product-surface review (2026-08-30); counts verified against the generated manifests, root cause not yet investigated. Fix should make the numbers generated (from deploy/alerts/gen/build_rules.py) or CI-asserted rather than hand-edited.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both prose counts match the generated rule manifests
- [ ] #2 The counts are produced by the generator or asserted by a drift/CI check so they cannot silently rot again
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add `PanelLinkedAlertRules int `json:"panel_linked_alert_rules"`` to the `capabilityCounts` struct (internal/catalog/capability_counts_test.go:19). Derive it in `deriveCapabilityCounts` in the SAME `grafana-managed` glob loop already there: for `kind == "AlertRule"`, increment when `spec.annotations.__panelId__` is present and non-empty. `manifestKind` currently decodes only `{Kind string}`; widen that decode (or add a sibling helper) rather than reading each file twice.
2. Add `"panel_linked_alert_rules"` to `COUNT_KEYS` in scripts/check-capability-counts.py (line 14). `load_counts` asserts `set(raw) == COUNT_KEYS` exactly, so the key set and the JSON must move together or every count check fails.
3. Add two `SummaryPattern` rows, each with BOTH named groups so the numerator and denominator are gated together:
   - README.md: `r"(?P<panel_linked_alert_rules>\\d+) of (?P<alert_rules>\\d+) link a canonical dashboard panel"`
   - deploy/alerts/README.md: `r"(?P<panel_linked_alert_rules>\\d+) of the (?P<alert_rules>\\d+) carry the"`
4. Rewrite both sentences to the true values (96 / 100, and 96 of the 100).
5. `just gen-counts` to regenerate internal/catalog/capability_counts.json, then `just gen` and confirm no further diff.
6. NEGATIVE-TEST the new gate (doc-0002 recurring defect: guard tests that pass while asserting nothing). Change one prose number by hand, run `python3 scripts/check-capability-counts.py`, watch it fail naming that file and key, then restore. Also confirm the regex matches exactly once per file — `check_summaries` errors on 0 or 2+ matches, and a loose `\\d+ of \\d+` would match elsewhere.
7. Gate: `just check`. TDD note — this is a generator/gate change, so the failing-first evidence is step 6 (the deliberate wrong number going red), not a new Go unit test.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — CONFIRMED

Counted directly from the shipped manifests:

```
kinds  {RecordingRule: 23, AlertRule: 100, Folder: 1}   (124 files incl. _folder.json)
panel  {yes: 96, no: 4}     AlertRules carrying spec.annotations.__panelId__
runbook{yes: 100}           AlertRules carrying runbook_url
```

- `README.md:192` — "77 of 78 link a canonical dashboard panel". WRONG on both numbers; truth is 96 of 100.
- `deploy/alerts/README.md:138` — "92 of the 100 carry the `__dashboardUid__`/`__panelId__` annotation pair". Denominator right, numerator wrong; truth is 96.
- `__dashboardUid__` and `__panelId__` are always set together (96 files each), so a single count covers the pair.
- The "100 alert rules and 23 recording rules" figures in the same README paragraph are already CORRECT and already gated — do not touch them.

## Why it rotted, and the mechanism to reuse

`scripts/check-capability-counts.py` already gates every other public count. Its `SUMMARY_PATTERNS` table (script lines 28-62) holds one regex per prose sentence with named groups keyed to `internal/catalog/capability_counts.json`; `check_summaries` requires EXACTLY ONE match per pattern and compares each named group to the source. The source itself is derived in Go by `deriveCapabilityCounts` (`internal/catalog/capability_counts_test.go:63`), which globs `deploy/alerts/grafana-managed/*.json` and counts by `kind`. Neither sentence was ever added to that table, which is the entire bug.
<!-- SECTION:NOTES:END -->
