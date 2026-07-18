package app

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/catalog"
	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

const rfc3339 = time.RFC3339

// buildStatus assembles the admin status snapshot from live in-process state.
// It is the single source for both the HTML page and the JSON endpoint. All
// reads go through thread-safe accessors (the status tracker, device cache,
// dedup sets, cardinality tracker), so it is safe to call concurrently with the
// running collectors. Secrets are excluded here (see redactedConfigSummary).
// primary returns the first tailnet runtime — the sole runtime in single-tailnet
// and Headscale modes, and the streaming/webhook-bound runtime in multi mode.
func (a *App) primary() *tailnetRuntime { return a.runtimes[0] }

// multiTailnet reports whether more than one tailnet is observed (enables
// checkpoint-key namespacing and the per-tailnet status sections).
func (a *App) multiTailnet() bool { return len(a.runtimes) > 1 }

// aggregateCardSnapshot merges the per-tailnet cardinality snapshots into a
// single list keyed by metric name (summing counts, OR-ing the capped flag), for
// the combined top-level cardinality section. The process provider's own series
// are included so process self-obs shows up too.
func (a *App) aggregateCardSnapshot() []telemetry.SeriesCount {
	merged := map[string]telemetry.SeriesCount{}
	add := func(snaps []telemetry.SeriesCount) {
		for _, sc := range snaps {
			cur := merged[sc.Metric]
			cur.Metric = sc.Metric
			cur.Count += sc.Count
			cur.Capped = cur.Capped || sc.Capped
			merged[sc.Metric] = cur
		}
	}
	add(a.procCard.Snapshot())
	for _, rt := range a.runtimes {
		add(rt.card.Snapshot())
	}
	out := make([]telemetry.SeriesCount, 0, len(merged))
	for _, sc := range merged {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func (a *App) buildStatus() statusdata.Status {
	now := time.Now()
	uptime := now.Sub(a.startTime)

	// Catalog (static) joined with live cardinality (self-obs only). cardByName
	// maps an OTEL metric name to its active-series count for the last interval.
	cardSeries := a.aggregateCardSnapshot() // nil-safe; empty when self-obs disabled
	cardByName := make(map[string]int, len(cardSeries))
	for _, sc := range cardSeries {
		cardByName[sc.Metric] = sc.Count
	}
	cardLabels := a.aggregateLabelSnapshot() // nil-safe; empty when self-obs disabled
	var cardPerMetric map[string][]int
	if a.runtimeHist != nil {
		cardPerMetric = a.runtimeHist.perMetricSeries()
	}
	cardThresholds := statusdata.CardinalityThresholds{
		Warning:  a.cfg.Cardinality.WarningThreshold,
		Critical: a.cfg.Cardinality.CriticalThreshold,
	}
	metrics := catalog.Metrics()
	metricByName := make(map[string]metricdoc.Metric, len(metrics))
	for _, m := range metrics {
		metricByName[m.Name] = m
	}

	s := statusdata.Status{
		Provider:     string(a.primary().cp.Kind),
		Capabilities: a.primary().cp.Capabilities(),
		Service: statusdata.ServiceInfo{
			Name:      serviceName,
			Version:   a.version,
			GoVersion: runtime.Version(),
			Tailnet:   a.tailnetSummary(),
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
		Tailnets:      a.tailnetStatuses(now),
		Collectors:    a.collectorStatuses(now),
		Cache:         a.cacheInfo(),
		RDNS:          a.rdnsInfo(),
		Dedup:         a.dedupInfo(),
		Devices:       a.deviceRows(),
		NodeDiscovery: a.nodeDiscovery(),
		Cardinality:   cardinalityInfo(a.cfg.SelfObservability.Enabled, cardSeries, cardLabels, cardPerMetric, cardThresholds, metricByName),
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

// collectorStatuses returns the combined collector list across every tailnet
// runtime (single-tailnet: just that one runtime's collectors).
func (a *App) collectorStatuses(now time.Time) []statusdata.CollectorStatus {
	var out []statusdata.CollectorStatus
	for _, rt := range a.runtimes {
		out = append(out, a.runtimeCollectorStatuses(rt, now)...)
	}
	return out
}

// checkpointKeyFor mirrors the scheduler's tailnet checkpoint namespacing so the
// status page reads the same store keys the scheduler writes.
func (a *App) checkpointKeyFor(rt *tailnetRuntime, name string) string {
	if a.multiTailnet() {
		return rt.name + "/" + name
	}
	return name
}

func (a *App) runtimeCollectorStatuses(rt *tailnetRuntime, now time.Time) []statusdata.CollectorStatus {
	runs := rt.status.Snapshot()
	hist := rt.status.HistorySnapshot()
	entries := rt.registry.Entries()
	out := make([]statusdata.CollectorStatus, 0, len(entries))
	for _, e := range entries {
		name := e.Collector.Name()
		cs := statusdata.CollectorStatus{
			Name:           name,
			IntervalSec:    int64(e.Interval.Seconds()),
			FreshnessState: "none", // overridden below once a success is seen
		}
		// Attribute to the runtime in multi-tailnet mode so the combined list and
		// health reasons disambiguate duplicate collector names (#116).
		if a.multiTailnet() {
			cs.Tailnet = rt.name
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
			if !r.LastSuccessAt.IsZero() {
				cs.LastSuccessAt = r.LastSuccessAt.UTC().Format(rfc3339)
			}
			cs.FreshnessSec, cs.Freshness, cs.FreshnessState = freshnessState(r.LastSuccessAt, now, e.Interval)
		}
		if h, ok := hist[name]; ok {
			cs.DurationMsSeries = h.DurationMs
			cs.OutcomeSeries = h.Outcomes
		}
		if wc, ok := e.Collector.(collector.WindowCollector); ok && a.store != nil {
			if hwm, has := a.store.Get(a.checkpointKeyFor(rt, name)); has {
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

// tailnetSummary is the header tailnet label: the single tailnet's name, or a
// "N tailnets" summary in multi-tailnet mode.
func (a *App) tailnetSummary() string {
	if a.multiTailnet() {
		return fmt.Sprintf("%d tailnets", len(a.runtimes))
	}
	// Use the actual runtime name (covers a single-entry tailnets: list, whose name
	// lives in Tailnets[0], not the unused top-level tailscale.tailnet) — #116.
	if len(a.runtimes) > 0 && a.runtimes[0].name != "" {
		return a.runtimes[0].name
	}
	return a.cfg.Tailscale.Tailnet
}

// tailnetStatuses builds the per-tailnet sections of the status page.
func (a *App) tailnetStatuses(now time.Time) []statusdata.TailnetStatus {
	out := make([]statusdata.TailnetStatus, 0, len(a.runtimes))
	for _, rt := range a.runtimes {
		name := rt.name
		if name == "" {
			name = a.cfg.Tailscale.Tailnet
		}
		cols := a.runtimeCollectorStatuses(rt, now)
		failing := 0
		for _, cs := range cols {
			if cs.HasRun && !cs.LastSuccess {
				failing++
			}
		}
		authMethod := rt.authMethod
		if authMethod == "" {
			authMethod = a.cfg.Tailscale.Auth.Method // headscale / fallback
		}
		out = append(out, statusdata.TailnetStatus{
			Name:       name,
			AuthMethod: authMethod, // per-runtime (tailnets[] entry), not the unused top-level block (#116)
			Cache:      runtimeCacheInfo(rt),
			Collectors: cols,
			Devices:    runtimeDeviceRows(rt),
			API:        runtimeAPIInfo(rt),
			Failing:    failing,
		})
	}
	return out
}

// checkpointStuckMargin is the number of poll intervals (beyond a window
// collector's own Lag) a checkpoint may trail "now" before it is flagged stuck.
const checkpointStuckMargin = 3

// apiInfo builds the combined API-health section across every tailnet runtime.
// The auth method shown is the primary runtime's (they share one auth model in
// practice; the per-tailnet breakdown carries each runtime's own).
func (a *App) apiInfo() statusdata.APIInfo {
	var stats []*APIStats
	for _, rt := range a.runtimes {
		stats = append(stats, rt.apiStats)
	}
	method := ""
	if len(a.runtimes) > 0 {
		method = a.runtimes[0].authMethod
	}
	return apiInfoFrom(method, stats...)
}

// runtimeAPIInfo builds one runtime's API-health section.
func runtimeAPIInfo(rt *tailnetRuntime) statusdata.APIInfo {
	return apiInfoFrom(rt.authMethod, rt.apiStats)
}

// apiInfoFrom merges the per-endpoint request stats from one or more APIStats
// into a single section. The rate-limit summary is set only once a 429 has been
// observed.
func apiInfoFrom(method string, stats ...*APIStats) statusdata.APIInfo {
	var snaps []APIEndpointSnapshot
	for _, st := range stats {
		if st == nil {
			continue
		}
		snaps = append(snaps, st.Snapshot()...)
	}
	info := statusdata.APIInfo{Endpoints: make([]statusdata.APIEndpoint, 0, len(snaps))}
	var total429 int64
	var totalCalls int64
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
		totalCalls += s.Requests
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
	info.Auth = statusdata.APIAuth{
		Method:     method,
		TotalCalls: totalCalls,
		Total429:   total429,
	}
	return info
}

// cacheInfo combines the device-cache totals across runtimes (Devices summed;
// Age is the oldest/largest across runtimes).
func (a *App) cacheInfo() statusdata.CacheInfo {
	var devices int
	var maxAge time.Duration
	for _, rt := range a.runtimes {
		devices += rt.cache.Len()
		if age := rt.cache.Age(); age > maxAge {
			maxAge = age
		}
	}
	return statusdata.CacheInfo{
		Devices: devices,
		AgeSec:  int64(maxAge.Seconds()),
		Age:     humanDuration(maxAge),
	}
}

// runtimeCacheInfo builds one runtime's device-cache section.
func runtimeCacheInfo(rt *tailnetRuntime) statusdata.CacheInfo {
	age := rt.cache.Age()
	return statusdata.CacheInfo{
		Devices: rt.cache.Len(),
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
	// Report every runtime's flow/audit sets (not just runtimes[0]) so a busy
	// tailnet's dedup occupancy is visible in multi-tailnet mode; prefix with the
	// tailnet name when multi to disambiguate (#60).
	multi := a.multiTailnet()
	for _, rt := range a.runtimes {
		prefix := ""
		if multi {
			prefix = rt.name + "/"
		}
		add(prefix+"flow", rt.flowDedup)
		add(prefix+"audit", rt.auditDedup)
	}
	add("webhook_cross", a.webhookDedup)
	return out
}

// deviceRows returns the combined device rows across every runtime's cache.
func (a *App) deviceRows() []statusdata.DeviceRow {
	var out []statusdata.DeviceRow
	for _, rt := range a.runtimes {
		out = append(out, runtimeDeviceRows(rt)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// runtimeDeviceRows returns one runtime's device rows (sorted by name).
func runtimeDeviceRows(rt *tailnetRuntime) []statusdata.DeviceRow {
	devs := rt.cache.Snapshot()
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

// nodeDiscovery reports the first runtime that has a node-metrics collector
// (node-metrics is process-global in practice; in multi-tailnet it runs per
// tailnet but the discovery view shows the first active one).
func (a *App) nodeDiscovery() statusdata.NodeDiscovery {
	var nm *nodemetrics.Collector
	for _, rt := range a.runtimes {
		if rt.nodeMetrics != nil {
			nm = rt.nodeMetrics
			break
		}
	}
	if nm == nil {
		return statusdata.NodeDiscovery{}
	}
	ds := nm.Snapshot()
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
// right empty state rather than a misleading zero. labels and perMetric carry
// the label-cardinality and growth inputs (empty when self-obs is off); th is
// the configured warning/critical thresholds.
func cardinalityInfo(selfObs bool, series []telemetry.SeriesCount, labels []telemetry.LabelStat, perMetric map[string][]int, th statusdata.CardinalityThresholds, metricByName map[string]metricdoc.Metric) statusdata.CardinalityInfo {
	if !selfObs || len(series) == 0 {
		return statusdata.CardinalityInfo{Available: false, Thresholds: th}
	}
	info := statusdata.CardinalityInfo{
		Available:    true,
		Thresholds:   th,
		TotalMetrics: len(series),
		Series:       make([]statusdata.SeriesRow, 0, len(series)),
	}
	for _, sc := range series {
		level := cardSeriesLevel(sc.Count, th)
		info.Total += sc.Count
		info.Series = append(info.Series, statusdata.SeriesRow{
			Metric:   sc.Metric,
			PromName: promNameOf(sc.Metric, metricByName),
			Count:    sc.Count,
			Capped:   sc.Capped,
			Level:    level,
		})
		if level != "" {
			info.Alerts = append(info.Alerts, statusdata.CardinalityAlert{
				Metric: sc.Metric, Count: sc.Count, Level: level,
			})
		}
	}
	// Alerts: critical before warning, then by count desc.
	sort.Slice(info.Alerts, func(i, j int) bool {
		if info.Alerts[i].Level != info.Alerts[j].Level {
			return info.Alerts[i].Level == "critical" // critical first
		}
		return info.Alerts[i].Count > info.Alerts[j].Count
	})
	info.Labels = buildLabelRows(labels, metricByName)
	info.Growth = buildGrowthRows(perMetric, metricByName)
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
	// Auth identity: prefer the resolved primary runtime's (the tailnets[] entry in
	// multi mode) over the unused top-level tailscale: block, which is ignored when
	// a tailnets: list is configured (#116). Falls back to the top-level block for
	// headscale / when no Tailscale runtime resolved an auth method.
	authMethod := c.Tailscale.Auth.Method
	apiKeySet := c.Tailscale.Auth.APIKey != ""
	oauthSecretSet := c.Tailscale.Auth.OAuth.ClientSecret != ""
	if len(a.runtimes) > 0 && a.runtimes[0].authMethod != "" {
		authMethod = a.runtimes[0].authMethod
		apiKeySet = a.runtimes[0].apiKeySet
		oauthSecretSet = a.runtimes[0].oauthSecretSet
	}
	cs := statusdata.ConfigSummary{
		LogLevel:          c.LogLevel,
		AuthMethod:        authMethod,
		CheckpointStore:   a.checkpointEffective, // effective store, not the raw config value (#69)
		EnabledCollectors: a.enabledCollectorNames(),
		APIKeySet:         apiKeySet,
		OAuthSecretSet:    oauthSecretSet,
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

// enabledCollectorNames lists the distinct enabled collector names across all
// runtimes (deduped — the same collectors run per tailnet).
func (a *App) enabledCollectorNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, rt := range a.runtimes {
		for _, e := range rt.registry.Entries() {
			if n := e.Collector.Name(); !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	return names
}

// freshnessState reports the age of a collector's last SUCCESSFUL run and
// buckets it: "ok" (<= 2 intervals), "warning" (<= 5 intervals), "stale"
// (beyond), or "none" when there has been no success yet. A non-positive
// interval degrades to "ok" for any success (no meaningful staleness scale).
func freshnessState(lastSuccess, now time.Time, interval time.Duration) (sec int64, human, state string) {
	if lastSuccess.IsZero() {
		return 0, "", "none"
	}
	age := now.Sub(lastSuccess)
	if age < 0 {
		age = 0
	}
	sec = int64(age.Seconds())
	human = humanDuration(age)
	switch {
	case interval <= 0, age <= 2*interval:
		state = "ok"
	case age <= 5*interval:
		state = "warning"
	default:
		state = "stale"
	}
	return sec, human, state
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
