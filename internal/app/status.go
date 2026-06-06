package app

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/rknightion/tailscale2otel/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/internal/catalog"
	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const rfc3339 = time.RFC3339

// buildStatus assembles the admin status snapshot from live in-process state.
// It is the single source for both the HTML page and the JSON endpoint. All
// reads go through thread-safe accessors (the status tracker, device cache,
// dedup sets, cardinality tracker), so it is safe to call concurrently with the
// running collectors. Secrets are excluded here (see redactedConfigSummary).
func (a *App) buildStatus() statusdata.Status {
	now := time.Now()
	uptime := now.Sub(a.startTime)

	// Catalog (static) joined with live cardinality (self-obs only). cardByName
	// maps an OTEL metric name to its active-series count for the last interval.
	cardSeries := a.card.Snapshot() // nil-safe; nil when self-obs disabled
	cardByName := make(map[string]int, len(cardSeries))
	for _, sc := range cardSeries {
		cardByName[sc.Metric] = sc.Count
	}
	metrics := catalog.Metrics()
	metricByName := make(map[string]metricdoc.Metric, len(metrics))
	for _, m := range metrics {
		metricByName[m.Name] = m
	}

	s := statusdata.Status{
		Service: statusdata.ServiceInfo{
			Name:      serviceName,
			Version:   a.version,
			GoVersion: runtime.Version(),
			Tailnet:   a.cfg.Tailscale.Tailnet,
			StartedAt: a.startTime.UTC().Format(rfc3339),
			UptimeSec: int64(uptime.Seconds()),
			Uptime:    humanDuration(uptime),
			SelfObs:   a.cfg.SelfObservability.Enabled,
		},
		Telemetry: statusdata.TelemetryInfo{
			Protocol:        a.cfg.OTLP.Protocol,
			Endpoint:        a.cfg.OTLP.Endpoint,
			Insecure:        a.cfg.OTLP.TLS.Insecure,
			MetricIntervalS: int64(a.cfg.OTLP.MetricInterval.D().Seconds()),
		},
		Collectors:    a.collectorStatuses(now),
		Cache:         a.cacheInfo(),
		RDNS:          a.rdnsInfo(),
		Dedup:         a.dedupInfo(),
		Devices:       a.deviceRows(),
		NodeDiscovery: a.nodeDiscovery(),
		Cardinality:   cardinalityInfo(a.cfg.SelfObservability.Enabled, cardSeries, metricByName),
		Receivers: statusdata.ReceiversInfo{
			Streaming: a.cfg.Streaming.Enabled,
			Webhook:   a.cfg.Webhook.Enabled,
		},
		Profiling: statusdata.ProfilingInfo{
			PprofEnabled:     a.cfg.Profiling.Pprof.Enabled,
			PyroscopeEnabled: a.cfg.Profiling.Pyroscope.Enabled,
			PyroscopeServer:  a.cfg.Profiling.Pyroscope.ServerAddress,
		},
		Runtime:     runtimeInfo(),
		API:         a.apiInfo(),
		Metrics:     metricRows(metrics, cardByName),
		LogEvents:   logRows(catalog.LogEvents()),
		Config:      a.redactedConfigSummary(),
		GeneratedAt: now.UTC().Format(rfc3339),
	}
	if a.runtimeHist != nil {
		s.Runtime.GoroutinesSeries = a.runtimeHist.goroutines.Values()
		s.Runtime.HeapAllocSeries = a.runtimeHist.heapAlloc.Values()
		s.Runtime.GCRateSeries = a.runtimeHist.gcRate.Values()
		// The cardinality trend is meaningful only with self-obs on (otherwise the
		// total is a flat zero), matching the gating of the cardinality section.
		if a.cfg.SelfObservability.Enabled {
			s.Cardinality.TotalSeries = a.runtimeHist.cardTotal.Values()
		}
	}
	s.Health, s.HealthReasons = deriveHealth(s.Collectors)
	return s
}

