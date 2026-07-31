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

Consolidation (#526 decision 7): several near-duplicate single-purpose panels
from the two source tabs are merged into one multi-series panel where the
merged queries share a unit (so no dual-axis override is needed) — e.g. object
listing loss (skipped/retried/limit-stopped), webhook accepted-vs-rejected, or
ingest records-vs-rejections by source. No signal loses its last panel: every
metric charted by a source row is still charted here, just alongside its
nearest neighbour instead of alone in its own row.

`has_selfobs` gates the Dedup row but is NOT declared here — it is a
DASHBOARD-scope sentinel (consumed by 3+ health tabs) declared by whichever
module now owns the former `tabs/diagnostics.py` "App health" row; declaring
it again here at tab scope would collide with that dashboard-scope
declaration (builder._claim_sentinel's cross-scope check).
"""

from builder import (bargauge_opts, hq, lot, organize, panel, prom_t, RI, row,
                     sentinel, stat_opts, TBL_NOISE, thr, ts_custom, ts_opts,
                     vmap, WIN_FAST, WIN_SLOW)
from maps import bool_map

# Empty-state copy, carried over verbatim from tabs/diagnostics.py (#526 — this
# tab does not edit that file, but the prerequisite wording is still accurate
# for the panels moved here).
_OBJ_EMPTY = ("No object-store ingestion series. Requires collectors.flowlogs.objectstore "
              "(or collectors.auditlogs.objectstore) with the matching source: objectstore, "
              "and self_observability.enabled.")
_WAL_EMPTY = ("No ingress-WAL series. Requires ingress_wal.enabled together with a receiver "
              "that accepts payloads (streaming or webhook), and self_observability.enabled.")
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
    objstore_throughput = [
        (panel("Objects & records ingested/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_objects_total[%s]))" % RI, legend="objects/s"),
                prom_t("sum(rate(tailscale2otel_objectstore_records_total[%s]))" % RI, legend="records/s", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="Objects fully ingested and flow-log records decoded out of them."), 8, 7),
        (panel("Object bytes read vs decompressed", "timeseries",
               [prom_t("sum(rate(tailscale2otel_objectstore_bytes_total[%s]))" % RI, legend="compressed read"),
                prom_t("sum(rate(tailscale2otel_objectstore_decompressed_bytes_total[%s]))" % RI, legend="decompressed", refid="B")],
               unit="Bps", custom=ts_custom(), options=ts_opts(), novalue=_OBJ_EMPTY,
               desc="Transfer cost (compressed bytes actually read) against expansion "
                    "(decompressed bytes consumed)."), 8, 7),
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
        (panel("Object-store provider requests/s", "timeseries",
               [prom_t("sum by (operation, outcome) (rate(tailscale2otel_objectstore_requests_total[%s]))" % RI,
                       legend="{{operation}} {{outcome}}")],
               unit="reqps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="TRANSPORT health only: `error` means the LIST or GET call itself returned "
                    "an error, never a decode/validation/framing/limit failure (those are the "
                    "ingestion-loss panel above). A body that fails mid-read counts as a "
                    "SUCCESSFUL get here."), 12, 6),
        (panel("Object-store provider latency p50/p95/p99", "timeseries",
               [prom_t(hq("0.5", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p50 {{operation}}"),
                prom_t(hq("0.95", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p95 {{operation}}", refid="B"),
                prom_t(hq("0.99", "tailscale2otel_objectstore_request_duration_seconds", by="operation"), legend="p99 {{operation}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_OBJ_EMPTY,
               desc="Times the provider call itself — for `get` that is obtaining the object's "
                    "reader, not streaming/decompressing/decoding its body."), 12, 6),
    ]
    # --- durable ingress WAL (receiver acceptance before processing), moved
    # from tabs/diagnostics.py #386.
    wal = [
        (panel("Ingress WAL pending & retained entries", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_entries_ratio)", legend="pending entries"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_stages_ratio)", legend="retained staging files", refid="B"),
                prom_t("max(tailscale2otel_ingress_wal_completion_markers_ratio)", legend="completion markers", refid="C")],
               unit="short", custom=ts_custom(), options=ts_opts(), novalue=_WAL_EMPTY,
               desc="Durable entries awaiting processor application and flush, staging files "
                    "kept for bounded recovery cleanup, and transient completion markers "
                    "awaiting durable cleanup. Pending climbing toward ingress_wal.max_entries "
                    "fails new requests closed."), 12, 6),
        (panel("Ingress WAL on-disk bytes", "timeseries",
               [prom_t("max(tailscale2otel_ingress_wal_pending_size_bytes)", legend="pending"),
                prom_t("max(tailscale2otel_ingress_wal_orphan_size_bytes)", legend="retained staging", refid="B")],
               unit="bytes", custom=ts_custom(stack="normal"), options=ts_opts(), novalue=_WAL_EMPTY,
               desc="Encoded bytes on the WAL filesystem. There is no TTL or eviction, so this "
                    "is bounded only by ingress_wal.max_bytes."), 12, 6),
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
    receiver = [
        (panel("Receiver in-flight", "timeseries",
               [prom_t("tailscale_stream_inflight", legend="stream"),
                prom_t("tailscale_webhook_inflight", legend="webhook", refid="B")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Requests currently being handled by the stream/webhook receivers — a "
                    "sustained non-zero value means the receiver is backed up."), 8, 7),
        (panel("Receiver latency p50/p95/p99 (stream)", "timeseries",
               [prom_t(hq("0.5", "tailscale_stream_request_duration_seconds"), legend="p50"),
                prom_t(hq("0.95", "tailscale_stream_request_duration_seconds"), legend="p95", refid="B"),
                prom_t(hq("0.99", "tailscale_stream_request_duration_seconds"), legend="p99", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Stream (HEC) receiver request-handling latency quantiles."), 8, 7),
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
    ingestvol = [
        (panel("Ingest records/s & rejections/s by source", "timeseries",
               [prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_records_total[%s]))" % RI, legend="accepted {{source}}/{{signal}}"),
                prom_t('sum by (source) ('
                       'label_replace(rate(tailscale_stream_rejected_total[%(w)s]), "source", "stream", "", "") or '
                       'label_replace(rate(tailscale_webhook_rejected_total[%(w)s]), "source", "webhook", "", "") or '
                       'label_replace(rate(tailscale2otel_objectstore_skipped_total[%(w)s]), "source", "objectstore", "", "")'
                       ')' % {"w": RI}, legend="rejected {{source}}", refid="B")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Records accepted across every ingestion path, by source and signal, beside "
                    "rejections and skips from every path on the same axis. A source missing "
                    "from the rejected series is a source that has never rejected anything, not "
                    "one failing silently — the receivers are independently optional. "
                    "Object-store contributes its skipped-object count, which includes "
                    "per-cycle budget skips as well as faults."), 12, 7),
        (panel("Ingest decoded bytes/s by source", "timeseries",
               [prom_t("sum by (source) (rate(tailscale2otel_ingest_size_bytes_total[%s]))" % RI, legend="{{source}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Decoded payload bytes accepted across every ingestion path, by source."), 12, 7),
    ]
    # --- accepted-data freshness/staleness, moved from tabs/events.py.
    ingestfresh = [
        (panel("Accepted event freshness & age p95", "timeseries",
               [prom_t("clamp_min(time() - max by (source, signal) "
                       "(last_over_time(tailscale2otel_ingest_last_event_timestamp_seconds[30d])), 0)",
                       legend="freshness {{source}}/{{signal}}"),
                prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_event_age_seconds_bucket[%s])))" % RI,
                       legend="p95 age {{source}}/{{signal}}", refid="B"),
                prom_t("histogram_quantile(0.95, sum by (le, source, signal) "
                       "(rate(tailscale2otel_ingest_capture_delay_seconds_bucket[%s])))" % RI,
                       legend="p95 capture delay {{source}}/{{signal}}", refid="C")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"),
               desc="Seconds since the greatest event timestamp accepted from each source/signal "
                    "(exposes stale-but-still-running ingestion, unlike receiver liveness alone); "
                    "the p95 age at acceptance (backfills and retries raise this without moving "
                    "the last-event timestamp backwards); and p95 upstream capture/observation "
                    "delay where the wire format supplies a capture timestamp separately from "
                    "event time."), 24, 7),
        (panel("Timestamp skew/s", "timeseries",
               [prom_t("sum by (source, signal) (rate(tailscale2otel_ingest_timestamp_skew_total[%s]))" % RI,
                       legend="{{source}}/{{signal}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Events whose timestamp is later than local acceptance, or whose capture "
                    "timestamp precedes event time. Negative derived durations are clamped to "
                    "zero."), 24, 6),
    ]
    # --- cross-source dedup, moved from tabs/events.py "Dedup effectiveness".
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
        (panel("Dedup set fill", "timeseries",
               [prom_t("max by (dedup_set) (tailscale2otel_dedup_size_ratio)", legend="{{dedup_set}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Keys currently held in each bounded dedup set (a count, despite the "
                    "metric's _ratio suffix)."), 12, 7),
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
    truncation = [
        (panel("Log records truncated/s by field", "timeseries",
               [prom_t("sum by (field) (rate(tailscale2otel_log_record_truncated_total[%s]))" % RI,
                       legend="{{field}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Log records whose body or an attribute value was truncated to a bounded "
                    "length before export, by field. Non-zero is expected on a very verbose "
                    "source; a sustained climb on a field that was previously flat means an "
                    "upstream payload shape got bigger, not that the exporter regressed."), 12, 6),
        (panel("Log bytes truncated/s by field", "timeseries",
               [prom_t("sum by (field) (rate(tailscale2otel_log_truncated_bytes_total[%s]))" % RI,
                       legend="{{field}}")],
               unit="Bps", custom=ts_custom(), options=ts_opts(), novalue="0",
               desc="Bytes dropped from log record bodies/attribute values by truncation, by "
                    "field — the size of what the panel beside it counts. Read together: a "
                    "rising record count with flat bytes means many small truncations; flat "
                    "count with rising bytes means the same records are losing more each "
                    "time."), 12, 6),
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
    ]
