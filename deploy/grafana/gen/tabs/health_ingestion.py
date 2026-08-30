"""tab_health_ingestion() — the "Ingestion" leaf tab on tailscale2otel-health (#526).

Second pipeline stage: data that has arrived and is being buffered, decoded and
de-duplicated before delivery. Content here is a mix of:

  - object-store, ingress-WAL, subrequest and receiver rows moved verbatim (as
    panel definitions) from the pre-#526 `tabs/diagnostics.py` "Exporter
    Diagnostics" tab;
  - the ingestion-side rows of the pre-#526 `tabs/events.py` "Events & Logs" tab
    (#526 decision 9: ~34 of that tab's 43 panels are exporter-pipeline health
    and move here or to Delivery — this module takes the intake/dedup/freshness
    half, leaving PII-filter status, SIEM log shipping and annotation-adjacent
    panels for Delivery, and leaving the ~9 genuine tailnet-event panels
    (event-rate summary, log explorer, per-signal flow/posture logs) on the
    product dashboard);
  - two new rows — "Processor queue" and "Log truncation" — covering five
    catalog signals that reached no panel anywhere before #526
    (tailscale2otel.processor.queue.size/.capacity/.dropped,
    tailscale2otel.log.record.truncated, tailscale2otel.log.truncated.bytes).
  - the ingress-WAL capacity-fill and dedup eviction-age rows added by
    TSO-0060/TSO-0065.

Consolidation (#526 decision 7): several near-duplicate single-purpose panels
from the two source tabs are merged into one multi-series panel where the
merged queries share a unit (so no dual-axis override is needed) — e.g. object
listing loss (skipped/retried/limit-stopped), webhook accepted-vs-rejected, or
ingest records-vs-rejections by source. No signal loses its last panel: every
metric charted by a source row is still charted here, just alongside its
nearest neighbour instead of alone in its own row.

`has_selfobs` gates the Dedup row but is NOT declared here at DASHBOARD scope — see the
docstring note below: this tab now declares its OWN tab-scoped copy, same reasoning as
`has_stream`/`has_webhook`/etc. above.

#526 second pass — two more sources feed this module:

1. The last three rows of the pre-split `tabs/events.py` "Events & Logs" tab (issue decision
   9's final piece): "Audit pipeline state" (the #393 four-state discriminator, deliberately
   UNGATED — see its own comment below), "Audit pipeline latency" and "Audit schema drift"
   (both gated `has_audit`, declared fresh here since the tab that used to carry it does not
   survive the split). `TNP`/`LOKI_TN`/`sel()`/`q_hist()`/`STATE_KEY`/`CFG_DOC` are copied
   from events.py verbatim — that module keeps its own copies for the rows that stayed there
   (Audit & event rates).
2. "Cardinality & dedup" from the now-dissolved `tabs/health_internals.py` does NOT feed this
   module — its two dedup panels went to cardinality.py's Cost & Cardinality tab (task 1 of
   this pass), where they turned out to be byte-identical duplicates of a row already there,
   so nothing was added. This tab's own "Cross-source dedup" row (tailscale2otel_dedup_hits_
   total / _evictions_total / _size_ratio) is unchanged by this pass.

Consolidation (#526 decision 7, second pass) to hold this tab at or under the 35-panel
ceiling after the audit-row addition:
  - "Audit events by ingestion path" (from the incoming state row) is DROPPED as a standalone
    panel — its data (records by source, filtered to signal="audit") is a strict subset of
    the existing "Ingest records/s & rejections/s by source" panel below, which already
    breaks out by source AND signal. It is not one of the #393 four-state discriminators
    (those are the scrape gauge, the accepted counter, the Loki counter and the failures
    panel — see STATE_KEY), so dropping it does not weaken the row's diagnostic job.
  - the two object-store rows merge two pairs of same-subject, different-unit panels via a
    right-axis override (objects/records ingested + bytes read/decompressed; provider
    requests + provider latency) — 11 panels -> 9.
  - "Ingest records/s & rejections/s by source" and "Ingest decoded bytes/s by source" merge
    into one panel via a right-axis override — 2 -> 1.
  - the two "Log truncation" panels (record count vs bytes, same field breakdown) merge into
    one via a right-axis override — 2 -> 1.
  - "Timestamp skew/s" folds into "Accepted event freshness & age p95" as a fourth
    right-axis series — 2 -> 1.
"""

from builder import (bargauge_opts, CFG_DOC, hq, LOKI_TN, loki_t, lot, organize,
                     panel, prom_t, q_hist, RI, row, sel, sentinel, STATE_KEY,
                     stat_opts, TBL_NOISE, thr, ts_custom, ts_opts, vmap,
                     WIN_FAST, WIN_SLOW)
from maps import bool_map, BOOL_HEALTHY_ON

