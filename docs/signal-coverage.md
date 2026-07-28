---
title: Signal Coverage
description: Which emitted metrics and log events are charted, alerted on, recorded, or deliberately query-only
---

# Signal coverage

Every metric and log event tailscale2otel emits is accounted for here. The table
says what each signal is *for* — whether the shipped dashboard charts it, an alert
rule watches it, a recording rule folds it, or it is deliberately query-only.

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
| `visualized` | queried by at least one panel or template variable in the generated dashboard |
| `alertable` | queried by at least one alert rule expression |
| `recorded` | consumed by at least one recording rule expression |
| `raw_only` | deliberately query-only: real and supported, but meant for ad-hoc PromQL/LogQL rather than a curated panel — usually high-detail per-entity series whose cardinality makes a default panel a bad idea |
| `omitted` | deliberately not surfaced at all |

A signal can carry several at once (most alerted metrics are also charted).
`raw_only` and `omitted` are mutually exclusive with each other and with the three
derived values — a signal on a curated surface is not deliberately absent from one.

**Operational and self-observability coverage are reported separately.**
They answer different questions ("can I see my tailnet?" versus "can I see the
exporter?"), and a well-covered fleet story would otherwise hide a blind exporter.

The units, descriptions and attribute keys for every signal below are in
[`metrics.md`](metrics.md); this page only covers what each one is used for.

<!-- BEGIN GENERATED: signal-coverage -->

## Summary

A signal can carry more than one disposition, so the columns do not sum to the total.

| surface | signals | visualized | alertable | recorded | raw_only | omitted |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| operational | 209 | 194 | 57 | 11 | 10 | 0 |
| self_obs | 71 | 65 | 29 | 9 | 2 | 2 |

## Operational signals

| signal | kind | queried as | disposition | note |
| --- | --- | --- | --- | --- |
| `tailscale.acl.risky_rule` | log_event | `event_name="tailscale.acl.risky_rule"` | raw_only | WARN log emitted once per unrestricted ACL/grant rule; read in Loki by event_name during a policy review rather than from a standing panel. |
| `tailscale.acl.validation_issue` | log_event | `event_name="tailscale.acl.validation_issue"` | visualized |  |
| `tailscale.config.audit` | log_event | `event_name="tailscale.config.audit"` | visualized, alertable |  |
| `tailscale.device.attribute.expiring` | log_event | `event_name="tailscale.device.attribute.expiring"` | raw_only | Per-device WARN detail behind the alertable tailscale.device.attribute.expiry metric: the metric carries the alert, this log names which device and attribute. |
| `tailscale.device.key_expiring` | log_event | `event_name="tailscale.device.key_expiring"` | raw_only | Per-device WARN detail behind the visualized+alertable tailscale.device.key.expiry metric: the metric carries the surface, this log names the device. |
| `tailscale.device.posture` | log_event | `event_name="tailscale.device.posture"` | visualized |  |
| `tailscale.device.tailnet_lock_error` | log_event | `event_name="tailscale.device.tailnet_lock_error"` | visualized |  |
| `tailscale.device_invite` | log_event | `event_name="tailscale.device_invite"` | raw_only | Per-invite INFO log, gated off by default (collectors.users.collect_device_invites); queried by event_name when it is enabled. |
| `tailscale.key.expiring` | log_event | `event_name="tailscale.key.expiring"` | visualized |  |
| `tailscale.key.scopes` | log_event | `event_name="tailscale.key.scopes"` | visualized |  |
| `tailscale.logstream.error` | log_event | `event_name="tailscale.logstream.error"` | visualized |  |
| `tailscale.network.flow` | log_event | `event_name="tailscale.network.flow"` | visualized |  |
| `tailscale.oauth_app.info` | log_event | `event_name="tailscale.oauth_app.info"` | visualized |  |
| `tailscale.webhook.<type>` | log_event | `event_name="tailscale.webhook.<type>"` | raw_only | The event name is templated per webhook category, so no literal selector can name it. The dashboard's log-event picker does reach it, with the glob tailscale.webhook.*, which a literal-name gate cannot see. |
| `tailscale.webhook_endpoints.event_mismatch` | log_event | `event_name="tailscale.webhook_endpoints.event_mismatch"` | visualized |  |
| `tailscale.acl.autoapprovers` | metric | `tailscale_acl_autoapprovers_ratio` | visualized, alertable |  |
| `tailscale.acl.last_changed` | metric | `tailscale_acl_last_changed_seconds` | visualized |  |
| `tailscale.acl.posture_gated_rules` | metric | `tailscale_acl_posture_gated_rules_ratio` | visualized |  |
| `tailscale.acl.rules` | metric | `tailscale_acl_rules_ratio` | visualized |  |
| `tailscale.acl.size` | metric | `tailscale_acl_size_bytes` | visualized |  |
| `tailscale.acl.ssh_wildcard` | metric | `tailscale_acl_ssh_wildcard_ratio` | visualized |  |
| `tailscale.acl.unrestricted_rules` | metric | `tailscale_acl_unrestricted_rules_ratio` | visualized, alertable |  |
| `tailscale.acl.validation.errors` | metric | `tailscale_acl_validation_errors_ratio` | visualized |  |
| `tailscale.acl.validation.ok` | metric | `tailscale_acl_validation_ok_ratio` | visualized |  |
| `tailscale.acl.validation.test_failures` | metric | `tailscale_acl_validation_test_failures_ratio` | visualized |  |
| `tailscale.acl.validation.warnings` | metric | `tailscale_acl_validation_warnings_ratio` | visualized |  |
| `tailscale.acl.wildcard_rules` | metric | `tailscale_acl_wildcard_rules_ratio` | visualized |  |
| `tailscale.config.audit.changes` | metric | `tailscale_config_audit_changes_total` | visualized, alertable |  |
| `tailscale.config.audit.deferred.delay` | metric | `tailscale_config_audit_deferred_delay_seconds` | visualized |  |
| `tailscale.config.audit.events` | metric | `tailscale_config_audit_events_total` | visualized |  |
| `tailscale.config.audit.processing.delay` | metric | `tailscale_config_audit_processing_delay_seconds` | visualized |  |
| `tailscale.config.audit.schema_drift` | metric | `tailscale_config_audit_schema_drift_total` | visualized, alertable |  |
| `tailscale.contact.needs_verification` | metric | `tailscale_contact_needs_verification_ratio` | visualized, alertable |  |
| `tailscale.derp.region.devices` | metric | `tailscale_derp_region_devices_ratio` | visualized |  |
| `tailscale.derp.region.latency_min` | metric | `tailscale_derp_region_latency_min_seconds` | visualized, alertable |  |
| `tailscale.derp.region.preferred` | metric | `tailscale_derp_region_preferred_ratio` | visualized |  |
| `tailscale.device.attribute` | metric | `tailscale_device_attribute_ratio` | visualized |  |
| `tailscale.device.attribute.expiry` | metric | `tailscale_device_attribute_expiry_seconds` | alertable |  |
| `tailscale.device.attribute.info` | metric | `tailscale_device_attribute_info_ratio` | visualized |  |
| `tailscale.device.blocks_incoming_connections` | metric | `tailscale_device_blocks_incoming_connections_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.connectivity.direct_capable` | metric | `tailscale_device_connectivity_direct_capable_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.endpoints` | metric | `tailscale_device_connectivity_endpoints_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.hard_nat` | metric | `tailscale_device_connectivity_hard_nat_ratio` | visualized |  |
| `tailscale.device.connectivity.ipv6` | metric | `tailscale_device_connectivity_ipv6_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.connectivity.udp` | metric | `tailscale_device_connectivity_udp_ratio` | visualized | One series per device, describing that device's NAT/transport situation; deliberately query-only for drill-down onto a host that is stuck on DERP. |
| `tailscale.device.derp.latency` | metric | `tailscale_device_derp_latency_seconds` | visualized |  |
| `tailscale.device.distro` | metric | `tailscale_device_distro_ratio` | raw_only | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.exit_node` | metric | `tailscale_device_exit_node_ratio` | visualized |  |
| `tailscale.device.key.expiry` | metric | `tailscale_device_key_expiry_seconds` | visualized, alertable, recorded |  |
| `tailscale.device.key_expiry_disabled` | metric | `tailscale_device_key_expiry_disabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.last_seen` | metric | `tailscale_device_last_seen_seconds` | visualized |  |
| `tailscale.device.multiple_connections` | metric | `tailscale_device_multiple_connections_ratio` | visualized, alertable | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.online` | metric | `tailscale_device_online_ratio` | visualized, recorded |  |
| `tailscale.device.posture` | metric | `tailscale_device_posture_ratio` | visualized, alertable, recorded |  |
| `tailscale.device.posture_identity.disabled` | metric | `tailscale_device_posture_identity_disabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.routes.advertised` | metric | `tailscale_device_routes_advertised` | visualized |  |
| `tailscale.device.routes.enabled` | metric | `tailscale_device_routes_enabled` | visualized |  |
| `tailscale.device.ssh_enabled` | metric | `tailscale_device_ssh_enabled_ratio` | visualized | One series per device: deliberately query-only, for ad-hoc drill-down onto a named host rather than a default panel that would carry the whole fleet. |
| `tailscale.device.update_available` | metric | `tailscale_device_update_available_ratio` | visualized, alertable |  |
| `tailscale.device.version_skew` | metric | `tailscale_device_version_skew_ratio` | visualized, alertable |  |
| `tailscale.device_invites.count` | metric | `tailscale_device_invites_count_ratio` | visualized, alertable |  |
| `tailscale.device_invites.pending_age` | metric | `tailscale_device_invites_pending_age_seconds` | visualized |  |
| `tailscale.devices.age` | metric | `tailscale_devices_age_seconds` | visualized |  |
| `tailscale.devices.by_country` | metric | `tailscale_devices_by_country_ratio` | raw_only | Fleet geography, emitted only when the opt-in enrichment.geoip is configured. Deliberately not on the shipped dashboard: the feature is off by default, so a panel would render empty for almost every deployment, and a geographic breakdown is a question operators ask ad hoc rather than watch. Query it directly when you want it. |
| `tailscale.devices.by_distro` | metric | `tailscale_devices_by_distro_ratio` | visualized |  |
| `tailscale.devices.by_tag` | metric | `tailscale_devices_by_tag_ratio` | visualized |  |
| `tailscale.devices.by_version` | metric | `tailscale_devices_by_version_ratio` | visualized |  |
| `tailscale.devices.client_supports` | metric | `tailscale_devices_client_supports_ratio` | visualized |  |
| `tailscale.devices.count` | metric | `tailscale_devices_count_ratio` | visualized, alertable, recorded |  |
| `tailscale.devices.direct_capable` | metric | `tailscale_devices_direct_capable_ratio` | visualized |  |
| `tailscale.devices.ephemeral` | metric | `tailscale_devices_ephemeral_ratio` | visualized |  |
| `tailscale.devices.hard_nat` | metric | `tailscale_devices_hard_nat_ratio` | visualized, alertable, recorded |  |
| `tailscale.devices.key_expiry` | metric | `tailscale_devices_key_expiry_days` | visualized |  |
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
| `tailscale.exit_node.io` | metric | `tailscale_exit_node_io_bytes_total` | visualized |  |
| `tailscale.exit_node.packets` | metric | `tailscale_exit_node_packets_total` | visualized |  |
| `tailscale.exit_nodes.count` | metric | `tailscale_exit_nodes_count_ratio` | visualized |  |
| `tailscale.feature.enabled` | metric | `tailscale_feature_enabled_ratio` | visualized, alertable |  |
| `tailscale.fleet.latest_version` | metric | `tailscale_fleet_latest_version_ratio` | visualized |  |
| `tailscale.geoip.database.build_time` | metric | `tailscale_geoip_database_build_time_seconds` | alertable |  |
| `tailscale.geoip.downloads` | metric | `tailscale_geoip_downloads_total` | raw_only | Per-edition download outcomes for the opt-in MaxMind updater. Not alerted on directly: a run of failures is only a problem once it makes the database stale, which ts2o-geoip-database-stale catches on the build date. This is the metric that then tells you WHY — failure vs unmodified. |
| `tailscale.geoip.lookups` | metric | `tailscale_geoip_lookups_total` | raw_only | Enrichment hit/miss/skipped accounting for an opt-in feature that is off by default. Useful when answering 'why is this address not enriched' — a high country miss rate means the database does not cover the traffic, a high skipped count means the addresses were not globally routable — but it is a debugging query, not a standing panel. |
| `tailscale.geoip.reloads` | metric | `tailscale_geoip_reloads_total` | raw_only | Counts database hot-swaps for an opt-in feature. A reload failure already logs a WARN naming the file, and staleness is covered by the ts2o-geoip-database-stale alert on build_time, which is the signal that actually matters. Query this when diagnosing a specific swap. |
| `tailscale.key.allowed_tags` | metric | `tailscale_key_allowed_tags_ratio` | visualized |  |
| `tailscale.key.expiry` | metric | `tailscale_key_expiry_seconds` | visualized, alertable |  |
| `tailscale.key.preauthorized` | metric | `tailscale_key_preauthorized_ratio` | visualized |  |
| `tailscale.key.scope_class` | metric | `tailscale_key_scope_class_ratio` | alertable |  |
| `tailscale.key.scopes` | metric | `tailscale_key_scopes_ratio` | visualized |  |
| `tailscale.key.tag_scope` | metric | `tailscale_key_tag_scope_ratio` | alertable |  |
| `tailscale.keys.age` | metric | `tailscale_keys_age_seconds` | visualized |  |
| `tailscale.keys.by_owner` | metric | `tailscale_keys_by_owner_ratio` | visualized |  |
| `tailscale.keys.count` | metric | `tailscale_keys_count_ratio` | visualized |  |
| `tailscale.logstream.bytes_sent` | metric | `tailscale_logstream_bytes_sent_bytes_total` | visualized |  |
| `tailscale.logstream.configured` | metric | `tailscale_logstream_configured_ratio` | visualized |  |
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
| `tailscale.network.flows` | metric | `tailscale_network_flows_total` | visualized, alertable |  |
| `tailscale.network.io` | metric | `tailscale_network_io_bytes_total` | visualized, recorded |  |
| `tailscale.network.io.rollup` | metric | `tailscale_network_io_rollup_bytes_total` | visualized, recorded |  |
| `tailscale.network.packets` | metric | `tailscale_network_packets_total` | visualized |  |
| `tailscale.network.packets.rollup` | metric | `tailscale_network_packets_rollup_total` | visualized |  |
| `tailscale.network.reporter.observations` | metric | `tailscale_network_reporter_observations_total` | visualized, alertable |  |
| `tailscale.network.store.dropped` | metric | `tailscale_network_store_dropped_total` | visualized |  |
| `tailscale.network.unique.dst_peers` | metric | `tailscale_network_unique_dst_peers` | visualized |  |
| `tailscale.network.unique.dst_ports` | metric | `tailscale_network_unique_dst_ports` | visualized |  |
| `tailscale.node.derp.home_region` | metric | `tailscale_node_derp_home_region_ratio` | visualized |  |
| `tailscale.node.health_messages` | metric | `tailscale_node_health_messages_ratio` | visualized, alertable |  |
| `tailscale.node.io` | metric | `tailscale_node_io_bytes_total` | visualized |  |
| `tailscale.node.packets` | metric | `tailscale_node_packets_total` | visualized | Packet counterpart of the visualized tailscale.node.io / tailscale.node.peer_relay.io byte counters; kept for ad-hoc PromQL rather than doubling every traffic panel. |
| `tailscale.node.packets.dropped` | metric | `tailscale_node_packets_dropped_total` | visualized, alertable |  |
| `tailscale.node.peer_relay.endpoints` | metric | `tailscale_node_peer_relay_endpoints_ratio` | visualized, alertable |  |
| `tailscale.node.peer_relay.io` | metric | `tailscale_node_peer_relay_io_bytes_total` | visualized |  |
| `tailscale.node.peer_relay.packets` | metric | `tailscale_node_peer_relay_packets_total` | visualized | Packet counterpart of the visualized tailscale.node.io / tailscale.node.peer_relay.io byte counters; kept for ad-hoc PromQL rather than doubling every traffic panel. |
| `tailscale.node.up` | metric | `tailscale_node_up_ratio` | visualized, alertable |  |
| `tailscale.oauth_app.node_attributes` | metric | `tailscale_oauth_app_node_attributes_ratio` | visualized |  |
| `tailscale.oauth_app.redirect_uris` | metric | `tailscale_oauth_app_redirect_uris_ratio` | visualized |  |
| `tailscale.oauth_app.scope_class` | metric | `tailscale_oauth_app_scope_class_ratio` | visualized |  |
| `tailscale.oauth_app.scopes` | metric | `tailscale_oauth_app_scopes_ratio` | visualized |  |
| `tailscale.oauth_apps.age` | metric | `tailscale_oauth_apps_age_seconds` | visualized |  |
| `tailscale.oauth_apps.count` | metric | `tailscale_oauth_apps_count_ratio` | visualized |  |
| `tailscale.posture_integration.error` | metric | `tailscale_posture_integration_error_ratio` | alertable |  |
| `tailscale.posture_integration.last_sync` | metric | `tailscale_posture_integration_last_sync_seconds` | visualized, alertable |  |
| `tailscale.posture_integration.matched` | metric | `tailscale_posture_integration_matched_ratio` | visualized, alertable |  |
| `tailscale.posture_integration.possible_matched` | metric | `tailscale_posture_integration_possible_matched_ratio` | visualized, alertable |  |
| `tailscale.posture_integration.provider_hosts` | metric | `tailscale_posture_integration_provider_hosts_ratio` | visualized |  |
| `tailscale.posture_integrations.count` | metric | `tailscale_posture_integrations_count_ratio` | visualized |  |
| `tailscale.rdns.cache.capacity` | metric | `tailscale_rdns_cache_capacity_ratio` | visualized |  |
| `tailscale.rdns.cache.entries` | metric | `tailscale_rdns_cache_entries_ratio` | visualized |  |
| `tailscale.rdns.cache.evictions` | metric | `tailscale_rdns_cache_evictions_total` | visualized |  |
| `tailscale.rdns.cache.lookups` | metric | `tailscale_rdns_cache_lookups_total` | visualized |  |
| `tailscale.rdns.cache.overflows` | metric | `tailscale_rdns_cache_overflows_total` | visualized, alertable |  |
| `tailscale.rdns.queries` | metric | `tailscale_rdns_queries_total` | visualized |  |
| `tailscale.rdns.refreshes` | metric | `tailscale_rdns_refreshes_total` | visualized |  |
| `tailscale.service.hosts` | metric | `tailscale_service_hosts_ratio` | visualized, alertable |  |
| `tailscale.service.ports` | metric | `tailscale_service_ports` | visualized |  |
| `tailscale.services.count` | metric | `tailscale_services_count_ratio` | visualized |  |
| `tailscale.setting.devices_key_duration` | metric | `tailscale_setting_devices_key_duration_days` | visualized |  |
| `tailscale.setting.enabled` | metric | `tailscale_setting_enabled_ratio` | visualized, alertable |  |
| `tailscale.setting.users_external_tailnets_role` | metric | `tailscale_setting_users_external_tailnets_role_ratio` | visualized |  |
| `tailscale.stream.decode_errors` | metric | `tailscale_stream_decode_errors_total` | visualized |  |
| `tailscale.stream.inflight` | metric | `tailscale_stream_inflight` | visualized |  |
| `tailscale.stream.records` | metric | `tailscale_stream_records_total` | visualized |  |
| `tailscale.stream.rejected` | metric | `tailscale_stream_rejected_total` | visualized, alertable, recorded |  |
| `tailscale.stream.request.duration` | metric | `tailscale_stream_request_duration_seconds` | visualized, alertable |  |
| `tailscale.stream.skipped` | metric | `tailscale_stream_skipped_total` | visualized, alertable |  |
| `tailscale.subnet_routes.advertised` | metric | `tailscale_subnet_routes_advertised` | visualized |  |
| `tailscale.subnet_routes.enabled` | metric | `tailscale_subnet_routes_enabled` | visualized |  |
| `tailscale.subnet_routes.routers` | metric | `tailscale_subnet_routes_routers_ratio` | visualized, alertable |  |
| `tailscale.subnet_routes.unapproved` | metric | `tailscale_subnet_routes_unapproved` | visualized, alertable |  |
| `tailscale.tailnet_lock.errors` | metric | `tailscale_tailnet_lock_errors_ratio` | visualized, alertable |  |
| `tailscale.user.connected` | metric | `tailscale_user_connected_ratio` | visualized |  |
| `tailscale.user.devices` | metric | `tailscale_user_devices_ratio` | visualized |  |
| `tailscale.user.last_seen` | metric | `tailscale_user_last_seen_seconds` | visualized |  |
| `tailscale.user_invites.count` | metric | `tailscale_user_invites_count_ratio` | visualized |  |
| `tailscale.user_invites.pending_age` | metric | `tailscale_user_invites_pending_age_seconds` | visualized, alertable |  |
| `tailscale.users.age` | metric | `tailscale_users_age_seconds` | visualized |  |
| `tailscale.users.count` | metric | `tailscale_users_count_ratio` | visualized |  |
| `tailscale.webhook.duplicates` | metric | `tailscale_webhook_duplicates_total` | visualized |  |
| `tailscale.webhook.events` | metric | `tailscale_webhook_events_total` | visualized, alertable |  |
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
| `tailscale2otel.objectstore.backlog` | metric | `tailscale2otel_objectstore_backlog_ratio` | visualized, alertable, recorded |  |
| `tailscale2otel.objectstore.bytes` | metric | `tailscale2otel_objectstore_bytes_bytes_total` | visualized |  |
| `tailscale2otel.objectstore.cursor.age` | metric | `tailscale2otel_objectstore_cursor_age_seconds` | visualized |  |
| `tailscale2otel.objectstore.decompressed.bytes` | metric | `tailscale2otel_objectstore_decompressed_bytes_bytes_total` | visualized |  |
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
| `process.cpu.time` | metric | `process_cpu_time_seconds_total` | omitted | Standard OTEL process-level metric, deliberately left to a generic runtime dashboard rather than duplicated on this exporter's own surfaces. |
| `process.uptime` | metric | `process_uptime_seconds` | omitted | Standard OTEL process-level metric, deliberately left to a generic runtime dashboard rather than duplicated on this exporter's own surfaces. |
| `tailscale2otel.admin.auth.rejected` | metric | `tailscale2otel_admin_auth_rejected_total` | visualized, alertable |  |
| `tailscale2otel.api.availability` | metric | `tailscale2otel_api_availability_ratio` | visualized, alertable |  |
| `tailscale2otel.api.duration` | metric | `tailscale2otel_api_duration_seconds` | visualized |  |
| `tailscale2otel.api.last_probe` | metric | `tailscale2otel_api_last_probe_seconds` | visualized |  |
| `tailscale2otel.api.rate_limit.wait` | metric | `tailscale2otel_api_rate_limit_wait_seconds` | visualized, alertable |  |
| `tailscale2otel.api.requests` | metric | `tailscale2otel_api_requests_total` | visualized, alertable, recorded |  |
| `tailscale2otel.api.retries` | metric | `tailscale2otel_api_retries_total` | visualized, alertable |  |
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
| `tailscale2otel.dedup.size` | metric | `tailscale2otel_dedup_size_ratio` | visualized |  |
| `tailscale2otel.enrich.cache_age` | metric | `tailscale2otel_enrich_cache_age_seconds` | visualized, alertable |  |
| `tailscale2otel.enrich.cache_size` | metric | `tailscale2otel_enrich_cache_size_ratio` | visualized |  |
| `tailscale2otel.export.datapoints` | metric | `tailscale2otel_export_datapoints_total` | visualized, alertable |  |
| `tailscale2otel.export.duration` | metric | `tailscale2otel_export_duration_seconds` | visualized, alertable, recorded |  |
| `tailscale2otel.export.failures` | metric | `tailscale2otel_export_failures_total` | visualized, alertable |  |
| `tailscale2otel.export.log_records` | metric | `tailscale2otel_export_log_records_total` | visualized |  |
| `tailscale2otel.ingest.capture.delay` | metric | `tailscale2otel_ingest_capture_delay_seconds` | visualized |  |
| `tailscale2otel.ingest.event.age` | metric | `tailscale2otel_ingest_event_age_seconds` | visualized |  |
| `tailscale2otel.ingest.last_event_timestamp` | metric | `tailscale2otel_ingest_last_event_timestamp_seconds` | visualized, alertable, recorded |  |
| `tailscale2otel.ingest.records` | metric | `tailscale2otel_ingest_records_total` | visualized, recorded |  |
| `tailscale2otel.ingest.size` | metric | `tailscale2otel_ingest_size_bytes_total` | visualized |  |
| `tailscale2otel.ingest.timestamp_skew` | metric | `tailscale2otel_ingest_timestamp_skew_total` | visualized |  |
| `tailscale2otel.ingress_wal.completion.markers` | metric | `tailscale2otel_ingress_wal_completion_markers_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.orphan.size` | metric | `tailscale2otel_ingress_wal_orphan_size_bytes` | visualized |  |
| `tailscale2otel.ingress_wal.orphan.stages` | metric | `tailscale2otel_ingress_wal_orphan_stages_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.pending.entries` | metric | `tailscale2otel_ingress_wal_pending_entries_ratio` | visualized |  |
| `tailscale2otel.ingress_wal.pending.size` | metric | `tailscale2otel_ingress_wal_pending_size_bytes` | visualized |  |
| `tailscale2otel.pii_filter.category` | metric | `tailscale2otel_pii_filter_category_ratio` | visualized |  |
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
| `tailscale2otel.scrape.duration` | metric | `tailscale2otel_scrape_duration_seconds` | visualized |  |
| `tailscale2otel.scrape.errors` | metric | `tailscale2otel_scrape_errors_total` | visualized, alertable |  |
| `tailscale2otel.scrape.last_timestamp` | metric | `tailscale2otel_scrape_last_timestamp_seconds` | visualized, alertable |  |
| `tailscale2otel.scrape.staleness` | metric | `tailscale2otel_scrape_staleness_seconds` | visualized, alertable, recorded |  |
| `tailscale2otel.scrape.success` | metric | `tailscale2otel_scrape_success_ratio` | visualized, alertable, recorded |  |
| `tailscale2otel.series.active` | metric | `tailscale2otel_series_active` | visualized, alertable, recorded |  |
| `tailscale2otel.series.by_group` | metric | `tailscale2otel_series_by_group` | visualized, recorded |  |
| `tailscale2otel.series.limit` | metric | `tailscale2otel_series_limit` | visualized, alertable |  |
| `tailscale2otel.series.overflowing` | metric | `tailscale2otel_series_overflowing_ratio` | visualized, alertable |  |
| `tailscale2otel.subrequest.attempts` | metric | `tailscale2otel_subrequest_attempts_total` | visualized |  |
| `tailscale2otel.subrequest.coverage` | metric | `tailscale2otel_subrequest_coverage_ratio` | visualized |  |
| `tailscale2otel.subrequest.failures` | metric | `tailscale2otel_subrequest_failures_total` | visualized |  |
| `tailscale2otel.tls.cert.not_after` | metric | `tailscale2otel_tls_cert_not_after_seconds` | alertable |  |
| `tailscale2otel.tls.cert.not_before` | metric | `tailscale2otel_tls_cert_not_before_seconds` | raw_only | Read alongside not_after when confirming WHICH certificate a listener actually loaded. Not alerted on: a not-yet-valid certificate fails the handshake immediately, so an alert would only restate a symptom the client errors already show. |
| `tailscale2otel.tls.cert.reload.failures` | metric | `tailscale2otel_tls_cert_reload_failures_total` | alertable |  |
| `tailscale2otel.tls.cert.reload.last_success` | metric | `tailscale2otel_tls_cert_reload_last_success_seconds` | raw_only | The companion to reload.failures, read on the status page while investigating one. Not alerted on: a listener whose certificate legitimately never rotates keeps an old timestamp forever, so any threshold would fire on correct behaviour. |
| `tailscale2otel.up` | metric | `tailscale2otel_up_ratio` | visualized, alertable, recorded |  |
| `tailscale2otel.update_available` | metric | `tailscale2otel_update_available_ratio` | visualized, alertable |  |

<!-- END GENERATED -->
