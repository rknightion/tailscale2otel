---
id: doc-0004
title: Tailscale PAM (Border0) API - live-captured reference
type: specification
created_date: '2026-09-04 10:55'
updated_date: '2026-09-04 13:03'
---

Everything here was verified live on 2026-09-04 against a real PAM deployment on the lab tailnet:
one connector, one SSH service, six real recorded sessions. Nothing in it is from documentation,
because there is barely any.

**There is no OpenAPI spec for this API.** `api.border0.com/api/v1/{openapi,swagger}.json`,
`/openapi.json` and `/docs` all 404. Consequence: the daily api-drift lane cannot cover PAM, and
`spec/tailscale-api.json` never will either. The ground truth is the fixture set in
`.capture/pam_*.json` (gitignored, local) plus this document. Any collector built on this needs an
unhandled-field adjudication test, because nothing upstream will announce a shape change.

## 1. Auth

Base URL `https://api.border0.com/api/v1`. Static bearer token, `Authorization: Bearer <jwt>`.
Nothing else works: PAM has **no surface on `api.tailscale.com`** (every `/tailnet/-/pam*`,
`/connectors`, `/sessions` path 404s).

Service-account roles are `admin`, `member`, `read only`, `client`. **The `read only` role is
sufficient for every endpoint a collector needs and is denied writes**, verified both directions:

```
GET  /sockets   with read-only token -> 200
POST /socket    with read-only token -> 403
  {"error_message":"forbidden: entity 'token:<id>' is not allowed to perform action 'create'..."}
```

The token is a JWT carrying `org_id`, `service_account`, `service_account_id`, `type: token`,
`iat`. **It has no `exp` claim and does not expire.** There is no role claim in it; the role is
server-side, so a collector cannot self-check its own permissions from the token.

Lab credentials live in an ignored local env file outside this repository, as `BORDER0_API`,
`BORDER0_TOKEN`, `BORDER0_ORG_ID` and `BORDER0_SERVICE_ACCOUNT_ID`. A dedicated read-only service
account exists for this work; its id is in that file and deliberately not written here.

## 2. Endpoint map

Verified 200 with a `read only` token:

| Path | Returns |
|---|---|
| `GET /organization` | org config, plan, subscription limits, feature flags, setup wizard |
| `GET /serverinfo` | `{"data_consistency":{"rx_after_tx_delay_ms":N}}` only |
| `GET /connectors` | all connectors, each with rich metadata |
| `GET /connector/{id}` | one connector |
| `GET /connector/{id}/tokens` | token **metadata** only, no token values |
| `GET /connector/{id}/plugins` | plugin list |
| `GET /sockets` | all PAM services |
| `GET /socket/{id}` | one service |
| `GET /socket/{id}/connectors` | connector linkage |
| `GET /socket/{id}/upstream_configurations` | **upstream config including cleartext secrets** |
| `GET /sessions` | session logs, org-wide |
| `GET /socket/{id}/sessions` | session logs for one service |
| `GET /policies` | policies |
| `GET /policy/{id}` | one policy |
| `GET /organizations/iam/users` | users |
| `GET /organizations/iam/groups` | groups |
| `GET /organizations/iam/service_accounts` | service accounts, paginated |
| `GET /organizations/iam/service_accounts/{name}/tokens` | token metadata |

Verified 404, so do not go looking for them: `/session`, `/sessions/{id}`, `/logs`, `/session_logs`,
`/recordings`, `/events`, `/audit`, `/activity`, `/settings`, `/organizations/settings`,
`/organization/settings`, `/notifications`, `/custom_domains`, `/recording_storage`, `/plugins`,
`/users`, `/groups`, `/organizations`, `/organizations/iam/policies`, `/device_posture`,
`/monitoring`, `/health`, `/status`, `/metrics`, `/subscriptions`, `/billing`,
`/socket/{id}/policy`, `/socket/{id}/tags`, `/connectors/health`.