func (a *App) collectorStatuses(now time.Time) []statusdata.CollectorStatus {
	runs := a.status.Snapshot()
	hist := a.status.HistorySnapshot()
	entries := a.registry.Entries()
	out := make([]statusdata.CollectorStatus, 0, len(entries))
	for _, e := range entries {
		name := e.Collector.Name()
		cs := statusdata.CollectorStatus{
			Name:        name,
			IntervalSec: int64(e.Interval.Seconds()),
		}
		cs.Description, cs.Metrics = collectorBrief(name)
		if r, ok := runs[name]; ok && r.Runs > 0 {
			cs.HasRun = true
			cs.Runs = r.Runs
			cs.Failures = r.Failures
			cs.LastStartedAt = r.LastStarted.UTC().Format(rfc3339)
			cs.LastFinishedAt = r.LastFinished.UTC().Format(rfc3339)
			cs.LastDurationMs = r.LastDuration.Milliseconds()
			cs.LastSuccess = r.LastSuccess
			cs.LastError = r.LastError
			cs.ConsecutiveFailures = r.ConsecutiveFailures
			cs.SuccessRatePct = successRatePct(r.Runs, r.Failures)
			d := nextRunIn(r.LastFinished, e.Interval, now)
			cs.NextRunInSec = int64(d.Seconds())
			cs.NextRunIn = humanDuration(d)
			cs.Overdue = isOverdue(r.LastFinished, e.Interval, now)
		}
		if h, ok := hist[name]; ok {
			cs.DurationMsSeries = h.DurationMs
			cs.OutcomeSeries = h.Outcomes
		}
		if wc, ok := e.Collector.(collector.WindowCollector); ok && a.store != nil {
			if hwm, has := a.store.Get(name); has {
				lag := now.Sub(hwm)
				cs.Checkpoint = &statusdata.CheckpointStatus{
					HighWaterMark: hwm.UTC().Format(rfc3339),
					LagSec:        int64(lag.Seconds()),
					Lag:           humanDuration(lag),
					// Not advancing: the high-water mark trails "now" by well over
					// the collector's own lag plus a few intervals of slack.
					Stuck: lag > wc.Lag()+checkpointStuckMargin*e.Interval,
				}
			}
		}
		out = append(out, cs)
	}
	return out
}

// checkpointStuckMargin is the number of poll intervals (beyond a window
// collector's own Lag) a checkpoint may trail "now" before it is flagged stuck.
const checkpointStuckMargin = 3

// apiInfo builds the API-health section from the per-endpoint request stats. The
// rate-limit summary is set only once a 429 has been observed.
func (a *App) apiInfo() statusdata.APIInfo {
	snaps := a.apiStats.Snapshot()
	info := statusdata.APIInfo{Endpoints: make([]statusdata.APIEndpoint, 0, len(snaps))}
	var total429 int64
	var last429 time.Time
	for _, s := range snaps {
		ep := statusdata.APIEndpoint{
			Endpoint:         s.Endpoint,
			Requests:         s.Requests,
			Errors:           s.Errors,
			Retries:          s.Retries,
			RateLimited:      s.RateLimited,
			LastStatus:       s.LastStatus,
			LastError:        s.LastErr,
			DurationMsSeries: s.DurMs,
		}
		if !s.LastAt.IsZero() {
			ep.LastAt = s.LastAt.UTC().Format(rfc3339)
		}
		if !s.Last429At.IsZero() {
			ep.Last429At = s.Last429At.UTC().Format(rfc3339)
		}
		info.Endpoints = append(info.Endpoints, ep)
		total429 += s.RateLimited
		if s.Last429At.After(last429) {
			last429 = s.Last429At
		}
	}
	if total429 > 0 {
		rl := &statusdata.APIRateLimit{Count: total429}
		if !last429.IsZero() {
			rl.LastSeen = last429.UTC().Format(rfc3339)
		}
		info.RateLimit = rl
	}
	return info
}

func (a *App) cacheInfo() statusdata.CacheInfo {
	age := a.cache.Age()
	return statusdata.CacheInfo{
		Devices: a.cache.Len(),
		AgeSec:  int64(age.Seconds()),
		Age:     humanDuration(age),
	}
}

// rdnsInfo snapshots the reverse-DNS cache for the status page. It reads Stats()
// directly so the numbers show regardless of whether self-observability (which
// gates the OTEL metrics) is on. Returns a disabled marker when the cache is off.
func (a *App) rdnsInfo() statusdata.RDNSInfo {
	if a.rdnsCache == nil {
		return statusdata.RDNSInfo{Enabled: false}
	}
	s := a.rdnsCache.Stats()
	info := statusdata.RDNSInfo{
		Enabled:        true,
		Size:           s.Size,
		Capacity:       s.Capacity,
		TTL:            humanDuration(s.TTL),
		NegativeTTL:    humanDuration(s.NegativeTTL),
		Hits:           s.Hits,
		Misses:         s.Misses,
		Negatives:      s.Negatives,
		QuerySuccess:   s.QuerySuccess,
		QueryFail:      s.QueryFail,
		EvictedExpired: s.EvictedExpired,
		EvictedPurged:  s.EvictedPurged,
		Overflows:      s.Overflows,
	}
	if total := s.Hits + s.Misses + s.Negatives; total > 0 {
		info.HitRatePct = float64(s.Hits) / float64(total) * 100
	}
	if !s.LastPurge.IsZero() {
		info.LastPurge = s.LastPurge.UTC().Format(rfc3339)
	}
	return info
}

func (a *App) dedupInfo() []statusdata.DedupInfo {
	var out []statusdata.DedupInfo
	add := func(name string, set *dedup.Set) {
		if set == nil {
			return
		}
		out = append(out, statusdata.DedupInfo{Name: name, Len: set.Len(), Capacity: set.Cap()})
	}
	add("flow", a.flowDedup)
	add("audit", a.auditDedup)
	add("webhook_cross", a.webhookDedup)
	return out
}

