---
title: Event explorer
description: Inspect recent audit and webhook events locally, with bounded history and filtered JSON access.
---

# Event explorer

Open `/events` on the admin server to inspect recent configuration audit and webhook events.
The explorer works without a telemetry backend and uses the same authentication as the status
page. It combines events from all configured tailnets in one process-local store.

`events.enabled`, `admin.enabled` and `admin.landing_page` must all be true. They are enabled by
default. If any is disabled, the event routes return 404. Audit or webhook ingestion must also be
configured before events can appear; an empty explorer alone does not prove ingestion works.

## Retention and privacy

`events.max_events` defaults to 5,000, with a permitted range of 100 to 100,000. The store evicts
the oldest entries at capacity and loses all entries on restart. Its capacity is a count, not a
time window: a busy tailnet has less retained history than a quiet one. Use exported logs for
history beyond this ring.

The local explorer can contain actor names, addresses and raw audit-change details. `pii_filter`
controls exported telemetry, not this administrator view. Restrict the admin token to people who
may see those identities. See [Security](security.md).

## Filtering and JSON access

The page reads `GET /api/events.json`. You can use the same API with admin authentication:

```sh
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  'http://127.0.0.1:9091/api/events.json?source=audit&limit=200'
```

| Parameter | Behavior |
|---|---|
| `source`, `severity`, `type` | Exact, case-insensitive matches. Sources include `audit` and `webhook`. |
| `actor`, `action`, `target` | Case-insensitive substring matches; actor and target search both name and ID. |
| `errors` | A non-empty value other than `0` or `false` selects events carrying an error. |
| `start`, `end` | RFC3339 timestamps. Omitted or invalid values use all retained history for the start and the current time for the end. |
| `limit` | Defaults to 200, capped at 1,000 per response. |
| `cursor` | Pass the previous response's `next_cursor` to read older matching entries. An invalid cursor starts from the newest entries. |

Filters combine with AND. A filter longer than 128 bytes returns HTTP 400. Read `matched`,
`returned`, `retained`, `truncated` and `next_cursor` to distinguish a short result from a
capacity-limited history. A cursor cannot recover an event that has already been evicted.
The JSON response has a [versioned schema](api/compatibility.md).

For connection traffic and CSV/JSON downloads, use the separate [flow view](flow-view.md).
