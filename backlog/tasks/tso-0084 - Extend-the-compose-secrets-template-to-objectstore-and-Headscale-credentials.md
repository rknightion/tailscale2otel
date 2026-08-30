---
id: TSO-0084
title: Extend the compose secrets template to objectstore and Headscale credentials
status: To Do
assignee: []
created_date: '2026-08-30 09:36'
labels: []
dependencies: []
priority: low
ordinal: 87000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
deploy/docker-compose.secrets.yaml omits the objectstore and Headscale _file credential variants even though the app contract supports them. Add both, matching the existing template style.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both credential families have secrets-template entries consistent with the existing pattern
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
