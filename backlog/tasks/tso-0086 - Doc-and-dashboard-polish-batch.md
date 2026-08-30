---
id: TSO-0086
title: Doc and dashboard polish batch
status: Done
assignee:
  - '@codex'
created_date: '2026-08-30 09:36'
updated_date: '2026-08-30 16:32'
labels: []
milestone: m-7
dependencies: []
priority: low
ordinal: 89000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Five small items in one pass: (1) surface recording-rule _desc somewhere operator-visible - ts2o-rec-* rules are undocumented in the Grafana UI (deploy/alerts/gen/build_rules.py:446); (2) dedupe the near-verbatim Grafana 13+/naming prose between docs/dashboards.md:104-148 and deploy/grafana/README.md; (3) add an instance/environment dashboard variable for fleets running multiple exporter installs into one stack; (4) k8saudit panel description stating status/latency structurally cannot exist in the tsrecorder feed (deploy/grafana/gen/tabs/k8saudit.py) to pre-empt bug reports; (5) a docker-compose.no-admin.yaml override for the healthcheck-vs-admin-disabled footgun (deploy/docker-compose.yaml ~55-64). Regenerate all touched artifacts.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All five items addressed or individually noted as dropped with a reason
- [x] #2 Generated artifacts (dashboards, alert docs, helm README) regenerated where touched
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
C1: resolve the orphaned tab with a negative-tested generator guard; complete the five polish items; regenerate every affected family; return focused-check evidence without tracker writes or commits.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Latitude deviation: the run contract called for one commit per feature, but root retained the already-integrated shared-tree feature commit fa6a465 plus review-fix commit a18a5dd rather than performing prohibited destructive history surgery after integration and push. All task evidence is tied to the verified implementation head a18a5dd06f9ac9c8b84fda73bba653ded2398d5a.

Latitude deviation: lane C1 was interrupted, so root completed the dashboard-control work. The visible-control budget was raised from 6 to 7 only for the fleet-wide multi-install filter, preserving the bounded-control contract.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed the documentation and generated-dashboard polish batch, including the fleet-wide multi-install selector and bounded panel/control guards. Verified by negative-tested dashboard tests, exact GitSync blob readback, final just check, and exact-head CI run 33322449434 at a18a5dd06f9ac9c8b84fda73bba653ded2398d5a (success).
<!-- SECTION:FINAL_SUMMARY:END -->
