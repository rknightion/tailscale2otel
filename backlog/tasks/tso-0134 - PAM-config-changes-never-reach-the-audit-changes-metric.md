---
id: TSO-0134
title: PAM config changes never reach the audit-changes metric
status: Done
assignee:
  - '@codex'
created_date: '2026-09-04 10:36'
updated_date: '2026-09-04 22:25'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 135000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live-verified 2026-09-04 against a real PAM deployment on the lab tailnet. Nine BORDER0_API audit events (PAM_SERVICE_ACCOUNT, PAM_CONNECTOR and PAM_SERVICE creates, one update, one delete) were polled and counted correctly on tailscale.config.audit.events (origin=BORDER0_API, CREATE=7 UPDATE=1 DELETE=1 confirmed in Grafana Cloud), but produced ZERO increments on tailscale.config.audit.changes.

Cause: classifyChange keys on a curated target.property or on the device-churn / api-key type+action rules. A PAM event carries target.type PAM_SERVICE / PAM_CONNECTOR / PAM_SERVICE_ACCOUNT with no curated property, so it falls through. Consequence: the PAM_CONNECTOR and PAM_SERVICE_ACCOUNT entries TSO-0087 added to knownActorTypes in internal/audit/classify.go are unreachable in practice, because normalizeActorType is only called from the changes path.

Second, related gap: enabling PAM emits target.type=TAILNET, target.property=BORDER0_PROVISIONING, action=ENABLE. BORDER0_PROVISIONING is absent from the vendored spec entirely, so it is in neither propertyCategories nor propertyExclusions and the taxonomy_test schema-drift guard cannot fail on it. A third party being granted tenant-wide provisioning is exactly the kind of change the changes metric is for.

Decide whether PAM deserves its own curated change category (pam_service / pam_connector / pam_credential) or whether BORDER0_PROVISIONING alone is enough, then add the guard so the next unspecced property is caught.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A PAM config change made through the Border0 API increments tailscale.config.audit.changes with a bounded, curated category
- [x] #2 BORDER0_PROVISIONING is classified or explicitly excluded with a stated reason
- [x] #3 A test proves normalizeActorType is reachable for PAM_CONNECTOR and PAM_SERVICE_ACCOUNT
- [x] #4 The drift guard fails on a live target.property that is absent from the vendored spec
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-04 Wave 12 implementation: classified PAM_SERVICE, PAM_CONNECTOR and PAM_SERVICE_ACCOUNT as bounded pam_service, pam_connector and pam_service_account categories because live object lifecycle events carry no target.property and target type is the stable discriminator. Classified BORDER0_PROVISIONING as pam_provisioning. Added a separate live-only-property taxonomy ledger so an observed property absent from the vendored schema must still be categorized or explicitly excluded. Negative-tested the guard by removing the category and observing the intended failure, then restored it. Focused audit tests pass.

2026-09-04 Wave 12 completion evidence:
- Audit fix commit f339e173e28afc4f671670f0f10d1f9d0e691b9b.
- Exact-head CI run 33923725959 attempt 1 succeeded; Auto-RC run 33924342484 attempt 1 succeeded.
- just check passed; just gen left no drift; just --fmt --check passed; final CodeRabbit review completed with zero findings.
- PAM service, connector, service-account, and provisioning changes now use bounded curated categories. Tests prove PAM actor normalization is reachable.
- The live-only target-property ledger makes the taxonomy guard fail when an observed property is absent from the vendored specification and lacks an explicit classification or exclusion. The guard was negative-tested before restoration.
- Verbose test inventory contained only the three established skips: TestRuleCountsFromRealACL because its optional capture was absent, TestDumpFlowsJSON because FLOWSJSON_DUMP was unset, and TestDefaultCheckpointPath_XDGStateHomeWins because it is inapplicable on Darwin.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed PAM audit change classification with bounded categories for PAM object lifecycle and provisioning events, made PAM actor normalization reachable, and added the durable live-only property drift guard. Verified with focused negative testing, the full local gate, exact-head CI, and completed CodeRabbit review.
<!-- SECTION:FINAL_SUMMARY:END -->