# Empty-state copy, carried over verbatim from tabs/diagnostics.py (#526 — this
# tab does not edit that file, but the prerequisite wording is still accurate
# for the panels moved here).
_OBJ_EMPTY = ("No object-store ingestion series. Requires collectors.flowlogs.objectstore "
              "(or collectors.auditlogs.objectstore) with the matching source: objectstore, "
              "and self_observability.enabled.")
_WAL_EMPTY = ("No ingress-WAL series. Requires ingress_wal.enabled together with a receiver "
              "that accepts payloads (streaming or webhook), and self_observability.enabled.")
_DEDUP_AGE_EMPTY = ("No dedup eviction-age series. Requires self_observability.enabled and "
                    "at least one dedup set eviction.")
_SUBREQ_EMPTY = ("No per-entity subrequest series. Requires a collector that fans out per entity "
                 "— devices posture attributes, device invites, or user invites — and "
                 "self_observability.enabled.")
_STREAM_EMPTY = ("No streaming-receiver series. Requires streaming.enabled with "
                 "collectors.flowlogs.source or collectors.auditlogs.source set to stream.")
_WEBHOOK_EMPTY = ("No webhook-receiver series. Requires webhook.enabled and at least one Tailscale "
                  "webhook endpoint delivering to it.")
_QUEUE_EMPTY = ("No processor-queue series. Requires self_observability.enabled and at least one "
                "log or trace export path active (the queue only exists once something is being "
                "batched for OTLP export).")


