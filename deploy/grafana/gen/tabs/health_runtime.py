"""tab_health_runtime() — the "Runtime" tab on the tailscale2otel-health dashboard (#526).

The process itself: Go runtime/GC, plus the two opt-in subsystems that live alongside it
(the Pyroscope profile-upload agent and TLS certificate reload on the admin/metrics/stream
listeners). The Go-runtime rows are copied from tabs/diagnostics.py's "Go runtime" /
"Liveness & build" / "App health" material (that module is read-only for this lane) rather
than shared, since #526 is splitting the monolithic diagnostics tab apart.

Profiling and TLS are both opt-in features with no existing presence sentinel registered in
builder._SENTINEL_ORDER (the only two catalog gaps named "alertable-only" by #526's ledger
are the TLS pair). Adding a new sentinel name is out of scope for this lane — builder.py is
read-only here — so both rows are UNGATED and rely entirely on each panel's own `novalue=`
empty state, the same pattern tabs/diagnostics.py already uses for the object-store/WAL/
subrequest families (#399/#386).
"""

from builder import (hq, panel, prom_t, pyroscope_t, RI, row, stat_opts, ts_custom,
                     ts_opts, WIN_FAST)

_PROFILING_EMPTY = ("No profile-upload series. Requires profiling.pyroscope.server_address "
                    "to be set (the opt-in Pyroscope push agent).")
_TLS_EMPTY = ("No TLS certificate series. Requires TLS to be configured on at least one "
              "listener (admin, metrics, or stream).")