`GET /organizations/recording_storage` returns **500 with an empty body**, not 404. It probably
exists and is unconfigured. Treat a 500 there as "not configured", not as an outage.
`GET /account` returns 403 `"No account credentials found in token"` for a service-account token.

`/sessions` is absent from the `border0-go` SDK and from the Tailscale docs entirely. It was found
by probing. It is the only source of session telemetry.

## 3. Three different response envelopes on one API

This is a parsing trap, not a style quibble:

- `{"pagination":{...},"list":[...]}` - `/sockets`, `/organizations/iam/*`
- `{"pagination":{...},"session_logs":[...]}` - `/sessions`, `/socket/{id}/sessions`
- `{"list":[...]}` with **no pagination at all** - `/connectors`
- a **bare JSON array** - `/policies`
- `{}` - a socket-scoped session listing with zero sessions. No `pagination`, no `session_logs`.
  `.pagination.total_records` is `null`, not `0`. A collector that reads the count without a nil
  guard gets a wrong answer or a panic.

`pagination` is `{current_page, next_page, total_records, total_pages, records_per_page,
actual_page_size}`. `next_page` is `0` on the last page.

## 4. `/sessions` semantics - read this before designing the poller

**Every filter parameter is silently ignored.** Measured against a fixture set of 6 sessions, all
`session_type: ssh` on one socket:

```
sessions?session_type=database                          -> total_records=6
sessions?session_type=bogus                             -> total_records=6
sessions?socket_id=00000000-0000-0000-0000-000000000000 -> total_records=6
sessions?result=failed                                  -> total_records=6
sessions?killed=true                                    -> total_records=6
sessions?user_email=nobody@example.com                  -> total_records=6
sessions?start_time=2030-01-01T00:00:00Z                -> total_records=6
sessions?from=... / ?since=...                          -> total_records=6
```

No 400, no warning, no empty result. They are accepted and discarded. **There is no server-side time
window**, so an incremental poller cannot ask for "sessions since T".

Two things make bounded polling possible anyway, and both are load-bearing:

- **`page` and `page_size` ARE honoured.** `page_size=2` really returns 2 and sets
  `records_per_page: 2`, `next_page: 3`.
- **Records are ordered newest-first by `start_time`, descending.** Verified across a 6-record set
  spanning two clusters 20 minutes apart.

So the poller pages from page 1 and **stops at the first `session_id` it has already recorded**, or
at the first `start_time` at or before its cursor. That bounds each tick to the new sessions plus
one page. Reuse the durable-evidence / poll-cursor split from TSO-0023; do not conflate them.

`GET /socket/{id}/sessions` is the one real filter that works, but scoping per socket is an N+1 fan
out. Prefer the org-wide `/sessions` and bound sub-requests the way TSO-0052 did.

## 5. Object shapes

### Session (`session_logs[]`)

```
session_id, socket_id, socket_name, server_name, server_port,
start_time, end_time, last_seen,        // end_time absent while a session is live
session_type ("ssh"), result ("success"), killed (bool), audit_log (bool),
sshuser, user_email, name, picture, sub, nickname,
client_ip, client_port, country_code, country_flag,
auth_info    // JSON-in-a-string: {"allowed":["tailscale acl: granted by tailscale.com/cap/pam", ...]}
recordings[] { recording_id, start_time, recording_type ("asciinema") }
recording_locked_by_plan (bool)
metadata { ip_metadata{}, device{ ip, name } }
events[] { created_at, type ("ssh_session"|"ssh_exec"), status, metadata }
             // metadata is JSON-in-a-string and CARRIES THE LITERAL COMMAND LINE:
             // {"pty": false, "command": "hostname; id", "username": "<user>", ...}
```

`auth_info` and `events[].metadata` are **strings containing JSON**, not objects. Double decode.

### Connector

