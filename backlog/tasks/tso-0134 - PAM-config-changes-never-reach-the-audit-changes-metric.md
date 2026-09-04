---
id: TSO-0134
title: PAM config changes never reach the audit-changes metric
status: To Do
assignee: []
created_date: '2026-09-04 10:36'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 135000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live-verified 2026-09-04 against a real PAM deployment on the m7kni.io tailnet. Nine BORDER0_API audit events (PAM_SERVICE_ACCOUNT, PAM_CONNECTOR and PAM_SERVICE creates, one update, one delete) were polled and counted correctly on tailscale.config.audit.events (origin=BORDER0_API, CREATE=7 UPDATE=1 DELETE=1 in Grafana Cloud), but produced ZERO increments on tailscale.config.audit.changes.

Cause: classifyChange keys on a curated target.property or on the device-churn / api-key type+action rules. A PAM event carries target.type PAM_SERVICE / PAM_CONNECTOR / PAM_SERVICE_ACCOUNT with no curated property, so it falls through. Consequence: the PAM_CONNECTOR and PAM_SERVICE_ACCOUNT entries TSO-0087 added to knownActorTypes in internal/audit/classify.go are unreachable in practice, because normalizeActorType is only called from the changes path.

Second, related gap: enabling PAM emits target.type=TAILNET, target.property=BORDER0_PROVISIONING, action=ENABLE. BORDER0_PROVISIONING is absent from the vendored spec entirely, so it is in neither propertyCategories nor propertyExclusions and the taxonomy_test schema-drift guard cannot fail on it. A third party being granted tenant-wide provisioning is exactly the kind of change the changes metric is for.

Decide whether PAM deserves its own curated change category (pam_service / pam_connector / pam_credential) or whether BORDER0_PROVISIONING alone is enough, then add the guard so the next unspecced property is caught.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A PAM config change made through the Border0 API increments tailscale.config.audit.changes with a bounded, curated category
- [ ] #2 BORDER0_PROVISIONING is classified or explicitly excluded with a stated reason
- [ ] #3 A test proves normalizeActorType is reachable for PAM_CONNECTOR and PAM_SERVICE_ACCOUNT
- [ ] #4 The drift guard fails on a live target.property that is absent from the vendored spec
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->
