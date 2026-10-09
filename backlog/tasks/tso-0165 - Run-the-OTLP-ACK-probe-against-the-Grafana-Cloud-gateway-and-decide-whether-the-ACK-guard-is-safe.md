---
id: TSO-0165
title: >-
  Run the OTLP ACK probe against the Grafana Cloud gateway and decide whether
  the ACK guard is safe
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:27'
updated_date: '2026-10-09 23:13'
labels: []
dependencies:
  - TSO-0164
priority: high
type: chore
ordinal: 165000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
This is the live half of TSO-0164 (the OTLP ACK probe recipe). Rob authorised it on 2026-10-09: one run of just probe-otlp-ack against the OTLP gateway of the lab Grafana Cloud stack named in LOOP.md, at most 3 requests in total, using the stack's OTLP write credential read by path. This repository is public, so the task records the shape only (status, Content-Type, body length, protobuf yes or no), never the endpoint tenant, the user or the credential. If either signal returns a non-empty, non-protobuf 2xx, file a high-priority bug for the ACK guard with that evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The probe ran once against the gateway; per-signal status, Content-Type, body length and protobuf-decodable result are recorded in this task's notes with no identifiers
- [ ] #2 If any 2xx body is non-empty and not protobuf, a high-priority bug task exists citing this evidence; otherwise the notes state that the guard is safe against this gateway
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Run exactly one allowlisted first-token probe on reviewed landed code, count emitted signal requests, allow fallback only on 401/403 and only if first run consumed one request, otherwise abort before exceeding three. Record public-safe ACK shapes and file a guard bug only for nonempty nonprotobuf 2xx.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Live ACK probe on reviewed landed SHA d5f023fadc688a486d8ea9f9d9b33bfd23d92e81, once, exit 0, two requests. Logs: status 204, Content-Type empty, body length 0, protobuf-decode true. Metrics: status 200, Content-Type application/x-protobuf, body length 0, protobuf-decode true. First credential succeeded, no fallback and no additional request. Neither response has a nonempty nonprotobuf 2xx body, so the guard is safe against these observed gateway ACK shapes. This proves ACK response shape only, not backend ingestion, partial acceptance semantics or future availability; no guard bug is warranted by this evidence. No endpoint/user/token/body captured in tracked notes.
<!-- SECTION:NOTES:END -->
