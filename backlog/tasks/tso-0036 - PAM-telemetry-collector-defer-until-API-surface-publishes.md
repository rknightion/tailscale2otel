---
id: TSO-0036
title: PAM telemetry collector (defer until API surface publishes)
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-09-04 11:00'
labels: []
milestone: m-8
dependencies: []
priority: low
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale PAM went beta 2026-08-26 (Border0 acquisition); PAM service accounts call a PAM API but no endpoints are in the published OpenAPI spec yet. We have no PAM setup, so this is spec-driven only: placeholder tracking task - when PAM endpoints appear in the vendored spec (the daily api-drift lane will surface them), design a collector for session counts/durations by service type, JIT access-request rates and recording-storage settings. Do not build ahead of the spec.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Revisited when PAM operations appear in spec/tailscale-api.json; scope defined then
- [ ] #2 Until then the operations (when they appear) get explicit dispositions rather than sitting unadjudicated
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Upstream check 2026-09-01 against the vendored spec/tailscale-api.json (60 paths): zero paths matching pam/border0/session/recording. PAM operations still absent, so this stays correctly blocked on upstream and is excluded from Wave 5. Re-check is free: the daily api-drift lane surfaces new paths, or rerun the same path scan after any spec re-vendor.

Parked 2026-09-03 by owner decision. Blocked on Tailscale publishing a PAM telemetry API surface; no wave can drain it. Resume boundary: when the Tailscale API spec re-vendor (spec/tailscale-api.json) first carries PAM endpoints, move back to To Do and scope a collector against them. Parked rather than left in To Do so the board reads as genuinely drained, which is what the v5 trigger keys on.

2026-09-04 UNPARKED. The premise was wrong: PAM does have a published API surface, just not on api.tailscale.com. Verified live against a real PAM deployment (connector 'camden' on /opt/compose/tailzero, service 'pam-sandbox' against a throwaway openssh container, one successful recorded SSH session as rob@m7kni.io).

Base URL https://api.border0.com/api/v1, bearer token; creds in chat-personal/tailscale/.secrets/creds.local.env as BORDER0_API / BORDER0_TOKEN / BORDER0_ORG_ID / BORDER0_SERVICE_ACCOUNT_ID. Service-account JWT has no exp claim.

Endpoints that exist (200): GET /connectors, GET /connector/{id}, GET /sockets, GET /socket/{id}, GET /socket/{id}/connectors, GET /socket/{id}/upstream_configurations, GET /policies, GET /organizations/iam/service_accounts, and crucially GET /sessions plus GET /socket/{id}/sessions. /sessions is NOT in border0-go and not in the Tailscale docs. 404: /session, /logs, /session_logs, /recordings, /events, /audit.

Session record fields, verified from a real session: session_id, socket_id, socket_name, start_time, end_time, last_seen, session_type (ssh), result (success), killed (bool), server_name, server_port, sshuser, auth_info (which grant allowed it), recordings[] with recording_id + recording_type (asciinema), events[] with type (ssh_session, ssh_exec), status and metadata. That covers the original scope: session counts and durations by service type, and recording presence.

PII fence, non-negotiable: the same payload carries user_email, name, picture (gravatar URL), client_ip, client_port, metadata.device.name and events[].metadata.command (the literal command line). None of those may become metric labels. Bounded label candidates are socket_name, session_type, result, killed, recording_type.

Inventory is already partly free: the connector advertises each PAM service as a Tailscale Service with its own VIP, so svc:ssh-camden already appears in the existing services collector via tailscale.service.ports. A PAM collector should not duplicate that; it should add the Border0-only dimensions (connector health via is_connected/last_seen_at, socket count by socket_type, session counts/durations).

Scope now definable. Blocking condition removed.

2026-09-04 API exploration complete. Full live-captured reference is doc-0004 (Tailscale PAM (Border0) API - live-captured reference). READ THAT FIRST; it carries the endpoint map, the three response envelopes, the /sessions polling semantics, every object shape, the PII/secret fence and the open questions. Real fixtures are in .capture/pam_*.json (gitignored, unredacted).

Credential model settled: a Border0 service account with role 'read only' reads every endpoint a collector needs and is denied writes (403 on POST /socket). Account tailscale2otel-ro, token persisted as BORDER0_RO_TOKEN in ~/repos/chat-personal/tailscale/.secrets/creds.local.env. Token JWT has no exp claim.

Three findings that change the design and are easy to get wrong:

1. /sessions ignores EVERY filter and time-window parameter (session_type, socket_id, result, killed, user_email, start_time, from, since) with no error. Only page and page_size work. But records are ordered newest-first by start_time, so the poller pages from 1 and stops at the first already-seen session_id or at start_time <= cursor. That is the only thing that bounds a tick.

2. A socket-scoped session listing with zero sessions returns literally {} - no pagination key, no session_logs key. .pagination.total_records is null, not 0. Fixture: .capture/pam_socket_sessions_empty.json.

3. GET /socket/{id}/upstream_configurations returns the injected upstream password in CLEARTEXT to a read-only token. Any opt-in snapshot event for a PAM service must strip the auth sub-object before serialisation. internal/redact only handles URLs and cannot help.

Config-shape emission should follow internal/collector/settings verbatim: one 0/1 gauge per boolean feature keyed by a stable name attribute, plus an opt-in on-change JSON snapshot event with a heartbeat and a body-byte cap, plus an apistate.Disposition so a 403 is read as scope_denied rather than feature-off. Rich config-shape sources found: organization (mfa_required, private_network_enabled, dns_management_enabled, needs_reauth, ai_assistants_disabled, ai_session_analysis_disabled, setup_wizard.completed, plan.slug), organization.subscription.subscription_limit (a ready-made quota family: socket_count, socket_tcp_count, user_count, admin_user_count, custom_domain_count, custom_idp_count, notification_count), per-socket booleans (recording_enabled, end_to_end_encryption_enabled, cloud_authentication_enabled, connector_authentication_enabled, private_socket, protected_socket, connector_managed, private_network_enabled), connector (is_connected, last_seen_at age, active_tokens, active_plugins, built_in_ssh_service_enabled, version), and policy (org_wide, read_only, expires, version).

Do NOT duplicate: PAM config CHANGES are already counted by the auditlogs collector via origin=BORDER0_API, and PAM services already appear in the services collector as Tailscale Services with their own VIPs. The new collector adds Border0-only dimensions and session telemetry only.

No OpenAPI spec exists for this API, so the api-drift lane cannot cover it and an unhandled-field adjudication test is mandatory rather than nice to have.

Still unanswered, needs a lab change to capture: the session record for a denied or failed session (every capture is result=success), the shape of a live session (presumed absent end_time, which is how an active gauge would be derived), and any non-ssh session_type event vocabulary.
<!-- SECTION:NOTES:END -->