```
name, connector_id, description, active_tokens, active_plugins, sockets,
created_at, updated_at, last_seen_at, is_connected (bool),
notifications_enabled, notify_after_seconds,
built_in_ssh_service_enabled, built_in_ssh_service { socket_id, name, description,
    dnsname, socket_type, alive, connector_managed, autocreation_rule_id },
metadata.connector_internal_metadata {
    version, built_date,
    ip_address,                                  // the connector's PUBLIC IP
    ip_metadata { isp, city_name, region_name, country_code, latitude, longitude },
    host_metadata { os, platform, platform_version, kernel_arch, kernel_version, uptime, hostname }
}
```

`host_metadata.hostname` is the **container id** when the connector runs in Docker, not the host.
`uptime` is the host's, in seconds.

### Socket (PAM service)

```
socket_id, name, description, dnsname, socket_type, display_name,
socket_tcp_ports, upstream_type, upstream_http_hostname,
recording_enabled, connector_authentication_enabled, end_to_end_encryption_enabled,
cloud_authentication_enabled, cloud_authentication_email_allowed_addressses (sic, two s's),
cloud_authentication_email_allowed_domains, custom_domains[],
private_socket, private_network_enabled, private_network_ipv4, private_network_ipv6,
protected_socket, protected_username, protected_password,
upstream_username, upstream_password,
tags{}, alive, connector_managed, autocreation_rule_id,
connectors[] { name, connector_id }
```

`socket_type` enum from the Terraform provider: `ssh`, `http`, `database`, `tls`, `vnc`, `rdp`,
`subnet_router`, `exit_node`, `snowflake`, `elasticsearch`, `kubernetes`, `aws_s3`. Bounded, safe as
a metric label.

Note `upstream_username` on a live socket is a long opaque hex string, not the configured username.
The configured username lives in the upstream configuration.

### Upstream configuration

`GET /socket/{id}/upstream_configurations` returns `{"list":[{config, created_at, updated_at}]}`
where `config` is `{service_type, <type>_service_configuration:{...}}`, nesting for SSH as
`ssh_service_configuration.standard_ssh_service_configuration` with `hostname`, `port`,
`ssh_authentication_type`, and one of `username_and_password_auth_configuration`,
`private_key_auth_configuration`, `border0_certificate_auth_configuration`.

**This endpoint returns the injected upstream password in cleartext, to a `read only` token.**
Verified. See section 6.

### Organization

```
id, name, subdomain, account_id, owner_email, role,
mfa_required, private_network_enabled, dns_management_enabled, needs_reauth,
metadata { ai_assistants_disabled, ai_session_analysis_disabled, is_ts },
setup_wizard { completed, steps{ <step>: {completed, completed_at, completed_by_email,
                                          completed_by_id, skipped} } },
subscription {
  plan { name, slug },                            // lab: "TS Free plan" / "ts-free"
  subscription_limit { subscription_id, socket_count, socket_tcp_count, organization_count,
                       admin_user_count, user_count, custom_domain_count, custom_idp_count,
                       notification_count }
}
```

`subscription_limit` is a ready-made quota family. The lab tier caps sockets at 10, users at 6,
custom domains at 0.

### Policy

```
id, name, description, org_id, org_wide (bool), read_only (bool), version ("v2"),
expires (bool), deleted (bool), created_at, socket_ids[],
policy_data { condition { when{after,before,time_of_day_after,time_of_day_before},
                          who{email[],group[],service_account[]} },
              permissions { ssh{shell,exec,sftp}, database, http, kubernetes, rdp, vnc, aws_s3 } }
```

