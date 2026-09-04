---
id: TSO-0036
title: PAM telemetry collector (Border0 API)
status: To Do
assignee: []
created_date: '2026-08-30 09:09'
updated_date: '2026-09-04 17:02'
labels: []
milestone: m-8
dependencies: []
priority: medium
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Tailscale PAM went beta 2026-08-26 (Border0 acquisition). The original premise here was wrong and is corrected: PAM does have a usable API surface, it is just not on api.tailscale.com and never will be. There is no OpenAPI spec for it at all, so the daily api-drift lane cannot cover it and spec/tailscale-api.json will never carry it.

The specification is doc-0004, a live-captured reference verified against a real PAM deployment on the lab: the endpoint map, the four response envelopes, every object shape, the /sessions polling semantics, the PII and cleartext-secret fences, and the three result semantics that produce wrong metrics if taken at face value. Read it in full before writing anything. The fixtures behind it are in .capture/pam_*.json, gitignored and unredacted.

Build a collector for PAM inventory, config shape and session telemetry against that reference. Because nothing upstream will announce a shape change, an unhandled-field adjudication test is mandatory rather than nice to have.

Scope fence: PAM config CHANGES are already counted by the auditlogs collector via origin=BORDER0_API, and PAM services already appear in the services collector as Tailscale Services with their own VIPs. This collector adds the Border0-only dimensions and session telemetry, and restates neither. TSO-0134 owns the audit-side gap.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A tracked, redacted fixture set lives in the repository and preserves every response envelope and object shape from the live captures, with PII and cleartext secrets replaced by deterministic safe values; the unredacted .capture/pam_*.json stay local and no test depends on them, so the suite runs identically in CI and on a fresh clone
- [ ] #2 A new internal/b0api client talks to api.border0.com/api/v1 with a static bearer token, handles all four response envelopes including the bare {} empty case, and is covered by table tests built from the tracked fixtures
- [ ] #3 A read-only Border0 service account is sufficient: the collector never calls a mutating endpoint, and a 403 is recorded as scope_denied via apistate.Disposition rather than read as a disabled feature
- [ ] #4 Inventory and config-shape metrics land for connectors, services, policies, identities, org settings and subscription limits, following the internal/collector/settings pattern of one 0/1 gauge per boolean keyed by a stable name attribute
- [ ] #5 Session metrics land as deltas with no double counting across restarts, proven by a test that replays the same page twice
- [ ] #6 The session poller stops paging at the first already-seen session_id, proven by a test asserting the request count against a multi-page fixture
- [ ] #7 No PII reaches a metric label: a test asserts the emitted attribute set against an allowlist, and upstream_configurations secrets appear in no metric, log or snapshot
- [ ] #8 An unhandled-field adjudication test fails when a tracked fixture grows a field the decoder ignores, since there is no OpenAPI spec and the api-drift lane cannot cover this API
- [ ] #9 Config keys, the collector catalog entry and the generated collector docs are wired the same way the settings collector is
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

2026-09-04 UNPARKED. The premise was wrong: PAM does have a published API surface, just not on api.tailscale.com. Verified live against a real PAM deployment on the lab tailnet: two connectors, an SSH service and a database service against throwaway containers, and a set of real recorded sessions.

Full live-captured reference is doc-0004 (Tailscale PAM (Border0) API - live-captured reference). READ THAT FIRST. It carries the endpoint map, the four response envelopes, the /sessions polling semantics, every object shape, the PII and cleartext-secret fences, the provoked-condition findings and the remaining open questions. Real fixtures are in .capture/pam_*.json, which is gitignored and unredacted.

Base URL https://api.border0.com/api/v1, static bearer token. Lab credentials live in an ignored local env file outside this repository. Service-account tokens have no exp claim and do not expire.

Credential model settled: a Border0 service account with role 'read only' reads every endpoint a collector needs and is denied writes (403 on POST /socket, verified both directions).

Findings that change the design and are easy to get wrong. All are expanded in doc-0004; this is the index:

1. /sessions ignores EVERY filter and time-window parameter (session_type, socket_id, result, killed, user_email, start_time, from, since) with no error. Only page and page_size work. Records are ordered newest-first by start_time, so the poller pages from 1 and stops at the first already-seen session_id or at start_time <= cursor. That ordering is the only thing that bounds a tick.

2. A socket-scoped session listing with zero sessions returns literally {} - no pagination key, no session_logs key, so total_records is null rather than 0.

3. GET /socket/{id}/upstream_configurations returns the injected upstream password in CLEARTEXT to a read-only token. Any opt-in snapshot event for a PAM service must strip the auth sub-object before serialisation. internal/redact only handles URLs and cannot help.

4. result is the AUTHORIZATION result, not the connection outcome. A session against a stopped upstream still records success. Never present that label as connection health.

5. A grant-layer denial produces no session row at all, so /sessions is not an access-attempt log.

6. recordings populates asynchronously, minutes after a session ends, so counting it on first sight undercounts.

7. A database session carries no events and no sshuser. The per-query logs the product advertises are not in this API.

8. PUT /connector echoes pre-change state in its 200 response. Verify every mutation with a fresh GET.

Config-shape emission should follow internal/collector/settings verbatim: one 0/1 gauge per boolean feature keyed by a stable name attribute, plus an opt-in on-change JSON snapshot event with a heartbeat and a body-byte cap, plus an apistate.Disposition so a 403 is read as scope_denied rather than feature-off. Rich config-shape sources found: organization (mfa_required, private_network_enabled, dns_management_enabled, needs_reauth, ai_assistants_disabled, ai_session_analysis_disabled, setup_wizard.completed, plan.slug), organization.subscription.subscription_limit (a ready-made quota family: socket_count, socket_tcp_count, user_count, admin_user_count, custom_domain_count, custom_idp_count, notification_count), per-socket booleans (recording_enabled, end_to_end_encryption_enabled, cloud_authentication_enabled, connector_authentication_enabled, private_socket, protected_socket, connector_managed, private_network_enabled), connector (is_connected, last_seen_at age, active_tokens, active_plugins, built_in_ssh_service_enabled, version), and policy (org_wide, read_only, expires, version).

Do NOT duplicate: PAM config CHANGES are already counted by the auditlogs collector via origin=BORDER0_API, and PAM services already appear in the services collector as Tailscale Services with their own VIPs. The new collector adds Border0-only dimensions and session telemetry only. See TSO-0134 for the audit-side gap.

No OpenAPI spec exists for this API, so the api-drift lane cannot cover it and an unhandled-field adjudication test is mandatory rather than nice to have.

Scope now definable. Blocking condition removed; the implementation plan carries the frozen seams, the four phases and the proposed metric set.
<!-- SECTION:NOTES:END -->
