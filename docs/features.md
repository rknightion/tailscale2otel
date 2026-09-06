---
title: Feature guide
description: Find setup and operating guidance for each tailscale2otel feature.
---

# Feature guide

Use this index to find the setup guide for a feature. The [configuration reference](configuration.md)
contains defaults and limits; the [metrics catalog](metrics.md) lists emitted metrics and log events.
Optional collection requires both its configuration switch and access to the source API.

## Collection

| Feature | What it does | Setup and reference |
|---|---|---|
| Device inventory | Tracks online state, last seen, expiry, versions, routes, NAT, DERP latency, tailnet lock and fleet hygiene. Populates the enrichment cache. | [Devices](configuration.md#collectorsdevices) |
| Device changes | Emits inventory changes, including additions, removals and changed properties, for lifecycle history. | [Device signals](metrics.md#devices-tailscaledevice-tailscaledevicescount) |
| Device posture | Fetches posture and attribute expiry, optionally checks named attribute values, and bounds the promoted attribute keys. | [Device options](configuration.md#collectorsdevices) |
| Flow logs | Produces traffic metrics and per-connection logs, with top-N rollup, port/service attribution and optional TSMP records. | [Flow configuration](configuration.md#collectorsflowlogs), [flow view](flow-view.md) |
| Configuration audit | Emits structured events and categorized change counters, including PAM-related changes and schema-drift diagnostics. | [Audit configuration](configuration.md#collectorsauditlogs), [audit signals](metrics.md) |
| Kubernetes audit | Reads tsrecorder API audit and session recordings from object storage. | [Kubernetes audit](kubernetes-audit.md) |
| Users and invites | Tracks users, roles, status, device counts and invite lifecycle. | [Snapshot collectors](configuration.md#snapshot-collectors), [signals](metrics.md) |
| Keys | Tracks auth keys, OAuth clients and API tokens, expiry and creation/revocation observations. | [Snapshot collectors](configuration.md#snapshot-collectors), [signals](metrics.md) |
| OAuth apps | Reads the alpha OAuth-application inventory where the API is available. | [Snapshot collectors](configuration.md#snapshot-collectors) |
| ACL and grants | Counts policy structure, adoption and risk; optionally validates policy and exports raw snapshots/diffs. Raw snapshot consent bypasses PII filtering for those bodies. | [Policy options](configuration.md#snapshot-collectors), [security](security.md#acl-policy-hygiene) |
| DNS, settings and posture integrations | Reports configuration and integration health; optional snapshots record changes and periodic refreshes. | [Snapshot collectors](configuration.md#snapshot-collectors) |
| Contacts | Reports verification state without exporting the contact email. | [Snapshot collectors](configuration.md#snapshot-collectors) |
| Webhook inventory | Reports configured endpoints and subscriptions; optional snapshots record the configuration. This is separate from the inbound receiver. | [Snapshot collectors](configuration.md#snapshot-collectors) |
| Log-stream configuration and health | Reports configured destinations, safe destination metadata, enabled state and delivery health. | [Snapshot collectors](configuration.md#snapshot-collectors), [signals](metrics.md) |
| Tailscale Services | Reports VIP service inventory, names, tags, ports and optional backing hosts. | [Services](configuration.md#collectorsservices) |
| PAM | Reads Border0 inventory, policies, organization settings and sessions, with optional snapshots and session logs. | [PAM](pam.md) |
| Node metrics | Scrapes native tailscaled metrics centrally, forwarding raw series and deriving bounded metrics including peer-relay connectivity. | [Node metrics](node-metrics.md) |
| Node discovery | Combines static targets with device discovery, tag filters, address-family selection and per-tag port overrides. | [Discovery](node-metrics.md#dynamic-discovery-first-run) |

## Sources, identity and processing

| Feature | What it does | Setup and reference |
|---|---|---|
| API polling | Reads bounded windows with lag and resumable high-water marks. | [Ingestion choices](streaming-webhooks.md) |
| HEC streaming | Accepts authenticated Tailscale flow/audit pushes, with compressed-body and concurrency limits. | [Streaming receiver](streaming-webhooks.md) |
| S3-compatible ingestion | Reads flow/audit exports with independent destinations, checkpointed object identities, failed-object gaps and bounded backfill. | [Object storage](streaming-webhooks.md) |
| Webhook receiver | Verifies HMAC signatures and timestamps, routes tailnet events and suppresses duplicates. | [Webhooks](streaming-webhooks.md) |
| Receiver WAL | Persists accepted request bodies before acknowledgement and replays them after restart, with at-least-once semantics. | [Ingress WAL](configuration.md#ingress_wal-durable-local-receiver-acceptance) |
| Device enrichment | Resolves addresses and node IDs using a per-tailnet cache; tracks staleness during API failures. | [Enrichment](configuration.md#enrichment-device-name-cache) |
| Reverse DNS | Adds cached external PTR names asynchronously, with bounded queues, warm-start snapshots and an admin purge. | [Reverse DNS](configuration.md#enrichmentreverse_dns) |
| GeoIP and ASN | Enriches external peers from local MaxMind databases, with optional database refresh/download. | [GeoIP](configuration.md#enrichmentgeoip) |
| Cardinality and dedup bounds | Controls flow dimensions, per-entity metrics, series caps, per-tailnet overrides and dedup capacity. | [Cardinality](configuration.md#cardinality-metriclabel-cardinality-controls) |
| PII controls | Removes disabled identifier categories from exported telemetry and supported persisted data. Defaults retain identifiers. | [Security](security.md), [PII settings](configuration.md#pii_filter-pii-identifier-redaction) |
| OAuth, API key and workload identity | Supports refreshing OAuth, static API keys and OIDC token exchange for Tailscale. | [Authentication](configuration.md#tailscaleauth) |
| Headscale | Runs the supported reduced collector set against a self-hosted control plane, including custom private prefixes. | [Headscale](configuration.md#headscale-headscale-control-plane-connection) |
| Multiple tailnets | Uses separate credentials, clients, processors and signal labels within one process. Receiver routes and object-store destinations remain tailnet-specific. | [Multi-tailnet configuration](configuration.md#tailnets-multi-tailnet-msp-mode) |
| Organization discovery | Inventories tailnet IDs through the alpha Organizations API; it does not create authenticated collector runtimes. | [Tailscale settings](configuration.md#tailscale-api-connection-authentication) |
| Scheduling and API budgets | Spreads initial ticks, isolates collectors, bounds subrequests, retries and rate-limit waits. | [Scheduler](configuration.md#scheduler-initial-tick-spread), [HTTP client](configuration.md#tailscalehttp) |

## Delivery and operation

| Feature | What it does | Setup and reference |
|---|---|---|
| OTLP | Exports over HTTP or gRPC with TLS/mTLS, Grafana Cloud authentication, retry, batching and per-signal overrides. | [Delivery modes](configuration.md#delivery-modes), [OTLP](configuration.md#otlp-the-otlp-exporter) |
| Prometheus | Serves a separate pull endpoint with optional auth/TLS; supports pull-only or dual delivery to separate destinations. | [Getting started](getting-started.md#prometheus-pull) |
| stdout | Prints telemetry for local collection checks. | [stdout](getting-started.md#stdout) |
| Gateway | Sends through Alloy or a Collector for persistent buffering and routing. | [Gateway](gateway.md) |
| Metric temporality and resources | Configures cumulative/delta export, resource detection and custom attributes. | [OTLP](configuration.md#otlp-the-otlp-exporter), [resources](configuration.md#resource-otel-resource-enrichment) |
| Self-observability | Reports collection, API, ingress, queue, dedup, storage, delivery and cardinality health. | [Architecture](architecture.md#self-observability-and-the-admin-status-page), [runbooks](runbooks.md) |
| Tracing | Traces exporter work with per-workload sampling, inbound-parent controls and exemplars. | [Tracing](configuration.md#tracing-otel-traces-pillar) |
| Profiling | Supports authenticated pprof and Pyroscope push, with upload-health diagnostics. | [Profiling](configuration.md#profiling-pprof-pyroscope) |
| Version checks | Reports exporter updates and device version skew through independent outbound checks. | [Version checks](configuration.md#version_checks-outbound-is-a-newer-release-available-checks) |
| Local status and APIs | Displays collectors, delivery, cardinality, configuration provenance and component health. | [Admin](configuration.md#admin-admin-http-server-probes-status-page), [API compatibility](api/compatibility.md) |
| Flow explorer | Provides traffic views, filters, aggregates and bounded JSON/CSV export; SQLite persistence is optional. | [Flow view](flow-view.md) |
| Event explorer | Shows recent audit/webhook events in a bounded in-memory ring. | [Event explorer](events.md) |
| CLI diagnostics | Validates config, prints redacted effective settings, checks collection/export, probes health and adopts legacy flow databases. | [Getting started](getting-started.md#check-a-configuration-before-a-rollout), [troubleshooting](troubleshooting.md) |
| Support bundles | Downloads bounded redacted diagnostics and recent process logs, with separate device-inventory consent. | [Support bundles](troubleshooting.md#generating-a-support-bundle) |
| Secret and certificate rotation | Reloads supported fixed file contents; whole-config changes require a restart. | [Reload classifications](configuration.md#reload-classifications), [rotation](configuration.md#otlpcredential_reload) |
| Checkpoints and state | Separates cursor progress from ACL evidence; supports file, memory and Kubernetes cursor storage. | [Checkpoints](configuration.md#checkpoint-poll-high-water-marks), [upgrading](upgrading.md) |
| Kubernetes HA | Elects one active process, routes Services by leader label and keeps per-pod local state. | [High availability](high-availability.md) |
| Dashboards | Ships two Grafana v2 dashboards with conditional sections, tailnet selection and instance filtering. | [Dashboards](dashboards.md) |
| Alerts and recording rules | Ships Grafana-managed and Prometheus-compatible rules with profiles and runbooks. | [Alerts](alerts.md), [profiles](alert-profiles.md) |
| Grafana annotations | Publishes selected lifecycle/configuration events with bounded queues and dedup. Setting the destination opts into Grafana writes. | [Annotations](configuration.md#grafana_annotations-publish-tailnet-events-as-grafana-annotations) |

The [signal coverage ledger](signal-coverage.md) maps catalog signals to dashboards and rules.
A configured feature still needs source access and successful collection; a catalog entry alone
does not prove that data is arriving.
