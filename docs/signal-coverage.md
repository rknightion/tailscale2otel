---
title: Signal Coverage
description: Which emitted metrics and log events are charted, alerted on, recorded, or used by dashboard variables
---

# Signal coverage

Every metric and log event tailscale2otel emits is accounted for here. The table
says what each signal is *for* — whether a shipped dashboard charts it or uses it for a
variable, an alert rule watches it, or a recording rule folds it.

This page is **generated from
[`internal/catalog/signal_dispositions.json`](https://github.com/rknightion/tailscale2otel/blob/main/internal/catalog/signal_dispositions.json)**,
a manifest that is itself gated in CI against three things: the in-code telemetry
catalog (so a new signal cannot land undecided), the generated dashboard, and the
two alert-rule files (so a `visualized` or `alertable` claim has to be true, in
both directions). Nothing here comes from a text search over prose.

Regenerate with:

```sh
go test ./internal/catalog -run TestSignalDispositionsInSync -update
go test ./internal/catalog -run TestSignalCoverageDocInSync -update
```

## Dispositions

| disposition | meaning |
| --- | --- |
| `visualized` | queried by at least one **panel** on either generated dashboard |
| `alertable` | queried by at least one alert rule expression |
| `recorded` | consumed by at least one recording rule expression |
| `drives_a_variable` | queried by a dashboard **template variable** — a presence sentinel, or a filter dropdown |

A signal can carry several at once (most alerted metrics are also charted). All
four are **derived** — read off the shipped artifacts by the regenerator — so
there is nothing here for anyone to assign by hand, and no value that can be
written to settle a signal that is not actually on a surface.

`drives_a_variable` is deliberately **not** `visualized` (#527). A presence
sentinel's `label_values()` call references a metric every bit as really as a
panel query does, and puts nothing on screen — so while both fed one value, a
signal could satisfy the every-signal-reaches-a-panel bar while being invisible.
`tailscale.subnet_routes.advertised` sat in exactly that state, covered only by
the sentinel gating a row, and it stayed invisible until #526 deleted that row.
`tailscale.key.expiring` was worse: it counted as covered because its name
appears as an *option value* in a dropdown, which is not a query at all. Both
have panels now.

**Every emitted signal must appear on a panel**, and #526 removed the last three
ways round that. `raw_only` and `omitted` were deleted first: "deliberately
query-only" is indistinguishable, from the outside, from "nobody got round to it",
and 35 signals had accrued under the two of them. `pending_panel` replaced them as
an explicitly transitional, shrink-only ledger — a signal could only ever leave it
by gaining a panel — and was itself deleted the moment it emptied, which is the
only reason it was safe to introduce.

The only exemptions now are three **structural** classes, each an individually
justified entry in `catalog.StructuralExemptions()` and none of them a judgement
about whether a signal is worth charting:

| class | why it cannot be required |
| --- | --- |
| `histogram_base` | the base name of a histogram is never queried directly — panels query `_bucket`/`_sum`/`_count` |
| `templated_event` | the event name is built at emit time (`tailscale.webhook.<type>`), so no literal selector can equal it |
| `recorded_only` | consumed only by a recording rule whose *output* metric is itself panelled |

**Operational and self-observability coverage are reported separately.**
They answer different questions ("can I see my tailnet?" versus "can I see the
exporter?"), and a well-covered fleet story would otherwise hide a blind exporter.

The units, descriptions and attribute keys for every signal below are in
[`metrics.md`](metrics.md); this page only covers what each one is used for.

<!-- BEGIN GENERATED: signal-coverage -->

## Summary

A signal can carry more than one disposition, so the columns do not sum to the total.

| surface | signals | visualized | alertable | recorded | drives a variable |
| --- | ---: | ---: | ---: | ---: | ---: |
| operational | 234 | 233 | 57 | 11 | 44 |
| self_obs | 100 | 100 | 33 | 9 | 10 |

## Operational signals

| signal | kind | queried as | disposition | note |
| --- | --- | --- | --- | --- |
| `tailscale.acl.policy_diff` | log_event | `event_name="tailscale.acl.policy_diff"` | visualized |  |
| `tailscale.acl.policy_snapshot` | log_event | `event_name="tailscale.acl.policy_snapshot"` | visualized |  |
| `tailscale.acl.risky_rule` | log_event | `event_name="tailscale.acl.risky_rule"` | visualized | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Risk & ACL. |
| `tailscale.acl.validation_issue` | log_event | `event_name="tailscale.acl.validation_issue"` | visualized |  |
| `tailscale.config.audit` | log_event | `event_name="tailscale.config.audit"` | visualized, alertable, drives_a_variable |  |
| `tailscale.device.attribute.expiring` | log_event | `event_name="tailscale.device.attribute.expiring"` | visualized | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Posture & Compliance. |
| `tailscale.device.change` | log_event | `event_name="tailscale.device.change"` | visualized |  |
| `tailscale.device.key_expiring` | log_event | `event_name="tailscale.device.key_expiring"` | visualized | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Identity & Keys. |
| `tailscale.device.posture` | log_event | `event_name="tailscale.device.posture"` | visualized, drives_a_variable |  |
| `tailscale.device.tailnet_lock_error` | log_event | `event_name="tailscale.device.tailnet_lock_error"` | visualized |  |
| `tailscale.device_invite` | log_event | `event_name="tailscale.device_invite"` | visualized | #526 wave 2/3: panel scheduled on tailnet/Devices > Inventory & Hygiene. |
| `tailscale.dns.snapshot` | log_event | `event_name="tailscale.dns.snapshot"` | visualized |  |
| `tailscale.k8s.api_request` | log_event | `event_name="tailscale.k8s.api_request"` | visualized | The per-request record carrying the high-cardinality detail kept off the metrics (object name, query-free path, selectors, pod/container, and the raw exec command line under pii_filter.command_text). Read in Loki by event_name; this is the SIEM surface for the feed. |
| `tailscale.k8s.session` | log_event | `event_name="tailscale.k8s.session"` | visualized | Per-session record from the .cast header: namespace, pod, container, session type, command and the recorder that holds the recording. Read in Loki by event_name. |
| `tailscale.key.created` | log_event | `event_name="tailscale.key.created"` | visualized |  |
| `tailscale.key.expiring` | log_event | `event_name="tailscale.key.expiring"` | visualized, drives_a_variable |  |
| `tailscale.key.revoked` | log_event | `event_name="tailscale.key.revoked"` | visualized |  |
| `tailscale.key.scopes` | log_event | `event_name="tailscale.key.scopes"` | visualized |  |
| `tailscale.logstream.error` | log_event | `event_name="tailscale.logstream.error"` | visualized |  |
| `tailscale.network.flow` | log_event | `event_name="tailscale.network.flow"` | visualized, drives_a_variable |  |
| `tailscale.oauth_app.info` | log_event | `event_name="tailscale.oauth_app.info"` | visualized |  |
| `tailscale.posture_integrations.snapshot` | log_event | `event_name="tailscale.posture_integrations.snapshot"` | visualized |  |
| `tailscale.settings.snapshot` | log_event | `event_name="tailscale.settings.snapshot"` | visualized |  |
| `tailscale.user_invite.no_longer_open` | log_event | `event_name="tailscale.user_invite.no_longer_open"` | visualized |  |
| `tailscale.user_invite.observed` | log_event | `event_name="tailscale.user_invite.observed"` | visualized |  |
| `tailscale.webhook.<type>` | log_event | `event_name="tailscale.webhook.<type>"` | **UNASSIGNED** | The event name is templated per webhook category, so no literal selector can name it. The dashboard's log-event picker does reach it, with the glob tailscale.webhook.*, which a literal-name gate cannot see. |
| `tailscale.webhook_endpoints.event_mismatch` | log_event | `event_name="tailscale.webhook_endpoints.event_mismatch"` | visualized |  |
| `tailscale.webhooks.snapshot` | log_event | `event_name="tailscale.webhooks.snapshot"` | visualized |  |
| `tailscale.acl.autoapprovers` | metric | `tailscale_acl_autoapprovers_ratio` | visualized, alertable |  |
| `tailscale.acl.last_audit_change` | metric | `tailscale_acl_last_audit_change_seconds` | visualized |  |
| `tailscale.acl.last_changed` | metric | `tailscale_acl_last_changed_seconds` | visualized |  |
| `tailscale.acl.posture_gated_rules` | metric | `tailscale_acl_posture_gated_rules_ratio` | visualized |  |
| `tailscale.acl.rules` | metric | `tailscale_acl_rules_ratio` | visualized |  |
| `tailscale.acl.size` | metric | `tailscale_acl_size_bytes` | visualized |  |
| `tailscale.acl.ssh_wildcard` | metric | `tailscale_acl_ssh_wildcard_ratio` | visualized |  |
| `tailscale.acl.unrestricted_rules` | metric | `tailscale_acl_unrestricted_rules_ratio` | visualized, alertable, drives_a_variable |  |
| `tailscale.acl.validation.errors` | metric | `tailscale_acl_validation_errors_ratio` | visualized |  |
| `tailscale.acl.validation.ok` | metric | `tailscale_acl_validation_ok_ratio` | visualized |  |
| `tailscale.acl.validation.test_failures` | metric | `tailscale_acl_validation_test_failures_ratio` | visualized |  |
| `tailscale.acl.validation.warnings` | metric | `tailscale_acl_validation_warnings_ratio` | visualized |  |
| `tailscale.acl.wildcard_rules` | metric | `tailscale_acl_wildcard_rules_ratio` | visualized |  |
| `tailscale.config.audit.changes` | metric | `tailscale_config_audit_changes_total` | visualized, alertable, drives_a_variable |  |
| `tailscale.config.audit.deferred.delay` | metric | `tailscale_config_audit_deferred_delay_seconds` | visualized |  |
| `tailscale.config.audit.events` | metric | `tailscale_config_audit_events_total` | visualized, drives_a_variable |  |
| `tailscale.config.audit.processing.delay` | metric | `tailscale_config_audit_processing_delay_seconds` | visualized |  |
| `tailscale.config.audit.schema_drift` | metric | `tailscale_config_audit_schema_drift_total` | visualized, alertable |  |
| `tailscale.contact.needs_verification` | metric | `tailscale_contact_needs_verification_ratio` | visualized, alertable |  |
| `tailscale.derp.region.devices` | metric | `tailscale_derp_region_devices_ratio` | visualized, drives_a_variable |  |
| `tailscale.derp.region.latency_min` | metric | `tailscale_derp_region_latency_min_seconds` | visualized, alertable |  |
| `tailscale.derp.region.preferred` | metric | `tailscale_derp_region_preferred_ratio` | visualized |  |
| `tailscale.device.attribute` | metric | `tailscale_device_attribute_ratio` | visualized, drives_a_variable |  |
| `tailscale.device.attribute.expiry` | metric | `tailscale_device_attribute_expiry_seconds` | visualized, alertable | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Posture & Compliance - ALERTABLE-ONLY today. |
| `tailscale.device.attribute.info` | metric | `tailscale_device_attribute_info_ratio` | visualized |  |
| `tailscale.device.attributes.dropped` | metric | `tailscale_device_attributes_dropped_ratio` | visualized |  |
| `tailscale.device.blocks_incoming_connections` | metric | `tailscale_device_blocks_incoming_connections_ratio` | visualized, drives_a_variable | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.connectivity.direct_capable` | metric | `tailscale_device_connectivity_direct_capable_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.endpoints` | metric | `tailscale_device_connectivity_endpoints_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.hard_nat` | metric | `tailscale_device_connectivity_hard_nat_ratio` | visualized, drives_a_variable |  |
| `tailscale.device.connectivity.ipv6` | metric | `tailscale_device_connectivity_ipv6_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.udp` | metric | `tailscale_device_connectivity_udp_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.derp.latency` | metric | `tailscale_device_derp_latency_seconds` | visualized, drives_a_variable |  |
| `tailscale.device.distro` | metric | `tailscale_device_distro_ratio` | visualized | #526 wave 2/3: panel scheduled on tailnet/Devices > Inventory & Hygiene. |
| `tailscale.device.exit_node` | metric | `tailscale_device_exit_node_ratio` | visualized, drives_a_variable |  |
| `tailscale.device.key.expiry` | metric | `tailscale_device_key_expiry_seconds` | visualized, alertable, recorded |  |
| `tailscale.device.key_expiry_disabled` | metric | `tailscale_device_key_expiry_disabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.last_seen` | metric | `tailscale_device_last_seen_seconds` | visualized |  |
| `tailscale.device.multiple_connections` | metric | `tailscale_device_multiple_connections_ratio` | visualized, alertable | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.online` | metric | `tailscale_device_online_ratio` | visualized, recorded, drives_a_variable |  |
| `tailscale.device.posture` | metric | `tailscale_device_posture_ratio` | visualized, alertable, recorded, drives_a_variable |  |
| `tailscale.device.posture_identity.disabled` | metric | `tailscale_device_posture_identity_disabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.routes.advertised` | metric | `tailscale_device_routes_advertised` | visualized, drives_a_variable |  |
| `tailscale.device.routes.enabled` | metric | `tailscale_device_routes_enabled` | visualized |  |
| `tailscale.device.ssh_enabled` | metric | `tailscale_device_ssh_enabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.update_available` | metric | `tailscale_device_update_available_ratio` | visualized, alertable |  |
| `tailscale.device.version_skew` | metric | `tailscale_device_version_skew_ratio` | visualized, alertable |  |
| `tailscale.device_invites.count` | metric | `tailscale_device_invites_count_ratio` | visualized, alertable, drives_a_variable |  |
| `tailscale.device_invites.pending_age` | metric | `tailscale_device_invites_pending_age_seconds` | visualized |  |
| `tailscale.devices.age` | metric | `tailscale_devices_age_seconds` | visualized |  |
| `tailscale.devices.by_country` | metric | `tailscale_devices_by_country_ratio` | visualized | #526 wave 2/3: panel scheduled on tailnet/Devices > Inventory & Hygiene. |
| `tailscale.devices.by_distro` | metric | `tailscale_devices_by_distro_ratio` | visualized |  |
| `tailscale.devices.by_tag` | metric | `tailscale_devices_by_tag_ratio` | visualized |  |
| `tailscale.devices.by_version` | metric | `tailscale_devices_by_version_ratio` | visualized |  |
| `tailscale.devices.client_supports` | metric | `tailscale_devices_client_supports_ratio` | visualized |  |
| `tailscale.devices.count` | metric | `tailscale_devices_count_ratio` | visualized, alertable, recorded |  |
| `tailscale.devices.direct_capable` | metric | `tailscale_devices_direct_capable_ratio` | visualized |  |
| `tailscale.devices.ephemeral` | metric | `tailscale_devices_ephemeral_ratio` | visualized |  |
| `tailscale.devices.hard_nat` | metric | `tailscale_devices_hard_nat_ratio` | visualized, alertable, recorded |  |
| `tailscale.devices.key_expiry` | metric | `tailscale_devices_key_expiry_days` | visualized, drives_a_variable |  |
| `tailscale.devices.key_expiry_disabled` | metric | `tailscale_devices_key_expiry_disabled_ratio` | visualized, alertable |  |
| `tailscale.devices.outdated` | metric | `tailscale_devices_outdated_ratio` | visualized, alertable |  |
| `tailscale.devices.ssh_enabled` | metric | `tailscale_devices_ssh_enabled_ratio` | visualized |  |
| `tailscale.devices.untagged` | metric | `tailscale_devices_untagged_ratio` | visualized |  |
| `tailscale.dns.magic_dns` | metric | `tailscale_dns_magic_dns_ratio` | visualized |  |
| `tailscale.dns.nameservers.count` | metric | `tailscale_dns_nameservers_count_ratio` | visualized |  |
| `tailscale.dns.override_local` | metric | `tailscale_dns_override_local_ratio` | visualized |  |
| `tailscale.dns.resolver` | metric | `tailscale_dns_resolver_ratio` | visualized |  |
| `tailscale.dns.resolvers.use_with_exit_node` | metric | `tailscale_dns_resolvers_use_with_exit_node_ratio` | visualized |  |
| `tailscale.dns.search_path` | metric | `tailscale_dns_search_path_ratio` | visualized |  |
| `tailscale.dns.search_paths.count` | metric | `tailscale_dns_search_paths_count_ratio` | visualized |  |
| `tailscale.dns.split_zones.count` | metric | `tailscale_dns_split_zones_count_ratio` | visualized |  |
| `tailscale.exit_node.io` | metric | `tailscale_exit_node_io_bytes_total` | visualized, drives_a_variable |  |
| `tailscale.exit_node.packets` | metric | `tailscale_exit_node_packets_total` | visualized |  |
| `tailscale.exit_nodes.count` | metric | `tailscale_exit_nodes_count_ratio` | visualized |  |
| `tailscale.feature.enabled` | metric | `tailscale_feature_enabled_ratio` | visualized, alertable |  |
| `tailscale.fleet.latest_version` | metric | `tailscale_fleet_latest_version_ratio` | visualized |  |
| `tailscale.geoip.database.build_time` | metric | `tailscale_geoip_database_build_time_seconds` | visualized, alertable | #526 wave 2/3: panel scheduled on tailnet/Policy & Config > Integrations - ALERTABLE-ONLY today. |
| `tailscale.geoip.downloads` | metric | `tailscale_geoip_downloads_total` | visualized | #526 wave 2/3: panel scheduled on tailnet/Policy & Config > Integrations. |
| `tailscale.geoip.lookups` | metric | `tailscale_geoip_lookups_total` | visualized | #526 wave 2/3: panel scheduled on tailnet/Policy & Config > Integrations. |
| `tailscale.geoip.reloads` | metric | `tailscale_geoip_reloads_total` | visualized | #526 wave 2/3: panel scheduled on tailnet/Policy & Config > Integrations. |
| `tailscale.k8s.api.exec_sessions` | metric | `tailscale_k8s_api_exec_sessions_total` | visualized, drives_a_variable | kubectl exec/attach/port-forward attempts, dimensioned by the bounded command_class. Queried ad hoc in Loki alongside tailscale.k8s.api_request until a dashboard exists. |
| `tailscale.k8s.api.mutations` | metric | `tailscale_k8s_api_mutations_total` | visualized, drives_a_variable | create/update/patch/delete attempts. Counts what was REQUESTED, not what succeeded, so it cannot back a change-success panel and no rule uses it. |
| `tailscale.k8s.api.rbac_probes` | metric | `tailscale_k8s_api_rbac_probes_total` | visualized, drives_a_variable | SelfSubjectRulesReview/SelfSubjectAccessReview volume, the signature of permission enumeration. Charted by resource and namespace. Deliberately not alerted: it is normal for UI clients such as Freelens, so the interesting pattern is a burst from an unexpected user agent, which needs a cluster-specific baseline. |
| `tailscale.k8s.api.requests` | metric | `tailscale_k8s_api_requests_total` | visualized, drives_a_variable | Baseline Kubernetes API request volume, broken down by verb, namespace, resource and user agent on the Kubernetes Audit tab. Counts ATTEMPTS: the source carries no response status, so this is request volume, never a success or failure rate. |
| `tailscale.k8s.api.sensitive_reads` | metric | `tailscale_k8s_api_sensitive_reads_total` | visualized, drives_a_variable | Reads of secrets, service accounts and RBAC objects, charted by resource, namespace and user agent. A strong alerting candidate, but deliberately NOT wired to a rule: a useful threshold depends on the cluster's own baseline, and an arbitrary one would page on normal operator traffic. |
| `tailscale.k8s.schema_drift` | metric | `tailscale_k8s_schema_drift_total` | visualized, drives_a_variable | Guards an explicitly BETA upstream schema with no version field. Charted as a rate plus a range stat whose thresholds treat any drift as red, since a healthy feed reports nothing at all. Watch it after upgrading the operator or the recorder. |
| `tailscale.k8s.session.started` | metric | `tailscale_k8s_session_started_total` | visualized, drives_a_variable | Terminal sessions derived from .cast headers. Fires once at session start; session completeness is not observable from the bucket, so there is no duration metric to visualize alongside it. |
| `tailscale.key.allowed_tags` | metric | `tailscale_key_allowed_tags_ratio` | visualized |  |
| `tailscale.key.expiry` | metric | `tailscale_key_expiry_seconds` | visualized, alertable, drives_a_variable |  |
| `tailscale.key.preauthorized` | metric | `tailscale_key_preauthorized_ratio` | visualized |  |
| `tailscale.key.scope_class` | metric | `tailscale_key_scope_class_ratio` | visualized, alertable | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Identity & Keys - ALERTABLE-ONLY today. |
| `tailscale.key.scopes` | metric | `tailscale_key_scopes_ratio` | visualized, drives_a_variable |  |
| `tailscale.key.tag_scope` | metric | `tailscale_key_tag_scope_ratio` | visualized, alertable | #526 wave 2/3: panel scheduled on tailnet/Security & Audit > Identity & Keys - ALERTABLE-ONLY today. |
| `tailscale.keys.age` | metric | `tailscale_keys_age_seconds` | visualized |  |
| `tailscale.keys.by_owner` | metric | `tailscale_keys_by_owner_ratio` | visualized |  |
| `tailscale.keys.count` | metric | `tailscale_keys_count_ratio` | visualized |  |
| `tailscale.logstream.bytes_sent` | metric | `tailscale_logstream_bytes_sent_total` | visualized |  |
| `tailscale.logstream.configured` | metric | `tailscale_logstream_configured_ratio` | visualized, drives_a_variable |  |
| `tailscale.logstream.entries_sent` | metric | `tailscale_logstream_entries_sent_total` | visualized |  |
| `tailscale.logstream.error` | metric | `tailscale_logstream_error_ratio` | visualized |  |
| `tailscale.logstream.last_activity` | metric | `tailscale_logstream_last_activity_seconds` | visualized, alertable |  |
| `tailscale.logstream.max_body_requests` | metric | `tailscale_logstream_max_body_requests_total` | visualized, alertable |  |
| `tailscale.logstream.requests` | metric | `tailscale_logstream_requests_total` | visualized |  |
| `tailscale.logstream.requests_failed` | metric | `tailscale_logstream_requests_failed_total` | visualized, alertable |  |
| `tailscale.logstream.spoofed_entries` | metric | `tailscale_logstream_spoofed_entries_total` | visualized, alertable |  |
| `tailscale.network.data_quality` | metric | `tailscale_network_data_quality_total` | visualized |  |
| `tailscale.network.dedup.conflicts` | metric | `tailscale_network_dedup_conflicts_total` | visualized |  |
| `tailscale.network.field.observations` | metric | `tailscale_network_field_observations_total` | visualized |  |
| `tailscale.network.flow.logs_dropped` | metric | `tailscale_network_flow_logs_dropped_total` | visualized, alertable |  |
| `tailscale.network.flows` | metric | `tailscale_network_flows_total` | visualized, alertable, drives_a_variable |  |
| `tailscale.network.io` | metric | `tailscale_network_io_bytes_total` | visualized, recorded, drives_a_variable |  |
| `tailscale.network.io.rollup` | metric | `tailscale_network_io_rollup_bytes_total` | visualized, recorded, drives_a_variable |  |
| `tailscale.network.packets` | metric | `tailscale_network_packets_total` | visualized |  |
| `tailscale.network.packets.rollup` | metric | `tailscale_network_packets_rollup_total` | visualized |  |
| `tailscale.network.reporter.observations` | metric | `tailscale_network_reporter_observations_total` | visualized, alertable |  |
| `tailscale.network.store.dropped` | metric | `tailscale_network_store_dropped_total` | visualized |  |
| `tailscale.network.unique.dst_peers` | metric | `tailscale_network_unique_dst_peers` | visualized, drives_a_variable |  |
| `tailscale.network.unique.dst_ports` | metric | `tailscale_network_unique_dst_ports` | visualized |  |
| `tailscale.node.derp.home_region` | metric | `tailscale_node_derp_home_region_ratio` | visualized |  |
| `tailscale.node.health_messages` | metric | `tailscale_node_health_messages_ratio` | visualized, alertable |  |
| `tailscale.node.io` | metric | `tailscale_node_io_bytes_total` | visualized, drives_a_variable |  |
| `tailscale.node.packets` | metric | `tailscale_node_packets_total` | visualized | Packet counterpart of the visualized tailscale.node.io / tailscale.node.peer_relay.io byte counters; kept for ad-hoc PromQL rather than doubling every traffic panel. |
| `tailscale.node.packets.dropped` | metric | `tailscale_node_packets_dropped_total` | visualized, alertable |  |
| `tailscale.node.peer_relay.endpoints` | metric | `tailscale_node_peer_relay_endpoints_ratio` | visualized, alertable |  |
| `tailscale.node.peer_relay.io` | metric | `tailscale_node_peer_relay_io_bytes_total` | visualized |  |
| `tailscale.node.peer_relay.packets` | metric | `tailscale_node_peer_relay_packets_total` | visualized | Packet counterpart of the visualized tailscale.node.io / tailscale.node.peer_relay.io byte counters; kept for ad-hoc PromQL rather than doubling every traffic panel. |
| `tailscale.node.up` | metric | `tailscale_node_up_ratio` | visualized, alertable, drives_a_variable |  |
| `tailscale.oauth_app.node_attributes` | metric | `tailscale_oauth_app_node_attributes_ratio` | visualized |  |
| `tailscale.oauth_app.redirect_uris` | metric | `tailscale_oauth_app_redirect_uris_ratio` | visualized |  |
| `tailscale.oauth_app.scope_class` | metric | `tailscale_oauth_app_scope_class_ratio` | visualized |  |
| `tailscale.oauth_app.scopes` | metric | `tailscale_oauth_app_scopes_ratio` | visualized |  |
| `tailscale.oauth_apps.age` | metric | `tailscale_oauth_apps_age_seconds` | visualized |  |
| `tailscale.oauth_apps.count` | metric | `tailscale_oauth_apps_count_ratio` | visualized |  |
| `tailscale.posture_integration.error` | metric | `tailscale_posture_integration_error_ratio` | visualized, alertable | #526 wave 2/3: panel scheduled on tailnet/Policy & Config > Integrations - ALERTABLE-ONLY today. |
| `tailscale.posture_integration.last_sync` | metric | `tailscale_posture_integration_last_sync_seconds` | visualized, alertable |  |
| `tailscale.posture_integration.matched` | metric | `tailscale_posture_integration_matched_ratio` | visualized, alertable |  |
| `tailscale.posture_integration.possible_matched` | metric | `tailscale_posture_integration_possible_matched_ratio` | visualized, alertable |  |
| `tailscale.posture_integration.provider_hosts` | metric | `tailscale_posture_integration_provider_hosts_ratio` | visualized |  |
| `tailscale.posture_integrations.count` | metric | `tailscale_posture_integrations_count_ratio` | visualized, drives_a_variable |  |
| `tailscale.rdns.cache.capacity` | metric | `tailscale_rdns_cache_capacity_ratio` | visualized |  |
| `tailscale.rdns.cache.entries` | metric | `tailscale_rdns_cache_entries_ratio` | visualized, drives_a_variable |  |
| `tailscale.rdns.cache.evictions` | metric | `tailscale_rdns_cache_evictions_total` | visualized |  |
| `tailscale.rdns.cache.lookups` | metric | `tailscale_rdns_cache_lookups_total` | visualized |  |
| `tailscale.rdns.cache.overflows` | metric | `tailscale_rdns_cache_overflows_total` | visualized, alertable |  |
| `tailscale.rdns.queries` | metric | `tailscale_rdns_queries_total` | visualized |  |
| `tailscale.rdns.refreshes` | metric | `tailscale_rdns_refreshes_total` | visualized |  |
| `tailscale.service.host.info` | metric | `tailscale_service_host_info_ratio` | visualized |  |
| `tailscale.service.hosts` | metric | `tailscale_service_hosts_ratio` | visualized, alertable |  |
| `tailscale.service.ports` | metric | `tailscale_service_ports` | visualized, drives_a_variable |  |
| `tailscale.services.by_tag` | metric | `tailscale_services_by_tag_ratio` | visualized |  |
| `tailscale.services.count` | metric | `tailscale_services_count_ratio` | visualized, drives_a_variable |  |
| `tailscale.setting.devices_key_duration` | metric | `tailscale_setting_devices_key_duration_days` | visualized |  |
| `tailscale.setting.enabled` | metric | `tailscale_setting_enabled_ratio` | visualized, alertable |  |
| `tailscale.setting.users_external_tailnets_role` | metric | `tailscale_setting_users_external_tailnets_role_ratio` | visualized |  |
| `tailscale.stream.decode_errors` | metric | `tailscale_stream_decode_errors_total` | visualized |  |
| `tailscale.stream.inflight` | metric | `tailscale_stream_inflight` | visualized |  |
| `tailscale.stream.records` | metric | `tailscale_stream_records_total` | visualized, drives_a_variable |  |
| `tailscale.stream.rejected` | metric | `tailscale_stream_rejected_total` | visualized, alertable, recorded |  |
| `tailscale.stream.request.duration` | metric | `tailscale_stream_request_duration_seconds` | visualized, alertable, drives_a_variable |  |
| `tailscale.stream.skipped` | metric | `tailscale_stream_skipped_total` | visualized, alertable |  |
| `tailscale.subnet_routes.advertised` | metric | `tailscale_subnet_routes_advertised` | visualized |  |
| `tailscale.subnet_routes.enabled` | metric | `tailscale_subnet_routes_enabled` | visualized |  |
| `tailscale.subnet_routes.routers` | metric | `tailscale_subnet_routes_routers_ratio` | visualized, alertable |  |
| `tailscale.subnet_routes.unapproved` | metric | `tailscale_subnet_routes_unapproved` | visualized, alertable |  |
| `tailscale.tailnet_lock.errors` | metric | `tailscale_tailnet_lock_errors_ratio` | visualized, alertable, drives_a_variable |  |
| `tailscale.user.connected` | metric | `tailscale_user_connected_ratio` | visualized, drives_a_variable |  |
| `tailscale.user.devices` | metric | `tailscale_user_devices_ratio` | visualized |  |
| `tailscale.user.last_seen` | metric | `tailscale_user_last_seen_seconds` | visualized |  |
| `tailscale.user_invites.count` | metric | `tailscale_user_invites_count_ratio` | visualized |  |
| `tailscale.user_invites.pending_age` | metric | `tailscale_user_invites_pending_age_seconds` | visualized, alertable |  |
| `tailscale.users.age` | metric | `tailscale_users_age_seconds` | visualized |  |
| `tailscale.users.count` | metric | `tailscale_users_count_ratio` | visualized |  |
| `tailscale.webhook.duplicates` | metric | `tailscale_webhook_duplicates_total` | visualized |  |
| `tailscale.webhook.events` | metric | `tailscale_webhook_events_total` | visualized, alertable, drives_a_variable |  |
| `tailscale.webhook.inflight` | metric | `tailscale_webhook_inflight` | visualized |  |
| `tailscale.webhook.rejected` | metric | `tailscale_webhook_rejected_total` | visualized, alertable, recorded |  |
| `tailscale.webhook.request.duration` | metric | `tailscale_webhook_request_duration_seconds` | visualized |  |
| `tailscale.webhook.schema_drift` | metric | `tailscale_webhook_schema_drift_total` | visualized, alertable |  |
| `tailscale.webhook_endpoint.age` | metric | `tailscale_webhook_endpoint_age_seconds` | visualized |  |
| `tailscale.webhook_endpoint.subscriptions` | metric | `tailscale_webhook_endpoint_subscriptions_ratio` | visualized |  |
| `tailscale.webhook_endpoints.count` | metric | `tailscale_webhook_endpoints_count_ratio` | visualized |  |
| `tailscale.webhook_endpoints.desired_unrecognized` | metric | `tailscale_webhook_endpoints_desired_unrecognized_ratio` | visualized |  |
| `tailscale.webhook_endpoints.event_desired_covered` | metric | `tailscale_webhook_endpoints_event_desired_covered_ratio` | visualized |  |
| `tailscale.webhook_endpoints.event_subscriptions` | metric | `tailscale_webhook_endpoints_event_subscriptions_ratio` | visualized |  |
| `tailscale2otel.nodemetrics.discovery.success` | metric | `tailscale2otel_nodemetrics_discovery_success_ratio` | visualized, alertable |  |
| `tailscale2otel.nodemetrics.discovery.targets` | metric | `tailscale2otel_nodemetrics_discovery_targets` | visualized |  |
| `tailscale2otel.nodemetrics.metric_names.dropped` | metric | `tailscale2otel_nodemetrics_metric_names_dropped_total` | visualized, alertable |  |
| `tailscale2otel.nodemetrics.scrape.failures` | metric | `tailscale2otel_nodemetrics_scrape_failures_total` | visualized |  |
| `tailscale2otel.objectstore.backlog` | metric | `tailscale2otel_objectstore_backlog_ratio` | visualized, alertable, recorded |  |
| `tailscale2otel.objectstore.bytes` | metric | `tailscale2otel_objectstore_bytes_total` | visualized |  |
| `tailscale2otel.objectstore.cursor.age` | metric | `tailscale2otel_objectstore_cursor_age_seconds` | visualized |  |
| `tailscale2otel.objectstore.decompressed.bytes` | metric | `tailscale2otel_objectstore_decompressed_bytes_total` | visualized |  |
| `tailscale2otel.objectstore.discovered.newest.age` | metric | `tailscale2otel_objectstore_discovered_newest_age_seconds` | visualized, alertable |  |
| `tailscale2otel.objectstore.expansion.limit_failures` | metric | `tailscale2otel_objectstore_expansion_limit_failures_total` | visualized |  |
| `tailscale2otel.objectstore.gap.healthy` | metric | `tailscale2otel_objectstore_gap_healthy_ratio` | visualized |  |
| `tailscale2otel.objectstore.gap.oldest.age` | metric | `tailscale2otel_objectstore_gap_oldest_age_seconds` | visualized, alertable |  |
| `tailscale2otel.objectstore.gaps` | metric | `tailscale2otel_objectstore_gaps_ratio` | visualized |  |
| `tailscale2otel.objectstore.objects` | metric | `tailscale2otel_objectstore_objects_total` | visualized |  |
| `tailscale2otel.objectstore.pending.oldest.age` | metric | `tailscale2otel_objectstore_pending_oldest_age_seconds` | visualized |  |
| `tailscale2otel.objectstore.records` | metric | `tailscale2otel_objectstore_records_total` | visualized |  |
| `tailscale2otel.objectstore.request.duration` | metric | `tailscale2otel_objectstore_request_duration_seconds` | visualized |  |
| `tailscale2otel.objectstore.requests` | metric | `tailscale2otel_objectstore_requests_total` | visualized |  |
| `tailscale2otel.objectstore.retries` | metric | `tailscale2otel_objectstore_retries_total` | visualized |  |
| `tailscale2otel.objectstore.scan.truncated` | metric | `tailscale2otel_objectstore_scan_truncated_ratio` | visualized |  |
| `tailscale2otel.objectstore.skipped` | metric | `tailscale2otel_objectstore_skipped_total` | visualized, alertable, recorded |  |

## Self-observability signals

| signal | kind | queried as | disposition | note |
| --- | --- | --- | --- | --- |
| `process.cpu.time` | metric | `process_cpu_time_seconds_total` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (Go runtime). |
| `process.uptime` | metric | `process_uptime_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (Go runtime). |
| `tailscale2otel.admin.auth.rejected` | metric | `tailscale2otel_admin_auth_rejected_total` | visualized, alertable |  |
| `tailscale2otel.annotation.degraded` | metric | `tailscale2otel_annotation_degraded_ratio` | visualized | #526 wave 2/3: panel scheduled on health/Delivery (annotations). |
| `tailscale2otel.annotation.dropped` | metric | `tailscale2otel_annotation_dropped_total` | visualized | #526 wave 2/3: panel scheduled on health/Delivery (annotations). |
| `tailscale2otel.annotation.published` | metric | `tailscale2otel_annotation_published_total` | visualized | #526 wave 2/3: panel scheduled on health/Delivery (annotations). |
| `tailscale2otel.api.availability` | metric | `tailscale2otel_api_availability_ratio` | visualized, alertable |  |
| `tailscale2otel.api.duration` | metric | `tailscale2otel_api_duration_seconds` | visualized, drives_a_variable |  |
| `tailscale2otel.api.last_probe` | metric | `tailscale2otel_api_last_probe_seconds` | visualized |  |
| `tailscale2otel.api.rate_limit.utilization` | metric | `tailscale2otel_api_rate_limit_utilization_ratio` | visualized |  |
| `tailscale2otel.api.rate_limit.wait` | metric | `tailscale2otel_api_rate_limit_wait_seconds` | visualized, alertable |  |
| `tailscale2otel.api.requests` | metric | `tailscale2otel_api_requests_total` | visualized, alertable, recorded |  |
| `tailscale2otel.api.retries` | metric | `tailscale2otel_api_retries_total` | visualized, alertable, drives_a_variable |  |
| `tailscale2otel.build_info` | metric | `tailscale2otel_build_info_ratio` | visualized |  |
| `tailscale2otel.capability.scope_satisfied` | metric | `tailscale2otel_capability_scope_satisfied_ratio` | visualized |  |
| `tailscale2otel.capability.status` | metric | `tailscale2otel_capability_status_ratio` | visualized |  |
| `tailscale2otel.checkpoint.disk.size` | metric | `tailscale2otel_checkpoint_disk_size_bytes` | visualized |  |
| `tailscale2otel.checkpoint.persist.age` | metric | `tailscale2otel_checkpoint_persist_age_seconds` | visualized, alertable |  |
| `tailscale2otel.checkpoint.persist.errors` | metric | `tailscale2otel_checkpoint_persist_errors_total` | visualized, alertable |  |
| `tailscale2otel.component.errors` | metric | `tailscale2otel_component_errors_total` | visualized, alertable |  |
| `tailscale2otel.config.valid` | metric | `tailscale2otel_config_valid_ratio` | visualized, alertable |  |
| `tailscale2otel.config.warnings` | metric | `tailscale2otel_config_warnings_ratio` | visualized, alertable |  |
| `tailscale2otel.dedup.evictions` | metric | `tailscale2otel_dedup_evictions_total` | visualized, alertable |  |
| `tailscale2otel.dedup.hits` | metric | `tailscale2otel_dedup_hits_total` | visualized |  |
| `tailscale2otel.dedup.overlap_horizon` | metric | `tailscale2otel_dedup_overlap_horizon_seconds` | visualized, alertable |  |
| `tailscale2otel.dedup.size` | metric | `tailscale2otel_dedup_size_ratio` | visualized |  |
| `tailscale2otel.dedup.youngest_eviction_age` | metric | `tailscale2otel_dedup_youngest_eviction_age_seconds` | visualized, alertable |  |
| `tailscale2otel.enrich.cache_age` | metric | `tailscale2otel_enrich_cache_age_seconds` | visualized, alertable |  |
| `tailscale2otel.enrich.cache_size` | metric | `tailscale2otel_enrich_cache_size_ratio` | visualized |  |
| `tailscale2otel.export.datapoints` | metric | `tailscale2otel_export_datapoints_total` | visualized, alertable |  |
| `tailscale2otel.export.diagnostics.suppressed` | metric | `tailscale2otel_export_diagnostics_suppressed_total` | visualized | #526 wave 2/3: panel scheduled on health/Delivery (OTLP export). |
| `tailscale2otel.export.duration` | metric | `tailscale2otel_export_duration_seconds` | visualized, alertable, recorded, drives_a_variable |  |
| `tailscale2otel.export.failures` | metric | `tailscale2otel_export_failures_total` | visualized, alertable, drives_a_variable |  |
| `tailscale2otel.export.log_records` | metric | `tailscale2otel_export_log_records_total` | visualized |  |
| `tailscale2otel.export.spans` | metric | `tailscale2otel_export_spans_total` | visualized | #526 wave 2/3: panel scheduled on health/Delivery (OTLP export). |
| `tailscale2otel.flow_store.journal.size` | metric | `tailscale2otel_flow_store_journal_size_bytes` | visualized |  |
| `tailscale2otel.flow_store.last_checkpoint_timestamp` | metric | `tailscale2otel_flow_store_last_checkpoint_timestamp_seconds` | visualized |  |
| `tailscale2otel.ingest.capture.delay` | metric | `tailscale2otel_ingest_capture_delay_seconds` | visualized |  |
| `tailscale2otel.ingest.event.age` | metric | `tailscale2otel_ingest_event_age_seconds` | visualized |  |
| `tailscale2otel.ingest.last_event_timestamp` | metric | `tailscale2otel_ingest_last_event_timestamp_seconds` | visualized, alertable, recorded |  |
| `tailscale2otel.ingest.records` | metric | `tailscale2otel_ingest_records_total` | visualized, recorded, drives_a_variable |  |
| `tailscale2otel.ingest.size` | metric | `tailscale2otel_ingest_size_bytes_total` | visualized |  |
| `tailscale2otel.ingest.timestamp_skew` | metric | `tailscale2otel_ingest_timestamp_skew_total` | visualized |  |
| `tailscale2otel.ingress_wal.completion.markers` | metric | `tailscale2otel_ingress_wal_completion_markers_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.orphan.size` | metric | `tailscale2otel_ingress_wal_orphan_size_bytes` | visualized |  |
| `tailscale2otel.ingress_wal.orphan.stages` | metric | `tailscale2otel_ingress_wal_orphan_stages_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.pending.entries` | metric | `tailscale2otel_ingress_wal_pending_entries_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.pending.entries.fill` | metric | `tailscale2otel_ingress_wal_pending_entries_fill_ratio` | visualized, alertable |  |
| `tailscale2otel.ingress_wal.pending.size` | metric | `tailscale2otel_ingress_wal_pending_size_bytes` | visualized |  |
| `tailscale2otel.ingress_wal.pending.size.fill` | metric | `tailscale2otel_ingress_wal_pending_size_fill_ratio` | visualized, alertable |  |
| `tailscale2otel.log.record.truncated` | metric | `tailscale2otel_log_record_truncated_total` | visualized | #526 wave 2/3: panel scheduled on health/Ingestion (log truncation). |
| `tailscale2otel.log.truncated.bytes` | metric | `tailscale2otel_log_truncated_bytes_total` | visualized | #526 wave 2/3: panel scheduled on health/Ingestion (log truncation). |
| `tailscale2otel.metrics.auth.rejected` | metric | `tailscale2otel_metrics_auth_rejected_total` | visualized | #526 wave 2/3: panel scheduled on health/Collection (metrics endpoint). |
| `tailscale2otel.metrics.scrape.duration` | metric | `tailscale2otel_metrics_scrape_duration_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Collection (metrics endpoint). |
| `tailscale2otel.metrics.scrape.gather_errors` | metric | `tailscale2otel_metrics_scrape_gather_errors_total` | visualized | #526 wave 2/3: panel scheduled on health/Collection (metrics endpoint). |
| `tailscale2otel.metrics.scrape.in_flight` | metric | `tailscale2otel_metrics_scrape_in_flight` | visualized | #526 wave 2/3: panel scheduled on health/Collection (metrics endpoint). |
| `tailscale2otel.metrics.scrape.requests` | metric | `tailscale2otel_metrics_scrape_requests_total` | visualized | #526 wave 2/3: panel scheduled on health/Collection (metrics endpoint). |
| `tailscale2otel.pii_filter.category` | metric | `tailscale2otel_pii_filter_category_ratio` | visualized, drives_a_variable |  |
| `tailscale2otel.processor.dropped` | metric | `tailscale2otel_processor_dropped_total` | visualized | #526 wave 2/3: panel scheduled on health/Ingestion (processor queue). |
| `tailscale2otel.processor.queue.capacity` | metric | `tailscale2otel_processor_queue_capacity_ratio` | visualized | #526 wave 2/3: panel scheduled on health/Ingestion (processor queue). |
| `tailscale2otel.processor.queue.size` | metric | `tailscale2otel_processor_queue_size_ratio` | visualized | #526 wave 2/3: panel scheduled on health/Ingestion (processor queue). |
| `tailscale2otel.profiling.upload.attempts` | metric | `tailscale2otel_profiling_upload_attempts_total` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (profiling upload). |
| `tailscale2otel.profiling.upload.consecutive_failures` | metric | `tailscale2otel_profiling_upload_consecutive_failures_ratio` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (profiling upload). |
| `tailscale2otel.profiling.upload.duration` | metric | `tailscale2otel_profiling_upload_duration_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (profiling upload). |
| `tailscale2otel.profiling.upload.failures` | metric | `tailscale2otel_profiling_upload_failures_total` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (profiling upload). |
| `tailscale2otel.profiling.upload.last_success` | metric | `tailscale2otel_profiling_upload_last_success_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (profiling upload). |
| `tailscale2otel.receiver.misconfigured` | metric | `tailscale2otel_receiver_misconfigured_ratio` | visualized |  |
| `tailscale2otel.runtime.gc.count` | metric | `tailscale2otel_runtime_gc_count_total` | visualized |  |
| `tailscale2otel.runtime.gc.cpu_fraction` | metric | `tailscale2otel_runtime_gc_cpu_fraction_ratio` | visualized, alertable |  |
| `tailscale2otel.runtime.gc.next_target` | metric | `tailscale2otel_runtime_gc_next_target_bytes` | visualized |  |
| `tailscale2otel.runtime.gc.pause_time` | metric | `tailscale2otel_runtime_gc_pause_time_seconds_total` | visualized |  |
| `tailscale2otel.runtime.gomaxprocs` | metric | `tailscale2otel_runtime_gomaxprocs_ratio` | visualized |  |
| `tailscale2otel.runtime.goroutines` | metric | `tailscale2otel_runtime_goroutines_ratio` | visualized |  |
| `tailscale2otel.runtime.memory.alloc` | metric | `tailscale2otel_runtime_memory_alloc_bytes_total` | visualized |  |
| `tailscale2otel.runtime.memory.heap_alloc` | metric | `tailscale2otel_runtime_memory_heap_alloc_bytes` | visualized |  |
| `tailscale2otel.runtime.memory.heap_inuse` | metric | `tailscale2otel_runtime_memory_heap_inuse_bytes` | visualized |  |
| `tailscale2otel.runtime.memory.heap_objects` | metric | `tailscale2otel_runtime_memory_heap_objects_ratio` | visualized |  |
| `tailscale2otel.runtime.memory.heap_sys` | metric | `tailscale2otel_runtime_memory_heap_sys_bytes` | visualized |  |
| `tailscale2otel.runtime.memory.stack_inuse` | metric | `tailscale2otel_runtime_memory_stack_inuse_bytes` | visualized |  |
| `tailscale2otel.runtime.memory.sys` | metric | `tailscale2otel_runtime_memory_sys_bytes` | visualized |  |
| `tailscale2otel.scrape.budget` | metric | `tailscale2otel_scrape_budget_ratio` | visualized, alertable |  |
| `tailscale2otel.scrape.duration` | metric | `tailscale2otel_scrape_duration_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Collection (scrape/poll). |
| `tailscale2otel.scrape.duration.histogram` | metric | `tailscale2otel_scrape_duration_histogram_seconds` | visualized | Distribution of collector scrape durations, recorded with the scrape span so an exemplar links a bucket back to the trace. Now visualized: #369's follow-up replaced the flagship dashboard's last-value scrape-duration panel with p50/p95/p99 over this histogram, which is what the issue's acceptance asked for. The pre-existing scrape.duration gauge is retained alongside it. |
| `tailscale2otel.scrape.errors` | metric | `tailscale2otel_scrape_errors_total` | visualized, alertable, drives_a_variable |  |
| `tailscale2otel.scrape.last_timestamp` | metric | `tailscale2otel_scrape_last_timestamp_seconds` | visualized, alertable |  |
| `tailscale2otel.scrape.staleness` | metric | `tailscale2otel_scrape_staleness_seconds` | visualized, alertable, recorded, drives_a_variable |  |
| `tailscale2otel.scrape.success` | metric | `tailscale2otel_scrape_success_ratio` | visualized, alertable, recorded, drives_a_variable |  |
| `tailscale2otel.series.active` | metric | `tailscale2otel_series_active` | visualized, alertable, recorded, drives_a_variable |  |
| `tailscale2otel.series.by_group` | metric | `tailscale2otel_series_by_group` | visualized, recorded |  |
| `tailscale2otel.series.limit` | metric | `tailscale2otel_series_limit` | visualized, alertable |  |
| `tailscale2otel.series.overflowing` | metric | `tailscale2otel_series_overflowing_ratio` | visualized, alertable |  |
| `tailscale2otel.subrequest.attempts` | metric | `tailscale2otel_subrequest_attempts_total` | visualized |  |
| `tailscale2otel.subrequest.coverage` | metric | `tailscale2otel_subrequest_coverage_ratio` | visualized |  |
| `tailscale2otel.subrequest.failures` | metric | `tailscale2otel_subrequest_failures_total` | visualized |  |
| `tailscale2otel.tls.cert.not_after` | metric | `tailscale2otel_tls_cert_not_after_seconds` | visualized, alertable | #526 wave 2/3: panel scheduled on health/Runtime (TLS cert reload) - ALERTABLE-ONLY today. |
| `tailscale2otel.tls.cert.not_before` | metric | `tailscale2otel_tls_cert_not_before_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (TLS cert reload). |
| `tailscale2otel.tls.cert.reload.failures` | metric | `tailscale2otel_tls_cert_reload_failures_total` | visualized, alertable | #526 wave 2/3: panel scheduled on health/Runtime (TLS cert reload) - ALERTABLE-ONLY today. |
| `tailscale2otel.tls.cert.reload.last_success` | metric | `tailscale2otel_tls_cert_reload_last_success_seconds` | visualized | #526 wave 2/3: panel scheduled on health/Runtime (TLS cert reload). |
| `tailscale2otel.up` | metric | `tailscale2otel_up_ratio` | visualized, alertable, recorded |  |
| `tailscale2otel.update_available` | metric | `tailscale2otel_update_available_ratio` | visualized, alertable |  |

<!-- END GENERATED -->
