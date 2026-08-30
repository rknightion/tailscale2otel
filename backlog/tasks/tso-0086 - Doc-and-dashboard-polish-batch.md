---
id: TSO-0086
title: Doc and dashboard polish batch
status: To Do
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-30 09:48'
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
- [ ] #1 All five items addressed or individually noted as dropped with a reason
- [ ] #2 Generated artifacts (dashboards, alert docs, helm README) regenerated where touched
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