def tab_health_runtime(scope):
    del scope  # no sentinel declared by this tab (see module docstring)

    goruntime = [
        (panel("Goroutines", "stat", [prom_t("max(tailscale2otel_runtime_goroutines_ratio)")],
               unit="short", options=stat_opts(graph="area"),
               desc="Number of live goroutines (a count, despite the _ratio suffix)."), 6, 5),
        (panel("GOMAXPROCS", "stat", [prom_t("max(tailscale2otel_runtime_gomaxprocs_ratio)")],
               unit="short", options=stat_opts(),
               desc="Current GOMAXPROCS, the max OS threads executing Go code (a count, "
                    "despite the _ratio suffix)."), 6, 5),
        (panel("Process uptime", "stat", [prom_t("max(process_uptime_seconds)")],
               unit="s", options=stat_opts(),
               desc="Seconds since the process started (wall-clock uptime)."), 6, 5),
        (panel("Process CPU (user/system)", "timeseries",
               [prom_t("sum by (cpu_mode) (rate(process_cpu_time_seconds_total[%s]))" % RI, legend="{{cpu_mode}}")],
               unit="percentunit", custom=ts_custom(), options=ts_opts(),
               desc="Cumulative process CPU time (getrusage RUSAGE_SELF), by mode, as a "
                    "rate (~cores). Emitted on unix platforms only."), 6, 5),
    ]
    gcmem = [
        (panel("Memory breakdown", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap in-use"),
                prom_t("max(tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_heap_inuse_bytes)", legend="heap idle"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack in-use"),
                prom_t("max(tailscale2otel_runtime_memory_sys_bytes - tailscale2otel_runtime_memory_heap_sys_bytes - tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="other (non-heap)")],
               unit="bytes", custom=ts_custom(stack="normal", fill=25), options=ts_opts(placement="right"),
               desc="Go memory obtained from the OS (runtime.memory.sys), stacked into in-use "
                    "heap, idle/reserved heap, stacks, and other non-heap runtime (GC, "
                    "mspan/mcache). Total height = total sys."), 12, 7),
        (panel("Goroutines & stack", "timeseries",
               [prom_t("max(tailscale2otel_runtime_goroutines_ratio)", legend="goroutines"),
                prom_t("max(tailscale2otel_runtime_memory_stack_inuse_bytes)", legend="stack inuse")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               overrides=[{"matcher": {"id": "byName", "options": "stack inuse"},
                           "properties": [{"id": "unit", "value": "bytes"}, {"id": "custom.axisPlacement", "value": "right"}]}],
               desc="Goroutine count alongside stack memory, over time (the stat panel above "
                    "shows only the current value)."), 12, 7),
        (panel("GC cycles/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_count_total[%s]))" % RI, legend="gc/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(),
               desc="Completed garbage-collection cycles per second."), 8, 6),
        (panel("GC pause/s", "timeseries", [prom_t("sum(rate(tailscale2otel_runtime_gc_pause_time_seconds_total[%s]))" % RI, legend="pause s/s")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Cumulative stop-the-world GC pause time, as seconds of pause per second "
                    "of wall clock."), 8, 6),
        (panel("GC CPU fraction", "timeseries", [prom_t("max(tailscale2otel_runtime_gc_cpu_fraction_ratio)", legend="gc cpu")],
               unit="percentunit", custom=ts_custom(), options=ts_opts(),
               desc="Fraction of total CPU time used by the garbage collector since process "
                    "start (0..1)."), 8, 6),
        (panel("GC next-target vs live heap", "timeseries",
               [prom_t("max(tailscale2otel_runtime_gc_next_target_bytes)", legend="next GC target"),
                prom_t("max(tailscale2otel_runtime_memory_heap_alloc_bytes)", legend="live heap")],
               unit="bytes", custom=ts_custom(), options=ts_opts(),
               desc="Live heap vs the heap size that triggers the next GC; the gap is GC "
                    "headroom."), 8, 6),
        (panel("Heap alloc churn", "timeseries",
               [prom_t("sum(rate(tailscale2otel_runtime_memory_alloc_bytes_total[%s]))" % RI, legend="alloc/s")],
               unit="Bps", custom=ts_custom(), options=ts_opts(),
               desc="Cumulative heap-allocation rate (includes freed); allocation churn / GC "
                    "pressure."), 8, 6),
        (panel("Live heap objects", "timeseries",
               [prom_t("max(tailscale2otel_runtime_memory_heap_objects_ratio)", legend="objects")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Number of live heap objects (a count, despite the _ratio suffix)."), 8, 6),
    ]
    # --- Profiling upload (Pyroscope push agent, opt-in). No presence sentinel exists for
    # this feature (see module docstring) — every panel relies on its own empty state.
    _upldur_p = panel(
        "Profile upload duration p50/p95/p99", "timeseries",
        [prom_t(hq("0.5", "tailscale2otel_profiling_upload_duration_seconds"), legend="p50"),
         prom_t(hq("0.95", "tailscale2otel_profiling_upload_duration_seconds"), legend="p95", refid="B"),
         prom_t(hq("0.99", "tailscale2otel_profiling_upload_duration_seconds"), legend="p99", refid="C")],
        unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_PROFILING_EMPTY,
        desc="Wall-clock seconds per profile upload attempt, including failed ones. Rising "
             "latency here is the early warning for the upload timeout that follows.")
    profiling = [
        (panel("Profile upload attempts/s", "timeseries",
               [prom_t("sum(rate(tailscale2otel_profiling_upload_attempts_total[%s]))" % RI, legend="attempts/s")],
               unit="cps", custom=ts_custom(), options=ts_opts(), novalue=_PROFILING_EMPTY,
               desc="Profile upload attempts to Pyroscope, successful or not (one per profile "
                    "type per profiling.pyroscope.upload_rate period). A flat line with the "
                    "agent enabled means it is not uploading at all — a different fault from "
                    "uploads being rejected."), 8, 6),
        (panel("Profile upload failures/s by type", "timeseries",
               [prom_t("sum by (error_type) (rate(tailscale2otel_profiling_upload_failures_total[%s])) "
                       "or (0 * sum(rate(tailscale2otel_profiling_upload_attempts_total[%s])))"
                       % (RI, RI), legend="{{error_type}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_PROFILING_EMPTY,
               desc="Profile upload attempts that failed, by bounded error type (timeout, "
                    "canceled, unauthenticated, rate_limited, unavailable, tls, invalid, "
                    "other) — never the server's response body."), 8, 6),
        (_upldur_p, 8, 6),
        (panel("Profile upload consecutive failures", "stat",
               [prom_t("max(tailscale2otel_profiling_upload_consecutive_failures_ratio)")],
               unit="short", options=stat_opts(color="value"), novalue=_PROFILING_EMPTY,
               desc="Current unbroken profile-upload failure streak, reset to 0 by any success "
                    "(a count, despite the _ratio suffix). Distinguishes a blip from a "
                    "sustained outage without needing a rate window."), 6, 6),
        (panel("Profile upload last-success age", "stat",
               [prom_t("time() - max(tailscale2otel_profiling_upload_last_success_seconds > 0)")],
               unit="s", options=stat_opts(), novalue=_PROFILING_EMPTY,
               desc="Seconds since the most recent SUCCESSFUL profile upload; absent until the "
                    "first success (last_success is 0 until then, filtered out rather than "
                    "charted as a huge age). The attempts counter keeps climbing during an "
                    "outage, so this is what actually catches profiles silently stopping."), 6, 6),
    ]
    profiles = [
        (panel("CPU profile activity", "timeseries",
               [pyroscope_t("process_cpu:cpu:nanoseconds:cpu:nanoseconds")],
               unit="ns", custom=ts_custom(fill=20), options=ts_opts(),
               novalue="No CPU profiles in this range. The profiler may be disabled, idle, or "
                       "pointing at another Profiles backend; use the upload-health row above "
                       "to distinguish those states.",
               desc="CPU nanoseconds attributed per step by the continuous Pyroscope profile."), 12, 7),
        (panel("In-use heap profile activity", "timeseries",
               [pyroscope_t("memory:inuse_space:bytes:space:bytes")],
               unit="bytes", custom=ts_custom(fill=20), options=ts_opts(),
               novalue="No in-use heap profiles in this range. Check upload health above.",
               desc="Live heap represented by the in-use-space profile."), 12, 7),
        (panel("CPU flame graph", "flamegraph",
               [pyroscope_t("process_cpu:cpu:nanoseconds:cpu:nanoseconds",
                            query_type="profile", max_nodes=8192)],
               novalue="No CPU profile samples in this range.",
               desc="Bounded merged CPU flame graph for tailscale2otel over the selected range. "
                    "Widen the range when the process is idle."), 12, 9),
        (panel("In-use heap flame graph", "flamegraph",
               [pyroscope_t("memory:inuse_space:bytes:space:bytes",
                            query_type="profile", max_nodes=8192)],
               novalue="No in-use heap profile samples in this range.",
               desc="Bounded flame graph of call stacks retaining live heap."), 12, 9),
    ]
    # --- TLS certificate reload (admin/metrics/stream listeners, opt-in). No presence
    # sentinel exists for this feature either. tailscale2otel.tls.cert.not_after and
    # tailscale2otel.tls.cert.reload.failures are the two ALERTABLE-ONLY signals #526 set
    # out to fix — "TLS certificate expiry countdown" and "TLS certificate reload
    # failures" are the panel titles an alert rule should link to.
    tls = [
        (panel("TLS certificate expiry countdown", "timeseries",
               [prom_t("min by (component) (tailscale2otel_tls_cert_not_after_seconds - time())", legend="{{component}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_TLS_EMPTY,
               desc="Seconds until the active certificate's notAfter (expiry), by listener "
                    "component (admin|metrics|stream). Falling toward zero is what "
                    "'TLS certificate expiring' pages on."), 8, 6),
        (panel("TLS certificate reload failures", "timeseries",
               [prom_t("sum by (component) (rate(tailscale2otel_tls_cert_reload_failures_total[%s]))" % RI, legend="{{component}}")],
               unit="cps", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_TLS_EMPTY,
               desc="Certificate reload attempts that failed to produce a valid keypair, by "
                    "listener component. The listener keeps serving the last known-good "
                    "certificate; a non-zero rate is a rotation going wrong, and is what "
                    "'TLS certificate reload failing' pages on."), 8, 6),
        (panel("TLS certificate age since issuance", "timeseries",
               [prom_t("time() - min by (component) (tailscale2otel_tls_cert_not_before_seconds)", legend="{{component}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_TLS_EMPTY,
               desc="Seconds since the active certificate's notBefore, by listener component. "
                    "Updates on every successful reload, so a value that stops growing at a "
                    "reload boundary and then jumps means a rotation landed."), 8, 6),
        (panel("TLS certificate reload freshness", "timeseries",
               [prom_t("time() - max by (component) (tailscale2otel_tls_cert_reload_last_success_seconds > 0)", legend="{{component}}")],
               unit="s", custom=ts_custom(), options=ts_opts(placement="right"), novalue=_TLS_EMPTY,
               desc="Seconds since the most recent successful certificate reload, by listener "
                    "component; absent until the first success. A rotated file on disk is "
                    "picked up on the next handshake at least this recently."), 8, 6),
    ]
    return [row("Go runtime", goruntime), row("GC & memory", gcmem),
            row("Profiling upload", profiling), row("Profiles", profiles),
            row("TLS certificate", tls)]
