---
id: TSO-0164
title: >-
  OTLP ACK probe recipe: record what a gateway returns on a successful HTTP
  export
status: In Progress
assignee:
  - '@loop20'
created_date: '2026-10-09 22:26'
updated_date: '2026-10-09 22:32'
labels: []
dependencies: []
priority: high
type: chore
ordinal: 164000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The HTTP OTLP ACK guard (frozen TSO-0148 design section 14) fails closed on a 2xx with a non-empty body that is not protobuf, including a missing Content-Type or a proxy that rewrites the body. All the evidence is from synthetic servers. If the Grafana Cloud gateway can return such a 2xx, every HTTP deployment drops logs and stalls WAL commits. Rob approved on 2026-10-09 a reusable probe that can be rerun before each release. The recipe reads OTLP_ACK_PROBE_ENDPOINT, OTLP_ACK_PROBE_USER and OTLP_ACK_PROBE_TOKEN_FILE from the environment, sends one OTLP/HTTP protobuf log record and one gauge with service.name=tailscale2otel-ackprobe, and prints for each signal only the status code, the Content-Type, the body length and whether the body decodes as the matching Export*ServiceResponse protobuf. It never prints the token, the URL credentials or the body.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 just probe-otlp-ack exists with a doc comment and a group, reads its credentials only from the three env vars (token from the file path), and exits non-zero if any are missing
- [ ] #2 Tests against httptest servers cover a protobuf 2xx, an empty 2xx, a JSON 2xx and a missing Content-Type, and assert the printed fields; the token never appears in output
- [ ] #3 The probe code builds under go build ./... and go vet ./... in the root module, and is covered by just check
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement local-only ACK probe with fixture evidence; exact candidate gate and security review before landing.
<!-- SECTION:PLAN:END -->
