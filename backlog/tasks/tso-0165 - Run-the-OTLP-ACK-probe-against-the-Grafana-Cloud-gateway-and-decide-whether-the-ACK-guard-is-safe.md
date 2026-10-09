---
id: TSO-0165
title: >-
  Run the OTLP ACK probe against the Grafana Cloud gateway and decide whether
  the ACK guard is safe
status: To Do
assignee: []
created_date: '2026-10-09 22:27'
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
