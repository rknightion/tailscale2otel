---
id: TSO-0189
title: Keep device OS pie-chart labels readable at panel snapshot dimensions
status: In Progress
assignee:
  - '@loop31'
created_date: '2026-10-11 17:15'
updated_date: '2026-10-13 09:19'
labels:
  - needs-triage
dependencies: []
priority: low
type: bug
ordinal: 189000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
During TSO-0172 (live dashboard audit), an independently inspected published Devices by OS panel at 800x600 rendered a populated pie with a readable right-side legend, but long in-chart labels overlapped and one clipped at the left boundary; small-slice percentage annotations were partly obscured. This is a layout readability issue, not a query error. No lab labels or values are needed to reproduce it. Filed during loop28; not admitted to its frozen envelope.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A synthetic multi-category fixture with long OS labels and small slices keeps every category readable at 800x600 and normal dashboard width.
- [ ] #2 The visualization makes the percentage-versus-count interpretation explicit without clipped or overlapping annotations.
- [ ] #3 Retain the existing query and signal coverage and capture before/after rendered fixture evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use long-label multi-category small-slice synthetic fixture to capture baseline real Grafana pie at 800x600 and normal width; adjust native legend/labels to readable explicit percentage-vs-count semantics preserving query/coverage; capture after renders, regenerate/stage, serialized G, guarded review/CodeRabbit then root composed/CI/delivery.

loop31: implement only the frozen owner-approved seam in isolated ownership; retain exact candidate and behavior evidence, named gate where applicable; independent guarded review then root integration/CI/delivery. Read-only diagnosis has no remediation or code gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop29: 0 implementation attempts; undispatched dependency park. TSO-0172 (comprehensive live dashboard audit) cannot start before 2026-10-11T21:05:15Z under its required minimum fresh window and has not landed. Resume only after that audit is accepted, landed and composed/CI/delivery green, then preceding frozen chain tasks settle. No candidate work or live call occurred.

loop30: 2 implementation attempts, no rescues or reviews; candidate uncommitted/unlanded. Native bottom Value/Percent legend, no in-slice labels, height adjustment and real query-backed 800x600/native-grid closed-navigation render proof succeed, but full G exits1 on test_specialist_panels.FleetAndRelationshipPanelContracts.test_fleet_composition_panels_are_native_pies exact default pie options and test_unit_consistency.DecimalsAndLegendConventionTest.test_no_panel_sets_an_explicit_decimals_override. Frozen shared-test edit fence blocks intended OS-specific contract changes; no test/helper bypass or half-land. Park needs owner approval of narrowly revised OS option/precision contracts, then width/reflow proof with expanded navigation, full gate and fresh reviews. Two attempts remain under current ceiling. Full G output tail only retained; complete narrow failure log and candidate/archive/final-03 render receipts remain /Users/rob/repos/tailscale2otel/codex/loop30/tso0189-retry/. No live mutation. Main remains green; parked candidate G red is not main red.

loop31 preparation, Rob 2026-10-13: recommission approved. Previous blocker was the frozen OS pie options/precision test fence. Changed premise: OS-only explicit bottom Value/Percent legend, no slice annotations and precision=1 expectations are now authorized; Compliance distribution and all other precision guards remain unchanged. Discriminating check: retained query-backed fixture at 800x600 and actual native grid with navigation expanded and closed, followed by full gate and fresh review. Lifetime implementation attempts remain 2 of 4; remaining ladder is rescue-astra-medium then rescue-astra, no reset or ceiling increase. Task remains Parked until launch admission.

loop31: rescue attempt3 reaches exact query-backed expanded-navigation width failure, despite green candidate gate. Native grid OS width falls 624px to504px, legend339px cannot fit374-454px text; all seven labels clip. Closed grid and800x600 solo pass. Actual Prometheus/Grafana response receipts distinguish scope/state categories. Final rescue attempt4 dispatched for evidence-based width/reflow, no test weakening; solo has no visible application nav, native-grid must prove both nav states.
<!-- SECTION:NOTES:END -->
