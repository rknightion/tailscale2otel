---
id: TSO-0084
title: Extend the compose secrets template to objectstore and Headscale credentials
status: Done
assignee: []
created_date: '2026-08-30 09:36'
updated_date: '2026-08-31 03:39'
labels: []
milestone: m-7
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
- [x] #1 Both credential families have secrets-template entries consistent with the existing pattern
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane K extends the Compose secrets template for Headscale and object-store file credentials and validates the resolved Compose contract.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lane K extended the Compose secrets template across Headscale and flow/audit/Kubernetes-audit object-store file credentials. The Compose self-test now asserts all 16 *_FILE variables; 67 checks passed.

Required CodeRabbit pre-commit review attempted on the integrated staged diff after just check passed; the service failed before analysis with recoverable  and emitted no  line. Treated as a failed review, not a clean result. Root manually reviewed the full staged diff and found no blocking issue; this is an overnight review-service deviation.

Correction to the preceding note: the exact recoverable error was WebSocket closed, and the review emitted no complete status line.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Extended the Compose secrets template to Headscale and flow, audit and Kubernetes-audit object-store credentials, with resolution coverage for all 16 file-backed variables. Implementation SHA d3af40f. Final integrated just check passed at 5b55617; exact-head CI run 33354208183 completed success.
<!-- SECTION:FINAL_SUMMARY:END -->