func (a *App) deviceRows() []statusdata.DeviceRow {
	devs := a.cache.Snapshot()
	out := make([]statusdata.DeviceRow, 0, len(devs))
	for _, d := range devs {
		addrs := make([]string, 0, len(d.Addrs))
		for _, ip := range d.Addrs {
			addrs = append(addrs, ip.String())
		}
		out = append(out, statusdata.DeviceRow{
			Name:      d.Name,
			Hostname:  d.Hostname,
			OS:        d.OS,
			OSVersion: d.OSVersion,
			User:      d.User,
			Addrs:     addrs,
			Tags:      d.Tags,
			External:  d.External,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *App) nodeDiscovery() statusdata.NodeDiscovery {
	if a.nodeMetrics == nil {
		return statusdata.NodeDiscovery{}
	}
	ds := a.nodeMetrics.Snapshot()
	nd := statusdata.NodeDiscovery{
		Enabled: ds.Enabled,
		LastOK:  ds.LastOK,
		Static:  ds.Static,
		Active:  ds.Active,
		Targets: make([]statusdata.NodeTarget, 0, len(ds.Targets)),
	}
	if !ds.LastDiscovery.IsZero() {
		nd.LastDiscovery = ds.LastDiscovery.UTC().Format(rfc3339)
	}
	for _, t := range ds.Targets {
		nd.Targets = append(nd.Targets, statusdata.NodeTarget{Instance: t.Instance, URL: t.URL, Source: t.Source})
	}
	return nd
}

// cardinalityInfo builds the live cardinality section. Available is false when
// self-obs is off or no interval has been reported yet, so the page shows the
// right empty state rather than a misleading zero.
func cardinalityInfo(selfObs bool, series []telemetry.SeriesCount, metricByName map[string]metricdoc.Metric) statusdata.CardinalityInfo {
	if !selfObs || len(series) == 0 {
		return statusdata.CardinalityInfo{Available: false}
	}
	info := statusdata.CardinalityInfo{Available: true, Series: make([]statusdata.SeriesRow, 0, len(series))}
	for _, sc := range series {
		promName := ""
		if m, ok := metricByName[sc.Metric]; ok {
			promName = m.PromName()
		}
		info.Total += sc.Count
		info.Series = append(info.Series, statusdata.SeriesRow{
			Metric:   sc.Metric,
			PromName: promName,
			Count:    sc.Count,
			Capped:   sc.Capped,
		})
	}
	return info
}

func metricRows(metrics []metricdoc.Metric, cardByName map[string]int) []statusdata.MetricRow {
	out := make([]statusdata.MetricRow, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, statusdata.MetricRow{
			Name:        m.Name,
			PromName:    m.PromName(),
			Instrument:  string(m.Instrument),
			Unit:        m.Unit,
			Group:       m.Group,
			Description: m.Description,
			Attributes:  m.PromLabels(),
			Series:      cardByName[m.Name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func logRows(events []metricdoc.LogEvent) []statusdata.LogRow {
	out := make([]statusdata.LogRow, 0, len(events))
	for _, e := range events {
		out = append(out, statusdata.LogRow{
			Name:        e.Name,
			Severity:    e.Severity,
			Group:       e.Group,
			Description: e.Description,
			Attributes:  e.Attributes,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func runtimeInfo() statusdata.RuntimeInfo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return statusdata.RuntimeInfo{
		Goroutines: runtime.NumGoroutine(),
		HeapAllocB: ms.HeapAlloc,
		HeapAlloc:  humanBytes(ms.HeapAlloc),
		NumGC:      ms.NumGC,
		GOMAXPROCS: runtime.GOMAXPROCS(0),
	}
}

// redactedConfigSummary returns a configuration overview that NEVER contains a
// secret value — only "<thing>Set" booleans and OTLP header KEY names.
func (a *App) redactedConfigSummary() statusdata.ConfigSummary {
	c := a.cfg
	cs := statusdata.ConfigSummary{
		LogLevel:          c.LogLevel,
		AuthMethod:        c.Tailscale.Auth.Method,
		CheckpointStore:   c.Checkpoint.Store,
		EnabledCollectors: a.enabledCollectorNames(),
		APIKeySet:         c.Tailscale.Auth.APIKey != "",
		OAuthSecretSet:    c.Tailscale.Auth.OAuth.ClientSecret != "",
		GCloudTokenSet:    c.OTLP.GrafanaCloud.Token != "",
		StreamTokenSet:    c.Streaming.Token != "",
		WebhookSecretSet:  c.Webhook.Secret != "",
		PyroscopeAuthSet:  c.Profiling.Pyroscope.BasicAuthPassword != "",
	}
	if len(c.OTLP.Headers) > 0 {
		keys := make([]string, 0, len(c.OTLP.Headers))
		for k := range c.OTLP.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		cs.OTLPHeaderKeys = keys
	}
	return cs
}

func (a *App) enabledCollectorNames() []string {
	entries := a.registry.Entries()
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Collector.Name())
	}
	return names
}

// humanDuration renders d compactly (e.g. "45s", "5m3s", "3h12m", "2d4h").
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// humanBytes renders b as a binary-prefixed size (e.g. "1.5 MiB").
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
