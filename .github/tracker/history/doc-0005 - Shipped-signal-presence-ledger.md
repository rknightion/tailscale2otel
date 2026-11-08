---
id: doc-0005
title: Shipped-signal presence ledger
type: other
created_date: '2026-09-05 22:03'
updated_date: '2026-09-05 22:03'
tags:
  - reference
---
The Wave 13 baseline found these 116 shipped signal families absent over 30 days. This ledger records the Wave 14 verdict after the lab ran RC.27 and every enabled collector completed successfully. `now-present` requires absence in the baseline and presence in the Wave 14 service-scoped query.

| family | kind | verdict | evidence or firing condition |
| --- | --- | --- | --- |
| tailscale2otel.api.rate_limit.wait | metric | expected-quiet | Fires when a Tailscale API request waits a non-zero duration on the configured client-side rate limiter before its first attempt. |
| tailscale2otel.capability.scope_satisfied | metric | dead | Live status reports satisfied modeled scopes, but the series is absent; `EmitScopePreflight` has no production call site. Filed as TSO-0142. |
| tailscale2otel.checkpoint.persist.errors | metric | expected-quiet | Fires when a completed collector or receiver window cannot persist its checkpoint high-water mark. |
| tailscale2otel.component.errors | metric | expected-quiet | Fires when a non-collector subsystem operation returns an error. |
| tailscale2otel.export.diagnostics.suppressed | metric | expected-quiet | Fires when a sustained OTLP outage causes an export-failure diagnostic log line to be rate-limited. |
| tailscale2otel.ingress_wal.completion.markers | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.orphan.size | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.orphan.stages | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.pending.entries | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.pending.entries.fill | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.pending.size | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.ingress_wal.pending.size.fill | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale2otel.metrics.scrape.gather_errors | metric | expected-quiet | Fires when the Prometheus registry returns a Gather error while serving `/metrics`. |
| tailscale2otel.tls.cert.not_after | metric | unexercisable-on-lab | Missing dependency: the lab receivers use internal plaintext endpoints and have no streaming or webhook TLS certificate/key pair. |
| tailscale2otel.tls.cert.not_before | metric | unexercisable-on-lab | Missing dependency: the lab receivers use internal plaintext endpoints and have no streaming or webhook TLS certificate/key pair. |
| tailscale2otel.tls.cert.reload.failures | metric | unexercisable-on-lab | Missing dependency: the lab receivers use internal plaintext endpoints and have no streaming or webhook TLS certificate/key pair. |
| tailscale2otel.tls.cert.reload.last_success | metric | unexercisable-on-lab | Missing dependency: the lab receivers use internal plaintext endpoints and have no streaming or webhook TLS certificate/key pair. |
| tailscale.config.audit.deferred.delay | metric | expected-quiet | Fires when an audit record carries ordered `eventTime` and `deferredAt` timestamps. |
| tailscale.network.data_quality | metric | expected-quiet | Fires when a decoded flow record fails bounded semantic validation. |
| tailscale.network.dedup.conflicts | metric | expected-quiet | Fires when a duplicate flow identity arrives with counters that conflict with the first observation. |
| tailscale.network.flow.logs_dropped | metric | expected-quiet | Fires when flow log records exceed `collectors.flowlogs.max_log_records_per_window`. |
| tailscale.network.store.dropped | metric | expected-quiet | Fires when a flow observation falls outside the local flow-view retention or future-skew bounds. |
| tailscale.device.attribute.expiry | metric | expected-quiet | Fires when a collected allow-listed posture attribute carries an explicit expiry timestamp. |
| tailscale.device_invites.pending_age | metric | expected-quiet | Fires when at least one outstanding device-share invite has a known creation timestamp. |
| tailscale.devices.posture_compliance.failed | metric | expected-quiet | Fires when a device fails one of the configured exact-match posture compliance checks. |
| tailscale.organization.tailnets.count | metric | unexercisable-on-lab | Missing dependency: the lab has no organization-inventory configuration and `tailnets:read` organization scope. |
| tailscale.user_invites.count | metric | expected-quiet | Fires when at least one open user invite is returned by the API. |
| tailscale.user_invites.pending_age | metric | expected-quiet | Fires when at least one open emailed user invite has a delivery timestamp. |
| tailscale.oauth_app.node_attributes | metric | expected-quiet | Fires when at least one OAuth application has one or more allowed custom node attributes. |
| tailscale.oauth_app.redirect_uris | metric | expected-quiet | Fires when at least one OAuth application has one or more redirect URIs. |
| tailscale.oauth_app.scope_class | metric | expected-quiet | Fires when at least one OAuth application exists. |
| tailscale.oauth_app.scopes | metric | expected-quiet | Fires when at least one OAuth application has one or more scopes. |
| tailscale.oauth_apps.age | metric | expected-quiet | Fires when at least one OAuth application has a known creation timestamp. |
| tailscale.pam.connector.connected | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connector.info | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connector.last_seen_age | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connector.plugins | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connector.sockets | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connector.tokens | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.connectors | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.identities | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.org.plan.info | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.org.setting.enabled | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.policies | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.policy.setting.enabled | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.service.alive | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.service.setting.enabled | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.services | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.session.duration | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.session.events | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.sessions | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.pam.sessions.active | metric | expected-quiet | Fires when the newest-first PAM polling prefix contains a session with no end time. |
| tailscale.pam.sessions.killed | metric | expected-quiet | Fires when a newly accepted PAM session is marked killed by the source. |
| tailscale.pam.subscription.limit | metric | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.webhook_endpoint.age | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook_endpoint.subscriptions | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook_endpoints.desired_unrecognized | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook_endpoints.event_desired_covered | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.logstream.spoofed_entries | metric | expected-quiet | Fires when Tailscale reports one or more log-stream entries rejected as spoofed. |
| tailscale.stream.decode_errors | metric | expected-quiet | Fires when a stream record classifies as flow or audit but fails typed decoding. |
| tailscale.stream.rejected | metric | expected-quiet | Fires when the stream receiver rejects a whole request for one of its bounded auth, size, admission, parse, validation, or WAL reasons. |
| tailscale.webhook.duplicates | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook.events | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook.inflight | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook.rejected | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook.request.duration | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook.schema_drift | metric | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale2otel.objectstore.backlog | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.bytes | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.cursor.age | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.decompressed.bytes | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.discovered.newest.age | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.expansion.limit_failures | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.gap.healthy | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.gap.oldest.age | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.gaps | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.objects | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.pending.oldest.age | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.records | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.request.duration | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.requests | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.retries | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.scan.truncated | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale2otel.objectstore.skipped | metric | unexercisable-on-lab | Missing dependency: the lab has no S3 log export, destination bucket, or object-store credentials; deploying that source is outside this wave. |
| tailscale.node.peer_relay.io | metric | expected-quiet | Fires when a discovered node exports forwarded peer-relay byte counters. |
| tailscale.node.peer_relay.packets | metric | expected-quiet | Fires when a discovered node exports forwarded peer-relay packet counters. |
| tailscale2otel.nodemetrics.metric_names.dropped | metric | expected-quiet | Fires when a node-metrics scrape presents a new metric name after `max_distinct_metrics` is exhausted. |
| tailscale.rdns.cache.overflows | metric | expected-quiet | Fires when a reverse-DNS miss cannot be scheduled because the cache has reached `max_entries`. |
| tailscale.k8s.api.exec_sessions | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.api.mutations | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.api.rbac_probes | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.api.requests | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.api.sensitive_reads | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.schema_drift | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.session.started | metric | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.acl.policy_diff | log | expected-quiet | Fires when snapshot emission is enabled and a later ACL policy revision differs from its retained baseline. |
| tailscale.acl.policy_snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.acl.risky_rule | log | expected-quiet | Fires when a changed ACL policy contains an unrestricted rule, wildcard SSH rule, or wildcard auto-approver. |
| tailscale.acl.validation_issue | log | expected-quiet | Fires when ACL validation returns at least one error, warning, or test failure. |
| tailscale.device.attribute.expiring | log | expected-quiet | Fires when a collected posture attribute has an expiry within the fixed 14-day warning window. |
| tailscale.device.change | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.device.tailnet_lock_error | log | expected-quiet | Fires when a collected device reports a non-empty tailnet-lock error. |
| tailscale.dns.snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.k8s.api_request | log | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.k8s.session | log | unexercisable-on-lab | Missing dependency: the lab has no tsrecorder Kubernetes-audit export or backing object-store source; deploying it is outside this wave. |
| tailscale.key.revoked | log | expected-quiet | Fires when a newly observed key record carries a non-zero source revocation timestamp. |
| tailscale.oauth_app.info | log | expected-quiet | Fires when at least one OAuth application exists. |
| tailscale.pam.session | log | expected-quiet | Fires when session logging is enabled and a PAM session is newly accepted beyond the durable cursor. |
| tailscale.pam.snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.posture_integrations.snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.settings.snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
| tailscale.user_invite.no_longer_open | log | expected-quiet | Fires when an invite present in a prior successful open-invite snapshot disappears from a later one. |
| tailscale.user_invite.observed | log | expected-quiet | Fires when an open user invite is first observed in a successful snapshot. |
| tailscale.webhook.<type> | log | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhook_endpoints.event_mismatch | log | unexercisable-on-lab | Missing dependency: the lab has no registered Tailscale webhook subscription and receiver secret feeding this path. |
| tailscale.webhooks.snapshot | log | now-present | Observed after the RC.27 rollout in the trailing 30-day service-scoped query; absent in the Wave 13 baseline. |