The Tailscale ACL's PAM grant is mirrored here automatically as a `read_only: true`, `org_wide:
true` policy named `tailscale-acl-<date>-<hex>`. Its `who.group` holds Border0 group ids, not
Tailscale group names.

### IAM

Users: `id, email, display_name, user_type, role, image_url, directory_service{id, display_name,
service_type}`. Groups: `id, display_name, group_type, directory_service{...}`. Service accounts:
`name, description, service_account_id, role, active, created_at, updated_at, last_seen_at`, plus
`directory_service` on the tag-mirrored ones.

**Enabling PAM mirrors every Tailscale tag into Border0 as a `client`-role service account** and
every autogroup as a group. The lab has 25 service accounts of which 1 is real. A collector counting
service accounts must split on `role` or it reports nonsense.

## 5b. Session semantics that only show up when you provoke them

All verified on 2026-09-04 by deliberately creating each condition.

**`result` is the AUTHORIZATION result, not the connection outcome.** The upstream container was
stopped and an SSH connection attempted; the client got `Connection reset by peer`, and the session
was logged `result: success`. Nothing in the record marks the failure. Do not build a
failed-connection alert on `result`, and do not describe the label as connection health.

**A denial at the tailnet grant layer produces no session record at all.** Connecting from a tagged
node that the `cap/pam` grant does not cover was dropped by the Tailscale filter before it reached
the connector: no `/sessions` row, and no `authz:` line in the connector log. Denials of that class
are visible only in Tailscale network flow logs. Whether a *Border0 policy* denial (as opposed to a
grant denial) creates a row is still untested; it needs a second identity to provoke.

So: `/sessions` counts sessions that reached the connector and were authorized. It is not an
access-attempt log and must not be presented as one.

**Field presence varies by `session_type`.** A `database` session has **no `events` key and no
`sshuser`** (it is `null`), despite `recording_enabled: true` on the socket. The per-query logs with
execution time and rows affected that the product documentation describes are **not in this API**;
they are inside the recording or console-only. Model every field as optional.

**`recordings` populates asynchronously, minutes after the session ends.** A session read
immediately after it closes has `recordings: []`; the same session read later has one entry. A
collector that counts recordings on first sight will undercount, so either count them on a later
pass or do not emit a recording-presence metric at all.

**A live session has `end_time: null`** and no recordings yet. That is how an active-sessions gauge
is derived: `end_time == null`. `last_seen` equals `start_time` while it runs.

**`auth_info` text varies with the grant.** A database session under the broad auto-added grant read
`"tailscale acl: granted by tailscale.com/cap/pam (full access)"`, where the SSH sessions read the
same string without the `(full access)` suffix. It is free text inside a JSON-in-a-string. Never
parse it for control flow, and never label on it.

### Database service payload, verified working

```jsonc
{
  "name": "<service>", "socket_type": "database", "recording_enabled": true,
  "connector_ids": ["<connector id>"],
  "upstream_configuration": {
    "service_type": "database",
    "database_service_configuration": {
      "database_service_type": "standard",
      "standard_database_service_configuration": {
        "hostname": "<upstream host>", "port": 5432,
        "protocol": "postgres",
        "authentication_type": "username_and_password",
        "database_name": "<db>",
        "username_and_password_auth_configuration": {"username": "…", "password": "…"}
      }
    }
  }
}
```

The client then connects to the service's MagicDNS name on 5432 with **its own identity as the
username and an empty password**; PAM injects the real upstream credentials. Verified with pg8000
against a throwaway Postgres 17.

### `PUT /connector` lies in its response body

Disabling the built-in SSH service with `PUT /connector` returned 200 with
`built_in_ssh_service_enabled: true` and the socket still populated, while the change had in fact
applied: the socket was gone from `GET /sockets` in the same second and the connector logged
`socket: removing socket` / `tailscale: unadvertised service`. The response echoes pre-change
state. **Verify every mutation with a fresh GET**, exactly as this repo already does for Intune
Settings Catalog writes.

## 6. PII and secret fence - hard rules

The repo's existing fence (flow logs, node metrics) applies, and PAM is worse. None of the
following may ever become a metric label:

`user_email`, `name`, `picture` / `image_url`, `sub`, `nickname`, `client_ip`, `client_port`,
`sshuser`, `country_code`, `metadata.device.name`, `metadata.device.ip`,
`events[].metadata.command` (the literal command line), and on connectors
`metadata.connector_internal_metadata.ip_address` plus the whole `ip_metadata` block, which
geolocates the operator's own premises.

Never emitted anywhere, log bodies included: `upstream_configurations[].config.*.password` and
`.private_key`, socket `upstream_password`, `protected_password`, `protected_username`.

**A `read only` service account can read every injected upstream credential in the organization**,
in cleartext, from `GET /socket/{id}/upstream_configurations`. Verified live. Two consequences: an
opt-in snapshot event for a PAM service must strip the auth sub-object before serialisation, not
after, and `internal/redact` cannot help because it only handles URLs. Connector tokens are safe:
`GET /connector/{id}/tokens` returns `{id, name, created_by, created_at}` and no token value.

Bounded, safe label candidates: `socket_type`, `session_type`, `result`, `killed`,
`recording_type`, `events[].type`, `events[].status`, policy `version`, service-account `role`, plan
`slug`, connector `version`, and the boolean feature names.

## 7. What the Tailscale side already gives you - do not duplicate it

- **Config changes are already collected.** Every PAM mutation lands in the ordinary
  `/logging/configuration` audit stream with `origin=BORDER0_API`, `actor.type=PAM_SERVICE_ACCOUNT`
  or `PAM_CONNECTOR`, and `target.type` in `PAM_SERVICE` / `PAM_CONNECTOR` /
  `PAM_SERVICE_ACCOUNT`. The auditlogs collector counts them correctly today, verified in Grafana
  Cloud. A PAM collector must not re-emit change events.
  Caveat: console changes carry `origin=ADMIN_CONSOLE`, `actor.type=USER` instead, so the
  `BORDER0_API` origin appears only for API-driven changes.
- The 22 dotted `PAM_*.*` strings in `spec/tailscale-api.json` are values of the **`event` query
  parameter**, not target types. `GET /logging/configuration?event=PAM_SERVICE.CREATE` filters
  server-side.
- **PAM services are already in the services collector.** The connector advertises each one as a
  Tailscale Service with its own VIP, so each PAM service appears in `tailscale.service.ports`
  under its own `svc:` name with no new code. A PAM collector should add the Border0-only
  dimensions, not restate service inventory.
- TSO-0134 covers the gap on the Tailscale side: PAM events never reach
  `tailscale.config.audit.changes`, and `BORDER0_PROVISIONING` is absent from the vendored spec.

## 8. Fixtures

`.capture/pam_*.json`, captured 2026-09-04 with the read-only token, unredacted and gitignored:

```
pam_organization  pam_serverinfo  pam_connectors  pam_connector_one  pam_connector_tokens
pam_connector_plugins  pam_sockets  pam_socket_one  pam_socket_connectors
pam_socket_upstream_config  pam_socket_builtin_ssh  pam_sessions  pam_socket_sessions
pam_socket_sessions_empty  pam_policies  pam_policy_one  pam_iam_users  pam_iam_groups
pam_iam_service_accounts
```

`pam_socket_sessions_empty.json` is literally `{}` and is the regression fixture for the empty-shape
trap. `pam_socket_upstream_config.json` contains a real cleartext password and must be redacted
before any of it becomes a committed testdata file.

## 9. Open questions

Answered since first writing: the live-session shape, the failed-session shape, the denied-session
shape and a non-SSH `session_type` are all covered in section 5b.

Still open:

- **Does a Border0-policy denial create a session row?** Needs a second identity to provoke. The
  grant-layer denial definitively does not.
- **Where do the per-query database logs actually live?** They are documented as a product feature
  and are absent from `/sessions`. Probably inside the recording artifact. If a future collector
  wants query counts, this is the blocker.
- **Does `/sessions` prune?** If it never does, `total_records` grows without bound and the
  newest-first ordering is the only thing keeping polling cheap.
- **Rate limits.** None hit, none documented, no `X-RateLimit-*` headers observed.