def tab_health_ingestion(scope):
    # Presence sentinels this tab declares, at the scope build.py passed in
    # (builder.tab_scope("Ingestion")). These four previously lived in
    # tabs/events.py at DASHBOARD scope for the pre-split single-dashboard
    # "Events & Logs" tab; that tab does not survive the split (#526 decision
    # 9), so the rows that still need them declare them fresh, scoped to the
    # one tab that now consumes them.
    sentinel("has_stream", "tailscale_stream_records_total", scope)
    # Same reason as the Collection tab's three: tabs/diagnostics.py used to declare
    # has_selfobs at DASHBOARD scope for everyone, and it is gone. The Overview tab's
    # copy is tab-scoped and therefore invisible here. Duplicating the name across two
    # TAB scopes is verified-safe: Grafana resolves a tab-scoped variable per tab, so
    # each tab's rows gate on their own declaration (probed live, #526).
    sentinel("has_selfobs", "tailscale2otel_series_active", scope)
    sentinel("has_webhook", "tailscale_webhook_events_total", scope)
    sentinel("has_recv_dur", "tailscale_stream_request_duration_seconds_count", scope)
    sentinel("has_ingest", "tailscale2otel_ingest_records_total", scope)
    # Moved from tabs/events.py (#526 decision 9, final piece): gates "Audit pipeline
    # latency" and "Audit schema drift" below. Declared fresh at THIS tab's scope — the
    # module that used to own it at DASHBOARD scope for the pre-split tab is gone.
    sentinel("has_audit", "tailscale_config_audit_events_total", scope)

    # --- object-store ingestion (flow/audit export bucket) — moved from
    # tabs/diagnostics.py #399. Every panel relies on its own empty state
    # rather than a presence sentinel: the whole family is absent on a poll-
    # or stream-fed deployment.
    objstore_status = [
        (panel("Object-store age (cursor & newest object)", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_objectstore_cursor_age_seconds"), legend="cursor age"),
                prom_t("max(%s)" % lot("tailscale2otel_objectstore_discovered_newest_age_seconds"), legend="newest object age", refid="B")],
               unit="s", options=stat_opts(graph="area"),
               mappings=vmap({"-1": {"text": "no timestamped object listed", "color": "orange", "index": 0}}),
               novalue=_OBJ_EMPTY,
               desc="End-to-end ingestion lag (now minus the last cycle's persisted timestamp) "
                    "beside how fresh the EXPORT's own writes are, independent of what was "
                    "ingested. -1 on the second series means the cycle listed no object with a "
                    "usable timestamp, deliberately distinguishable from a fresh zero-second "
                    "age."), 8, 6),
        (panel("Object-store gaps clear", "stat",
               [prom_t("min(%s)" % lot("tailscale2otel_objectstore_gap_healthy_ratio"))],
               mappings=bool_map("GAPS", "CLEAN", "red", "green"),
               thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="One when no unresolved gap remains; zero means at least one pending or "
                    "quarantined object is outstanding."), 8, 6),
        (panel("Object listing complete", "stat",
               [prom_t("max(%s)" % lot("tailscale2otel_objectstore_scan_truncated_ratio"))],
               mappings=bool_map("complete", "truncated", "green", "red"),
               thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="One means unexamined listing ground remains after the last cycle, which "
                    "makes the backlog and oldest-pending-age panels lower bounds rather than "
                    "totals."), 8, 6),
        (panel("Backlog & oldest pending object age", "timeseries",
               [prom_t("max(tailscale2otel_objectstore_backlog_ratio)", legend="objects waiting"),
                prom_t("max(tailscale2otel_objectstore_pending_oldest_age_seconds)", legend="oldest pending age", refid="B")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byName", "options": "oldest pending age"},
                           "properties": [{"id": "unit", "value": "s"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="Objects listed but not yet ingested, and how stale the next one to be "
                    "processed already is. Zero here pairs with a zero backlog and means "
                    "nothing is queued."), 12, 7),
        (panel("Unresolved gaps & oldest gap age", "timeseries",
               [prom_t("max(tailscale2otel_objectstore_gaps_ratio)", legend="failed objects"),
                prom_t("max(tailscale2otel_objectstore_gap_oldest_age_seconds)", legend="oldest gap age", refid="B")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byName", "options": "oldest gap age"},
                           "properties": [{"id": "unit", "value": "s"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="FAILED objects awaiting retry or operator acknowledgement, and the age of "
                    "the oldest. A gap aging without the count falling is an object failing "
                    "repeatedly rather than recovering."), 12, 7),
    ]
    # Consolidation (#526 decision 7, second pass): "Objects & records ingested/s" (cps) and
    # "Object bytes read vs decompressed" (Bps) merge into one panel via a right-axis
    # override — same subject (object-store throughput), different unit, no metric lost.
    objstore_throughput = [
        (panel("Objects & records ingested/s, bytes read vs decompressed", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_objects_total[%s]))" % RI, legend="objects/s"),
                prom_t("sum(rate(tailscale2otel_objectstore_records_total[%s]))" % RI, legend="records/s", refid="B"),
                prom_t("sum(rate(tailscale2otel_objectstore_bytes_total[%s]))" % RI, legend="compressed read", refid="C"),
                prom_t("sum(rate(tailscale2otel_objectstore_decompressed_bytes_total[%s]))" % RI, legend="decompressed", refid="D")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byName", "options": "compressed read"},
                           "properties": [{"id": "unit", "value": "Bps"}, {"id": "custom.axisPlacement", "value": "right"}]},
                          {"matcher": {"id": "byName", "options": "decompressed"},
                           "properties": [{"id": "unit", "value": "Bps"}, {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="Objects fully ingested and flow-log records decoded out of them (left "
                    "axis, cps), beside transfer cost (compressed bytes actually read) "
                    "against expansion (decompressed bytes consumed, right axis, Bps)."), 12, 7),
        (panel("Object ingestion loss (skipped / retried / limit-stopped)", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale2otel_objectstore_skipped_total[%s]))" % RI, legend="skipped {{reason}}"),
                prom_t("sum(rate(tailscale2otel_objectstore_retries_total[%s]))" % RI, legend="retries/s", refid="B"),
                prom_t("sum by (limit) (rate(tailscale2otel_objectstore_expansion_limit_failures_total[%s]))" % RI,
                       legend="limit-stopped {{limit}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="Every reason an object or row was not ingested cleanly, on one axis: "
                    "per-row skips (decode_error/semantic_invalid are single rows discarded "
                    "from an otherwise-complete object; per_cycle_budget means the per-cycle "
                    "object cap is holding ingestion behind the bucket), object-level retries "
                    "(a failed object becomes a durable gap, retried under bounded backoff), "
                    "and attempts stopped by a configured wire-byte/decompressed-byte/record-"
                    "count budget."), 12, 7),
        (panel("Undecodable objects (broken feed)", "stat",
               [prom_t('sum(increase(tailscale2otel_objectstore_skipped_total{reason="undecodable_object"}[$__range])) or vector(0)',
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"), novalue=_OBJ_EMPTY,
               desc="Whole objects that decoded ZERO records while at least one row failed — the "
                    "signature of an export whose framing is not newline-delimited records. Treat "
                    "any non-zero value as a feed-level fault, not corrupt data."), 12, 7),
        # Consolidation (#526 decision 7, second pass): provider requests (reqps) and
        # provider latency quantiles (s) merge into one panel via a right-axis override —
        # same subject (object-store provider call health), different unit, no metric lost.
        (panel("Object-store provider requests/s & latency", "timeseries",
               [prom_t("sum by (operation, outcome) (rate(tailscale2otel_objectstore_requests_total[%s]))" % RI,
                       legend="{{operation}} {{outcome}}"),
                prom_t(hq("0.5", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p50 {{operation}}", refid="B"),
                prom_t(hq("0.95", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p95 {{operation}}", refid="C"),
                prom_t(hq("0.99", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p99 {{operation}}", refid="D")],
               unit="reqps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               overrides=[{"matcher": {"id": "byRegexp", "options": "p(50|95|99) .+"},
                           "properties": [{"id": "unit", "value": "s"}]}],
               desc="TRANSPORT health only: `error` means the LIST or GET call itself returned "
                    "an error, never a decode/validation/framing/limit failure (those are the "
                    "ingestion-loss panel above). A body that fails mid-read counts as a "
                    "SUCCESSFUL get here. Latency quantiles (p50/p95/p99, right axis in "
                    "seconds) time the provider call itself — for `get` that is obtaining the "
                    "object's reader, not streaming/decompressing/decoding its body."), 24, 7),
    ]
    # --- durable ingress WAL (receiver acceptance before processing), moved
    # from tabs/diagnostics.py #386.
    wal = [
        (panel("Ingress WAL on-disk bytes", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_size_bytes)", legend="pending"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_size_bytes)", legend="retained staging", refid="B")],
               unit="bytes", custom=ts_custom(stack="normal"), options=ts_opts(), novalue=_WAL_EMPTY,
               desc="Encoded bytes on the WAL filesystem. There is no TTL or eviction, so this "
                    "is bounded only by ingress_wal.max_bytes."), 12, 6),
        (panel("Ingress WAL capacity fill", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_entries_fill_ratio)", legend="entries fill"),
                prom_t("max(tailscale2otel_ingress_wal_pending_size_fill_ratio)", legend="bytes fill", refid="B"),
                prom_t("max(tailscale2otel_ingress_wal_pending_entries_ratio)", legend="pending entries", refid="C"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_stages_ratio)", legend="retained staging files", refid="D"),
                prom_t("max(tailscale2otel_ingress_wal_completion_markers_ratio)", legend="completion markers", refid="E")],
               unit="percentunit", min_=0, max_=1, custom=ts_custom(), options=ts_opts(), novalue=_WAL_EMPTY,
               overrides=[{"matcher": {"id": "byRegexp", "options": "pending entries|retained staging files|completion markers"},
                           "properties": [{"id": "unit", "value": "short"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               desc="Durable pending entries and bytes as fractions of their configured ingress-WAL "
                    "limits (left axis), with raw pending entries, retained staging files and "
                    "completion markers on the right axis. The warning threshold is 80%, before "
                    "either limit fails new requests closed with HTTP 503."), 12, 6),
    ]
    # --- per-entity subrequest fan-out, moved from tabs/diagnostics.py #386.
    subreq = [
        (panel("Subrequest attempts & failures/s", "timeseries",
               [prom_t("sum by (tailscale_collector, tailscale_subrequest) (rate(tailscale2otel_subrequest_attempts_total[%s]))" % RI,
                       legend="attempts {{tailscale_collector}}/{{tailscale_subrequest}}"),
                prom_t("sum by (tailscale_subrequest, tailscale_api_state) (rate(tailscale2otel_subrequest_failures_total[%s]))" % RI,
                       legend="failures {{tailscale_subrequest}}/{{tailscale_api_state}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_SUBREQ_EMPTY,
               desc="Per-entity calls made inside a scrape (one posture-attributes call per "
                    "device, and so on) — the N+1 cost a single scrape-duration figure hides — "
                    "beside how many of them failed. A single entity's subrequest failing is "
                    "non-fatal to the enclosing snapshot, so the collector still reports a clean "
                    "scrape while coverage degrades; a scope_denied state is a missing OAuth "
                    "scope, not a transient error."), 12, 7),
        (panel("Subrequest coverage (last pass)", "bargauge",
               [prom_t("min by (tailscale_collector, tailscale_subrequest) (%s)" % lot("tailscale2otel_subrequest_coverage_ratio"),
                       legend="{{tailscale_collector}} / {{tailscale_subrequest}}")],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "red"), (0.9, "yellow"), (1, "green")]),
               options=bargauge_opts(), novalue=_SUBREQ_EMPTY,
               desc="Fraction of per-entity subrequests that succeeded on the last pass. 1 when "
                    "nothing was attempted: an empty tailnet has complete coverage of the "
                    "entities it has."), 12, 7),
    ]
    # --- stream (HEC) record intake, moved from tabs/events.py.
    stream = [
        (panel("Stream records/s by type", "timeseries",
               [prom_t("sum by (type) (rate(tailscale_stream_records_total[%s]))" % RI, legend="records {{type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Records accepted from the configured stream (HEC) receiver, by record "
                    "type."), 12, 7),
        (panel("Stream rejections & decode errors/s", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="rejected {{reason}}"),
                prom_t("sum by (type) (rate(tailscale_stream_decode_errors_total[%s]))" % RI, legend="decode error {{type}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="WHOLE requests refused before any record was extracted (rejected, by "
                    "bounded reason) beside records extracted but that failed to decode into a "
                    "known record type. Read against the accepted rate above."), 12, 7),
    ]
    # --- webhook intake, moved from tabs/events.py.
    webhook = [
        (panel("Webhook events by type & rejections by reason", "timeseries",
               [prom_t("sum by (tailscale_webhook_type) (rate(tailscale_webhook_events_total[%s]))" % RI, legend="accepted {{tailscale_webhook_type}}"),
                prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="rejected {{reason}}", refid="B")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(placement="right"),
               desc="Webhook events accepted, by event type, beside deliveries rejected (bad "
                    "signature, unknown event, etc.) by reason — accepted volume and its loss "
                    "counter on one axis."), 24, 7),
    ]
    # --- receiver-level health (both stream and webhook receivers), moved
    # from tabs/events.py.
    #
    # Consolidation (#526 decision 7, second pass): "Receiver in-flight" (short, a count)
    # and "Receiver latency p50/p95/p99 (stream)" (s) merge into one panel via a right-axis
    # override — same subject (receiver health), different unit, no metric lost.
    receiver = [
        (panel("Receiver in-flight & latency (stream)", "timeseries",
               [prom_t("tailscale_stream_inflight", legend="in-flight stream"),
                prom_t("tailscale_webhook_inflight", legend="in-flight webhook", refid="B"),
                prom_t(hq("0.5", "tailscale_stream_request_duration_seconds"), legend="p50 latency", refid="C"),
                prom_t(hq("0.95", "tailscale_stream_request_duration_seconds"), legend="p95 latency", refid="D"),
                prom_t(hq("0.99", "tailscale_stream_request_duration_seconds"), legend="p99 latency", refid="E")],
               unit="short", custom=ts_custom(), options=ts_opts(placement="right"),
               overrides=[{"matcher": {"id": "byRegexp", "options": "p(50|95|99) latency"},
                           "properties": [{"id": "unit", "value": "s"}, {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="Requests currently being handled by the stream/webhook receivers (left "
                    "axis) — a sustained non-zero value means the receiver is backed up. "
                    "Stream (HEC) receiver request-handling latency quantiles ride the right "
                    "axis (s); webhook has no equivalent latency histogram."), 12, 7),
        (panel("Receiver rejected/s (stream + webhook)", "timeseries",
               [prom_t("sum by (reason) (rate(tailscale_stream_rejected_total[%s]))" % RI, legend="stream {{reason}}"),
                prom_t("sum by (reason) (rate(tailscale_webhook_rejected_total[%s]))" % RI, legend="webhook {{reason}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Requests rejected by either receiver, by reason — combines stream and "
                    "webhook onto one axis."), 8, 7),
    ]
    # --- receiver-loss detail: stream skip and webhook duration/drift, moved
    # from tabs/diagnostics.py #405 (its duplicate combined stream+webhook
    # "rejections by reason" panel is dropped here — the receiver row above
    # already covers that combination without the redundancy).
    recvloss = [
        (panel("Stream records accepted vs skipped/s", "timeseries",
               [prom_t("sum(rate(tailscale_stream_records_total[%s]))" % RI, legend="accepted/s"),
                prom_t("sum by (reason) (rate(tailscale_stream_skipped_total[%s]))" % RI, legend="skipped {{reason}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_STREAM_EMPTY,
               desc="Skipped records were extracted from an otherwise-valid body and then never "
                    "routed to a processor: `unclassified` matched neither the flow nor the "
                    "audit shape, `unwrap_drop` was a non-object HEC event dropped while "
                    "unwrapping. Read against the accepted rate."), 12, 7),
        (panel("Webhook request duration p50/p95/p99", "timeseries",
               [prom_t(hq("0.5", "tailscale_webhook_request_duration_seconds"), legend="p50"),
                prom_t(hq("0.95", "tailscale_webhook_request_duration_seconds"), legend="p95", refid="B"),
                prom_t(hq("0.99", "tailscale_webhook_request_duration_seconds"), legend="p99", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(), novalue=_WEBHOOK_EMPTY,
               desc="Wall-clock handling time for webhook deliveries. Tailscale retries a "
                    "delivery it considers timed out, so a rising tail here turns into "
                    "duplicate events rather than lost ones."), 12, 7),
        (panel("Webhook accepted vs duplicates & schema drift/s", "timeseries",
               [prom_t("sum(rate(tailscale_webhook_events_total[%s]))" % RI, legend="accepted/s"),
                prom_t("sum(rate(tailscale_webhook_duplicates_total[%s]))" % RI, legend="duplicates suppressed/s", refid="B"),
                prom_t("sum by (field, status) (rate(tailscale_webhook_schema_drift_total[%s]))" % RI,
                       legend="drift {{field}} {{status}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_WEBHOOK_EMPTY,
               desc="Accepted volume alongside the redeliveries the dedup failsafe suppressed, "
                    "and webhook payload schema observations by field. A field moving to an "
                    "unknown status means Tailscale changed the payload shape: the receiver "
                    "keeps accepting the event, so nothing else here goes red, but whatever the "
                    "drifted field feeds is quietly no longer populated."), 24, 7),
    ]
    # --- ingestion volume across every path, moved from tabs/events.py.
    #
    # Consolidation (#526 decision 7, second pass): "Ingest decoded bytes/s by source" (Bps)
    # merges into the records/rejections panel (cps) via a right-axis override — same
    # subject, different unit, no metric lost.
    ingestvol = [
        (panel("Ingest records/s & rejections/s & bytes/s by source", "timeseries",
               [prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_records_total[%s]))" % RI, legend="accepted {{source}}/{{signal}}"),
                prom_t('sum by (source) ('
                       'label_replace(rate(tailscale_stream_rejected_total[%(w)s]), "source", "stream", "", "") or '
                       'label_replace(rate(tailscale_webhook_rejected_total[%(w)s]), "source", "webhook", "", "") or '
                       'label_replace(rate(tailscale2otel_objectstore_skipped_total[%(w)s]), "source", "objectstore", "", "")'
                       ')' % {"w": RI}, legend="rejected {{source}}", refid="B"),
                prom_t("sum by (source) (rate(tailscale2otel_ingest_size_bytes_total[%s]))" % RI,
                       legend="bytes {{source}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               overrides=[{"matcher": {"id": "byRegexp", "options": "bytes .+"},
                           "properties": [{"id": "unit", "value": "Bps"}]}],
               desc="Records accepted across every ingestion path, by source and signal, beside "
                    "rejections and skips from every path on the same axis (left, cps). A source "
                    "missing from the rejected series is a source that has never rejected "
                    "anything, not one failing silently — the receivers are independently "
                    "optional. Object-store contributes its skipped-object count, which includes "
                    "per-cycle budget skips as well as faults. Decoded payload bytes accepted, "
                    "by source, ride the right axis (Bps)."), 24, 7),
    ]
    # --- accepted-data freshness/staleness, moved from tabs/events.py.
    #
    # Consolidation (#526 decision 7, second pass): "Timestamp skew/s" (cps) folds into the
    # freshness panel (s) as a fourth right-axis series — same subject, different unit, no
    # metric lost.
    ingestfresh = [
        (panel("Accepted event freshness & age p95, timestamp skew/s", "timeseries",
               [prom_t("clamp_min(time() - max by (source, signal) "
                       "(last_over_time(tailscale2otel_ingest_last_event_timestamp_seconds[30d])), 0)",
                       legend="freshness {{source}}/{{signal}}"),
                prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_event_age_seconds_bucket[%s])))" % RI,
                       legend="p95 age {{source}}/{{signal}}", refid="B"),
                prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_capture_delay_seconds_bucket[%s])))" % RI,
                       legend="p95 capture delay {{source}}/{{signal}}", refid="C"),
                prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_timestamp_skew_total[%s]))" % RI,
                       legend="skew {{source}}/{{signal}}", refid="D")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue="0",
               overrides=[{"matcher": {"id": "byRegexp", "options": "skew .+"},
                           "properties": [{"id": "unit", "value": "cps"}]}],
               desc="Seconds since the greatest event timestamp accepted from each source/signal "
                    "(exposes stale-but-still-running ingestion, unlike receiver liveness alone); "
                    "the p95 age at acceptance (backfills and retries raise this without moving "
                    "the last-event timestamp backwards); and p95 upstream capture/observation "
                    "delay where the wire format supplies a capture timestamp separately from "
                    "event time. Right axis (cps): events whose timestamp is later than local "
                    "acceptance, or whose capture timestamp precedes event time — negative "
                    "derived durations are clamped to zero."), 24, 7),
    ]
    # --- cross-source dedup, moved from tabs/events.py "Dedup effectiveness". Unchanged by
    # #526's second pass: the dissolved tabs/health_internals.py "Cardinality & dedup" row
    # (task 1 of this pass) went to cardinality.py's Cost & Cardinality tab, not here — its
    # two dedup panels (dedup_size_ratio / dedup_evictions_total) were byte-identical
    # duplicates of a row already on THAT tab, so nothing moved. tailscale2otel_dedup_hits_
    # total already had its only panel right here, in "Dedup hits & evictions/s" below,
    # before this pass touched anything.
    dedup = [
        (panel("Dedup hits & evictions/s", "timeseries",
               [prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_hits_total[%s]))" % RI, legend="hits {{dedup_set}}"),
                prom_t("sum by (dedup_set) (rate(tailscale2otel_dedup_evictions_total[%s]))" % RI, legend="evictions {{dedup_set}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Duplicate records suppressed by the cross-source dedup failsafe, per set, "
                    "beside evictions from capacity pressure. Steady-state evictions are normal: "
                    "dedup keys are effectively unique, so a full set evicts one key per insert "
                    "forever even when healthy — only evictions approaching a set's capacity "
                    "within one poll interval indicate real overlap loss."), 12, 7),
        (panel("Dedup set fill & eviction age", "timeseries",
               [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="size {{dedup_set}}"),
                prom_t("max by (dedup_set) (tailscale2otel_dedup_youngest_eviction_age_seconds)",
                       legend="youngest eviction {{dedup_set}}", refid="B"),
                prom_t("max by (dedup_set) (tailscale2otel_dedup_overlap_horizon_seconds)",
                       legend="overlap horizon {{dedup_set}}", refid="C")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               overrides=[{"matcher": {"id": "byRegexp", "options": "(youngest eviction|overlap horizon) .+"},
                           "properties": [{"id": "unit", "value": "s"},
                                          {"id": "custom.axisPlacement", "value": "right"}]}],
               novalue=_DEDUP_AGE_EMPTY,
               desc="Keys currently held in each bounded dedup set (left axis, a count despite "
                    "the metric's _ratio suffix), plus the smallest residency age observed at "
                    "capacity eviction and the configured overlap horizon (right axis, seconds). "
                    "An age below its horizon means the set is too small; age is absent until an eviction."), 12, 7),
    ]
    # --- NEW: processor queue (batch processor between accept and OTLP export
    # for logs/traces). Two of the five #526 pending-panel signals scheduled
    # for this tab: tailscale2otel.processor.queue.size/.capacity, .dropped.
    queue = [
        (panel("Processor queue fill by signal", "bargauge",
               [prom_t("sum by (signal) (last_over_time(tailscale2otel_processor_queue_size_ratio[%s])) / "
                       "clamp_min(sum by (signal) (last_over_time(tailscale2otel_processor_queue_capacity_ratio[%s])), 1)"
                       % (WIN_FAST, WIN_FAST), legend="{{signal}}")],
               unit="percentunit", min_=0, max_=1,
               thresholds=thr([(None, "green"), (0.8, "yellow"), (1, "red")]),
               options=bargauge_opts(), novalue=_QUEUE_EMPTY,
               desc="This app's own log/trace batch-processor queue (buffered between "
                    "acceptance and OTLP export), current size as a fraction of its configured "
                    "capacity, by signal. Near or at 1 means the queue is saturated and new "
                    "records will start being dropped rather than buffered."), 12, 6),
        (panel("Processor queue drops/s by signal & reason", "timeseries",
               [prom_t("sum by (signal, reason) (rate(tailscale2otel_processor_dropped_total[%s]))" % RI,
                       legend="{{signal}} {{reason}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Records/spans dropped because the queue above was full when offered, by "
                    "signal and reason. Any non-zero rate means the fill fraction beside it was "
                    "already at or near 1 — read the two together, the drop counter is the loss "
                    "the fill gauge warned about."), 12, 6),
    ]
    # --- NEW: log truncation (body/attribute bounding before export). The
    # remaining two of the five #526 pending-panel signals scheduled for this
    # tab: tailscale2otel.log.record.truncated, tailscale2otel.log.truncated.bytes.
    #
    # Consolidation (#526 decision 7, second pass): the two panels merge into one via a
    # right-axis override — same field breakdown, different unit, no metric lost.
    truncation = [
        (panel("Log records & bytes truncated/s by field", "timeseries",
               [prom_t("sum by (field) (rate(tailscale2otel_log_record_truncated_total[%s]))" % RI,
                       legend="records {{field}}"),
                prom_t("sum by (field) (rate(tailscale2otel_log_truncated_bytes_total[%s]))" % RI,
                       legend="bytes {{field}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue="0",
               overrides=[{"matcher": {"id": "byRegexp", "options": "bytes .+"},
                           "properties": [{"id": "unit", "value": "Bps"}]}],
               desc="Log records whose body or an attribute value was truncated to a bounded "
                    "length before export, by field (left axis, cps). Non-zero is expected on "
                    "a very verbose source; a sustained climb on a field that was previously "
                    "flat means an upstream payload shape got bigger, not that the exporter "
                    "regressed. Bytes dropped by truncation, same field breakdown, right axis "
                    "(Bps) — read together: a rising record count with flat bytes means many "
                    "small truncations; flat count with rising bytes means the same records "
                    "are losing more each time."), 24, 6),
    ]

    # --- #526 decision 9 (final piece): moved from tabs/events.py, verbatim query logic.
    # Ungated on purpose — the row's whole job is to tell "no data" apart from "no
    # collector", and a presence gate would hide the answer in exactly the case an operator
    # opened the tab to diagnose (see STATE_KEY above). "Audit events by ingestion path" is
    # dropped here — see the module docstring's consolidation note; it is not one of the
    # four state discriminators.
    auditstate = [
        (panel("Audit poll collector", "stat",
               [prom_t("min(%s)" % lot(sel("tailscale2otel_scrape_success_ratio",
                                           'tailscale_collector="auditlogs"'), WIN_FAST))],
               mappings=BOOL_HEALTHY_ON, thresholds=thr([(None, "red"), (1, "green")]),
               options=stat_opts(color="background"),
               # No zero-fill. A manufactured 0 would render "the audit poller is
               # broken" on a tailnet that streams its audit log instead (#385).
               novalue="No auditlogs scrape reported. Prerequisites: collectors.auditlogs.enabled "
                       "with collectors.auditlogs.source of poll or both. A stream, webhook or "
                       "objectstore path reports no scrape here — read the ingestion-path panel. "
                       "See " + CFG_DOC,
               desc="Last poll-path scrape result for the auditlogs collector." + STATE_KEY), 5, 6),
        (panel("Audit events accepted (this range)", "stat",
               [prom_t("sum(increase(%s[$__range]))" % sel("tailscale_config_audit_events_total"),
                       instant=True)],
               unit="short", options=stat_opts(color="value", graph="none"),
               desc="Audit events the exporter accepted over the dashboard time range, from every "
                    "ingestion path. Reads exactly $__range, the same window as its Loki "
                    "counterpart — the two disagreeing on window is the classic false "
                    "\"the data is missing\" report." + STATE_KEY), 5, 6),
        (panel("Audit log lines in Loki (this range)", "stat",
               [loki_t("sum(count_over_time(%s | event_name=`tailscale.config.audit` [$__range]))"
                       % LOKI_TN, instant=True)],
               unit="short", options=stat_opts(color="value", graph="none"), novalue="0",
               desc="Audit log records present in the queried Loki datasource over the same "
                    "$__range window. Materially below the accepted counter means the records "
                    "were accepted but did not land where this dashboard reads them." + STATE_KEY), 5, 6),
        (panel("Audit ingestion failures/s", "timeseries",
               [prom_t("sum by (error_type) (rate(%s[%s]))"
                       % (sel("tailscale2otel_scrape_errors_total",
                              'tailscale_collector="auditlogs"'), RI),
                       legend="poll {{error_type}}", refid="A"),
                prom_t("sum by (reason) (rate(%s[%s]))"
                       % (sel("tailscale_stream_rejected_total"), RI),
                       legend="stream {{reason}}", refid="B"),
                prom_t("sum by (reason) (rate(%s[%s]))"
                       % (sel("tailscale_webhook_rejected_total"), RI),
                       legend="webhook {{reason}}", refid="C")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Records the pipeline attempted and lost, by path. Any non-zero series here "
                    "rules out the idle reading: something arrived and did not make it "
                    "through." + STATE_KEY), 9, 6),
    ]
    # --- #526 decision 9 (final piece), moved from tabs/events.py verbatim. Two distinct
    # delays: how long Tailscale itself deferred the record before logging it, and how long
    # it then took to reach local acceptance. A backlog in the first is upstream and nothing
    # here can shorten it.
    auditlatency = [
        (panel("Tailscale deferred delay (p50/p95/p99)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_config_audit_deferred_delay_seconds"), legend="p50"),
                prom_t(q_hist("0.95", "tailscale_config_audit_deferred_delay_seconds"), legend="p95"),
                prom_t(q_hist("0.99", "tailscale_config_audit_deferred_delay_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Time Tailscale deferred a configuration-audit record before logging it. "
                    "Emitted only when the record carries both eventTime and deferredAt in "
                    "order, so a quiet panel is not the same as a zero delay."), 12, 7),
        (panel("Local processing delay (p50/p95/p99)", "timeseries",
               [prom_t(q_hist("0.5", "tailscale_config_audit_processing_delay_seconds"), legend="p50"),
                prom_t(q_hist("0.95", "tailscale_config_audit_processing_delay_seconds"), legend="p95"),
                prom_t(q_hist("0.99", "tailscale_config_audit_processing_delay_seconds"), legend="p99")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Time from the record's deferredAt (or eventTime when it was not deferred) to "
                    "local processor acceptance. Rising here with a flat deferred delay points at "
                    "the poll window or the exporter, not at upstream."), 12, 7),
    ]
    auditdrift = [
        (panel("Audit schema drift", "timeseries",
               [prom_t("sum by (field, status) "
                       "(rate(tailscale_config_audit_schema_drift_total[%s]))" % RI,
                       legend="{{field}} / {{status}}")],
               unit="cps", custom=ts_custom(stack="normal"), options=ts_opts(),
               desc="Bounded known/unknown observations for audit action, origin, actor type, "
                    "and target property. Raw unknown values never enter metric labels."), 24, 7),
    ]

    return [
        row("Object-store ingestion status", objstore_status),
        row("Object-store ingestion throughput & faults", objstore_throughput),
        row("Ingress WAL", wal),
        row("Per-entity subrequest fan-out", subreq),
        row("Stream ingestion", stream, present="has_stream"),
        row("Webhook ingestion", webhook, present="has_webhook"),
        row("Receiver health", receiver, present="has_recv_dur"),
        row("Receiver loss detail", recvloss),
        row("Ingestion volume", ingestvol, present="has_ingest"),
        row("Accepted-data freshness", ingestfresh, present="has_ingest"),
        row("Cross-source dedup", dedup, present="has_selfobs"),
        row("Processor queue", queue),
        row("Log truncation", truncation),
        row("Audit pipeline state", auditstate),
        row("Audit pipeline latency", auditlatency, present="has_audit"),
        row("Audit schema drift", auditdrift, present="has_audit"),
    ]
