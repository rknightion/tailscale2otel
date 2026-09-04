---
id: TSO-0036
title: PAM telemetry collector (Border0 API)
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-09-04 13:43'
labels: []
milestone: m-8
dependencies: []
priority: medium
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale PAM went beta 2026-08-26 (Border0 acquisition); PAM service accounts call a PAM API but no endpoints are in the published OpenAPI spec yet. We have no PAM setup, so this is spec-driven only: placeholder tracking task - when PAM endpoints appear in the vendored spec (the daily api-drift lane will surface them), design a collector for session counts/durations by service type, JIT access-request rates and recording-storage settings. Do not build ahead of the spec.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A new internal/b0api client talks to api.border0.com/api/v1 with a static bearer token, handles all four response envelopes including the bare {} empty case, and is covered by table tests built from the .capture/pam_*.json fixtures
- [ ] #2 A read-only Border0 service account is sufficient: the collector never calls a mutating endpoint, and a 403 is recorded as scope_denied via apistate.Disposition rather than read as a disabled feature
- [ ] #3 Inventory and config-shape metrics land for connectors, services, policies, identities, org settings and subscription limits, following the internal/collector/settings pattern of one 0/1 gauge per boolean keyed by a stable name attribute
- [ ] #4 Session metrics land as deltas with no double counting across restarts, proven by a test that replays the same page twice
- [ ] #5 The session poller stops paging at the first already-seen session_id, proven by a test asserting the request count against a multi-page fixture
- [ ] #6 No PII reaches a metric label: a test asserts the emitted attribute set against an allowlist, and upstream_configurations secrets appear in no metric, log or snapshot
- [ ] #7 An unhandled-field adjudication test fails when a captured fixture grows a field the decoder ignores, since there is no OpenAPI spec and the api-drift lane cannot cover this API
- [ ] #8 Config keys, the collector catalog entry and the generated collector docs are wired the same way the settings collector is
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check passes (the full gate; it is what CI enforces)
- [ ] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [ ] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-09-04 plan. Read doc-0004 in full first; it is the API reference and every number here comes from it.

FROZEN SEAMS, agree before any lane starts:
- Package internal/b0api, constructor mirroring internal/tsapi NewClient(Options). Base URL configurable, default https://api.border0.com/api/v1. Static bearer, no refresh.
- Config block collectors.pam with enabled, interval, sessions_interval, snapshot_enabled, snapshot_heartbeat, snapshot_body_bytes. Credential keys pam.token and pam.api_url, env TS2OTEL_PAM__TOKEN.
- Metric and attribute namespace tailscale.pam.*.

PHASE 1, client and fixtures. Decode every .capture/pam_*.json shape. The four envelopes are {pagination,list}, {pagination,session_logs}, {list} with no pagination, and a bare array for /policies, plus literal {} for an empty socket-scoped session listing. auth_info and events[].metadata are JSON inside strings; decode twice or keep them opaque.

PHASE 2, inventory and config-shape collector, snapshot style, interval 600s:
- GET /connectors -> tailscale.pam.connectors, .connector.connected (0/1), .connector.last_seen_age (s), .connector.sockets, .connector.tokens, .connector.plugins, .connector.info (1) carrying version and built_date.
- GET /sockets -> tailscale.pam.services by socket_type, .service.alive (0/1), and .service.setting.enabled (0/1) keyed by setting name over recording_enabled, end_to_end_encryption_enabled, cloud_authentication_enabled, connector_authentication_enabled, private_socket, protected_socket, connector_managed, private_network_enabled.
- GET /policies -> tailscale.pam.policies plus .policy.setting.enabled over org_wide, read_only, expires.
- GET /organizations/iam/{users,groups,service_accounts} -> tailscale.pam.identities by kind and role. Split service accounts on role or the count is meaningless: enabling PAM mirrors every Tailscale tag in as a client-role account, 25 accounts of which 1 is real on the lab.
- GET /organization -> tailscale.pam.org.setting.enabled over mfa_required, private_network_enabled, dns_management_enabled, needs_reauth, ai_assistants_disabled, ai_session_analysis_disabled, setup_wizard.completed; .org.plan.info (1) keyed by plan slug; .subscription.limit keyed by limit name over the seven subscription_limit fields. That family plus the inventory counts gives quota headroom with no extra call.

PHASE 3, session collector, delta style, interval 60s. /sessions ignores every filter and time-window parameter but honours page and page_size and returns newest-first, so page from 1 and stop at the first already-seen session_id or start_time at or before the cursor. Reuse the durable-evidence and poll-cursor split from TSO-0023. Metrics: tailscale.pam.sessions (counter by service, session_type, result), .session.duration (histogram by session_type, start_time to end_time), .sessions.killed, .sessions.active (gauge, end_time null), .session.events (counter by event type and status), plus an opt-in log event tailscale.pam.session carrying the record, which is where the PII lives and why it is opt-in.

Three semantics that produce wrong metrics if ignored, all in doc-0004 section 5b: result is the AUTHORIZATION result and an upstream-down session still reads success, so never name it connection health; recordings populate asynchronously minutes after a session ends, so never count them on first sight; a grant-layer denial produces no row at all, so this is not an access-attempt log.

PHASE 4, wiring. internal/app/collectors.go, internal/app/collectordocs.go, internal/catalog/catalog.go, config.schema.json. Apply the TSO-0066 per-tailnet cardinality limits to the service and connector name labels.

OUT OF SCOPE: anything that re-emits PAM config changes. The auditlogs collector already counts them via origin=BORDER0_API and PAM services already appear in the services collector. See TSO-0134 for the audit-side gap.
<!-- SECTION:PLAN:END -->

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
