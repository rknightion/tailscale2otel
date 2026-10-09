package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// This is a finite-backlog experiment, not a steady-state capacity test. All
// cases use the production coordinator. Since TSO-0148 the drain completes on
// NORMAL scheduled metric slots at an explicit fixture interval rather than a
// per-body ForceFlush, so its wall-clock results are not comparable with the
// historical per-flush measurements. Coalescing HEC bodies BEFORE admission
// explores fewer flush barriers without pretending to implement group commit.
type walLoadCase struct {
	name     string
	mode     string
	group    int
	batch    int
	collapse bool
	delta    bool
}

func walLoadCases() []walLoadCase {
	return []walLoadCase{
		{name: "baseline", mode: "both", group: 1, batch: 10000},
		{name: "coalesced8_experiment", mode: "both", group: 8, batch: 10000},
		{name: "rollup_only", mode: "rollup", group: 1, batch: 10000},
		{name: "collapse_external", mode: "both", group: 1, batch: 10000, collapse: true},
		{name: "otlp_batch1000", mode: "both", group: 1, batch: 1000},
		{name: "delta", mode: "both", group: 1, batch: 10000, delta: true},
	}
}

// The sink tracks the latest value for EACH cumulative series, not the sum of
// repeated exports. This catches missing bodies, dropped work and double apply.
type walLoadSink struct {
	mu       sync.Mutex
	requests int
	rejected int
	points   int
	latest   map[string]map[string]float64
	err      error

	// Sustained-harness extensions. All are zero for the finite cases.
	fail   func() bool                    // true: answer 503 without recording anything
	stamps map[string]map[string][]uint64 // metric -> series -> exported TimeUnixNano
}

// ioTotal is the bytes of MetricIO the sink has decoded from successful
// exports: the latest value of each cumulative series, summed.
func (s *walLoadSink) ioTotal() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total float64
	for _, value := range s.latest[flowlog.MetricIO] {
		total += value
	}
	return total
}

// walLoadAddrID recovers the synthetic flow id from a "2001:db8::H:L" address,
// the inverse of the generator in walLoadWriteEntry.
func walLoadAddrID(raw string) (int, bool) {
	addr, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	if err != nil || !addr.Is6() {
		return 0, false
	}
	b := addr.As16()
	if b[0] != 0x20 || b[1] != 0x01 || b[2] != 0x0d || b[3] != 0xb8 {
		return 0, false
	}
	hi, lo := int(b[12])<<8|int(b[13]), int(b[14])<<8|int(b[15])
	return hi*65535 + lo - 1, true
}

func (s *walLoadSink) serve(w http.ResponseWriter, r *http.Request, delay time.Duration) {
	if r.URL.Path != "/v1/metrics" && r.URL.Path != "/v1/logs" {
		http.Error(w, "unexpected endpoint", http.StatusNotFound)
		return
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	body, err := io.ReadAll(r.Body)
	if s.fail != nil && s.fail() {
		s.mu.Lock()
		s.rejected++
		s.mu.Unlock()
		http.Error(w, "synthetic exporter outage", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/v1/metrics" {
		var request collectormetric.ExportMetricsServiceRequest
		if err == nil {
			err = proto.Unmarshal(body, &request)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.err = err
			http.Error(w, "invalid protobuf", http.StatusBadRequest)
			return
		}
		s.requests++
		for _, resource := range request.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					s.points += len(metric.GetSum().GetDataPoints()) + len(metric.GetGauge().GetDataPoints()) +
						len(metric.GetHistogram().GetDataPoints()) + len(metric.GetExponentialHistogram().GetDataPoints())
					totals := metric.Name == flowlog.MetricIO || metric.Name == flowlog.MetricIORollup
					stamped := s.stamps[metric.Name] != nil
					if !totals && !stamped {
						continue
					}
					if totals && s.latest[metric.Name] == nil {
						s.latest[metric.Name] = make(map[string]float64)
					}
					for _, point := range metric.GetSum().GetDataPoints() {
						// SDK attributes are canonically ordered. Length-prefix each
						// encoded attribute so series keys cannot be ambiguous.
						var key bytes.Buffer
						for _, attr := range point.Attributes {
							encoded, marshalErr := proto.Marshal(attr)
							if marshalErr != nil {
								s.err = marshalErr
								continue
							}
							fmt.Fprintf(&key, "%d:", len(encoded))
							key.Write(encoded)
						}
						if stamped {
							s.stamps[metric.Name][key.String()] = append(s.stamps[metric.Name][key.String()], point.GetTimeUnixNano())
						}
						if !totals {
							continue
						}
						if metric.GetSum().AggregationTemporality == metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
							s.latest[metric.Name][key.String()] += point.GetAsDouble()
						} else {
							s.latest[metric.Name][key.String()] = point.GetAsDouble()
						}
					}
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK) // Empty protobuf Export*ServiceResponse.
}

type walLoadResult struct {
	drain        time.Duration
	seed         time.Duration
	requests     int
	points       int
	entries      int
	pendingBytes int64
}

// walLoadWriteEntry appends one original HEC body. Reserved synthetic IPv6
// space gives each flow a unique endpoint; no private captures, external lookups
// or real identities. The bytes depend only on (entry, flows), so every case
// admits identical entries.
func walLoadWriteEntry(body *bytes.Buffer, entry, flows int) {
	for flow := range flows {
		id := entry*flows + flow
		fmt.Fprintf(body, `{"event":{"nodeId":"synthetic","start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::%x:%x]:443","txBytes":10,"rxBytes":20,"txPkts":1,"rxPkts":2}]}}`, id/65535, id%65535+1)
	}
}

func runWALLoad(tb testing.TB, tc walLoadCase, entries, flows int, delay, interval time.Duration) walLoadResult {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sink := &walLoadSink{latest: make(map[string]map[string]float64)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.serve(w, r, delay)
	}))
	defer server.Close()
	temporality := "cumulative"
	if tc.delta {
		temporality = "delta"
	}
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		Protocol: "http", Endpoint: server.URL, Insecure: true,
		ServiceName: "wal-load", MetricInterval: interval,
		MetricExportBatchSize: tc.batch, CardinalityLimit: entries*flows*4 + 1000,
		MetricTemporality: temporality,
	})
	if err != nil {
		tb.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := p.Shutdown(closeCtx); err != nil {
			tb.Error(err)
		}
	}()
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = tc.mode
	cfg.Cardinality.Flow.CollapseExternal = tc.collapse
	cfg.Cardinality.Flow.DestinationPort = true
	cfg.Collectors.Flowlogs.LogMode = "off" // Isolate the metric flush cost.
	cfg.Streaming.Enabled = true
	cfg.Streaming.Token = "local-load-token"
	cfg.IngressWAL.Enabled = true
	cfg.IngressWAL.Directory = tb.TempDir()
	cfg.IngressWAL.MaxBytes = 256 << 20
	cfg.IngressWAL.MaxEntries = entries + 1
	a := newAppShell(cfg, "load", slog.New(slog.NewTextHandler(io.Discard, nil)),
		p.Emitter(), p.Tracer(), func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	// No scheduler runs. Any accidental API request remains on loopback and is
	// rejected by the sink instead of using credentials or a real control plane.
	client, err := tsapi.NewClient(tsapi.Options{Tailnet: "example.com", BaseURL: server.URL, APIKey: "local-only"})
	if err != nil {
		tb.Fatal(err)
	}
	a.addRuntimeConfigured("example.com", "example.com", p.Emitter(), nil, nil,
		p, provider.Tailscale(client), false)
	if err := a.buildIngressWAL(a.buildReceivers()); err != nil {
		tb.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := a.Close(closeCtx); err != nil {
			tb.Error(err)
		}
	}()

	seedStart := time.Now()
	for first := 0; first < entries; first += tc.group {
		var body bytes.Buffer
		for entry := first; entry < min(first+tc.group, entries); entry++ {
			walLoadWriteEntry(&body, entry, flows)
		}
		request := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, &body)
		request.SetBasicAuth("", "local-load-token")
		response := httptest.NewRecorder()
		a.streamSrv.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			tb.Fatalf("admission: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	health := a.ingressWAL.Health().WAL
	result := walLoadResult{seed: time.Since(seedStart), entries: health.PendingEntries, pendingBytes: health.PendingBytes}
	if want := (entries + tc.group - 1) / tc.group; health.PendingEntries != want {
		tb.Fatalf("pending entries = %d, want %d", health.PendingEntries, want)
	}
	started := time.Now()
	if err := a.ingressWAL.Replay(ctx); err != nil {
		tb.Fatal(err)
	}
	result.drain = time.Since(started)
	// Completion must come from normal slots, never a lifecycle collection.
	if stats := p.CollectionStats(); stats.RequiredSnapshotsAcknowledged == 0 || stats.TerminalAttempts != 0 || stats.ScheduledAttempts == 0 {
		tb.Fatalf("drain did not complete on normal scheduled collections: %+v", stats)
	}
	if health := a.ingressWAL.Health(); !a.ingressWAL.Ready() || health.WAL.PendingEntries != 0 || health.WAL.PendingBytes != 0 {
		tb.Fatalf("incomplete drain: %+v", health)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.err != nil {
		tb.Fatal(sink.err)
	}
	for _, name := range []string{flowlog.MetricIO, flowlog.MetricIORollup} {
		var got float64
		for _, value := range sink.latest[name] {
			got += value
		}
		want := float64(entries * flows * 30)
		if tc.mode == "rollup" && name == flowlog.MetricIO {
			want = 0
		}
		if got != want {
			tb.Fatalf("sink-observed %s bytes = %g, want %g", name, got, want)
		}
	}
	result.requests, result.points = sink.requests, sink.points
	return result
}

func TestIngressWALLoadAccounting(t *testing.T) {
	for _, tc := range walLoadCases() {
		t.Run(tc.name, func(t *testing.T) {
			runWALLoad(t, tc, 9, 3, 0, time.Second) // Includes a partial coalesced group.
		})
	}
}

func walLoadInt(tb testing.TB, key string, fallback, maximum int) int {
	tb.Helper()
	if raw := os.Getenv(key); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maximum {
			tb.Fatalf("%s must be an integer in [1,%d]", key, maximum)
		}
		return value
	}
	return fallback
}

func BenchmarkIngressWALDrain(b *testing.B) {
	entries := walLoadInt(b, "WAL_LOAD_ENTRIES", 16, 256)
	flows := walLoadInt(b, "WAL_LOAD_FLOWS", 256, 4096)
	if entries*flows > 65536 {
		b.Fatal("WAL load is limited to 65536 total flows per case")
	}
	delay := time.Duration(walLoadInt(b, "WAL_LOAD_DELAY_MS", 2, 100)) * time.Millisecond
	interval := time.Duration(walLoadInt(b, "WAL_LOAD_INTERVAL_MS", 1000, 60000)) * time.Millisecond
	for _, tc := range walLoadCases() {
		b.Run(tc.name, func(b *testing.B) {
			var drain, seed time.Duration
			var requests, points, pending, walEntries float64
			for range b.N {
				r := runWALLoad(b, tc, entries, flows, delay, interval)
				drain += r.drain
				seed += r.seed
				requests += float64(r.requests)
				points += float64(r.points)
				pending += float64(r.pendingBytes)
				walEntries += float64(r.entries)
			}
			b.ReportMetric(float64(entries*flows)*float64(b.N)/drain.Seconds(), "flows/s")
			b.ReportMetric(drain.Seconds()/float64(b.N), "drain-s/op")
			b.ReportMetric(seed.Seconds()/float64(b.N), "seed-s/op")
			b.ReportMetric(requests/float64(b.N), "metric-requests/op")
			b.ReportMetric(points/float64(b.N), "exported-points/op")
			b.ReportMetric(pending/float64(b.N), "peak-WAL-bytes/op")
			b.ReportMetric(walEntries/float64(b.N), "WAL-entries/op")
		})
	}
}

// ---------------------------------------------------------------------------
// Sustained-arrival comparison (TSO-0148 criterion 7).
//
// The finite cases above drain a pre-seeded backlog. They say nothing about
// cadence, bounded backlog or commit safety, which only show up while work keeps
// arriving and the exporter misbehaves. Each case below admits the SAME entries
// (byte-identical, asserted against the generator) at a fixed pace through the
// production receiver while the production worker drains them, then checks:
//
//   - backlog: pending WAL entries/bytes stay within the configured WAL bound;
//   - cadence: the exported timestamps of a stable unrelated metric and of the
//     ingress-driven metric never come closer than the schedule allows, and the
//     provider never attempts more scheduled collections than the clock permits;
//   - totals: the bytes the sink actually decoded equal the admitted work;
//   - commit safety: no entry is committed before every one of its series was
//     delivered to the sink, every entry commits exactly once, nothing is lost
//     after the exporter recovers.
//
// Every assertion fails the run; the report is logged first so a failure is
// diagnosable. Synthetic, local, loopback sink only: not production sizing.
// ---------------------------------------------------------------------------

const (
	walLoadBytesPerFlow   = 30 // txBytes 10 + rxBytes 20 in walLoadWriteEntry
	walLoadStableMetric   = "wal.load.stable"
	walLoadSampleInterval = 5 * time.Millisecond
	// A sample closer than this fraction of the interval to the previous sample of
	// the same series cannot come from the schedule (a delayed collection moves one
	// sample by a fraction of the interval; observed worst case 0.57 of it, after
	// an outage); it is an out-of-schedule collection. Jitter cancels over a
	// series' lifetime, so the average rate is held to the schedule far more tightly.
	walLoadCadenceFloor = 0.4
	walLoadRateCeiling  = 1.25
)

type walSustainedCase struct {
	name   string
	delay  time.Duration // latency added to every sink request
	outage time.Duration // sink answers 503 for this long, starting at the first export request
}

type walSustainedParams struct {
	entries, flows int
	gap, interval  time.Duration
	deadline       time.Duration
	// retry overrides the exporter retry policy (nil keeps the production default,
	// 5 s initial backoff); retryAllowance is the matching lag the WAL bound allows
	// after an outage.
	retry          *telemetry.RetryPolicy
	retryAllowance time.Duration
	// lagIntervals (zero means 4) sizes the WAL bound; cadenceFloor (zero means
	// walLoadCadenceFloor) is the closest two samples of a series may be, as a
	// fraction of the interval. The short in-suite test widens both for loaded CI.
	lagIntervals int
	cadenceFloor float64
}

type walSustainedResult struct {
	name                        string
	bound                       int
	peakEntries, steadyEntries  int
	peakBytes                   int64
	admitted, exported          float64
	entries, commits            int
	violations                  []string
	scheduled, terminal         uint64
	stableSamples               int
	stableRate, ingressRate     float64
	stableMinGap, ingressMinGap time.Duration
	ingressSeries               int
	lagP50, lagMax              time.Duration
	drainAfterLast, total       time.Duration
	flowsPerSecond              float64
	rejected, requests          int
	digest                      string
	failures                    []string
}

// walLoadAudit decorates the real WAL. It changes no behavior: it observes
// which admitted entry each prepared generation is, and checks at the moment a
// commit is requested that the sink has already decoded, from SUCCESSFUL exports,
// at least the bytes of every entry committed so far plus this one. Entries are
// identical in size and the counters cumulative and additive, so k committed
// entries need k entries' bytes at the sink. That is a necessary condition: it
// proves a premature commit, but delivered bytes of a not-yet-committed entry
// could mask one by a single entry. Series carry no per-entry identity (flow
// attributes are aggregated), so a stricter per-entry check is not available.
type walLoadAudit struct {
	ingresswal.WAL
	sink  *walLoadSink
	flows int

	mu         sync.Mutex
	entry      map[string]int // generation ID -> entry index
	digest     map[int][sha256.Size]byte
	commits    map[int]int
	committed  map[int]time.Time
	violations []string
}

func (w *walLoadAudit) PrepareWindow(ctx context.Context, limits ingresswal.WindowLimits, held []ingresswal.Generation, observe ingresswal.GenerationObserver) ([]ingresswal.PreparedEntry, error) {
	entries, err := w.WAL.PrepareWindow(ctx, limits, held, observe)
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, prepared := range entries {
		body := string(prepared.Envelope.Body)
		start := strings.Index(body, `"dst":"[`)
		if start < 0 {
			w.violations = append(w.violations, "prepared entry carries no synthetic address")
			continue
		}
		start += len(`"dst":"[`)
		end := strings.Index(body[start:], `]:`)
		if end < 0 {
			w.violations = append(w.violations, "prepared entry carries no synthetic address")
			continue
		}
		id, ok := walLoadAddrID(body[start : start+end])
		if !ok {
			w.violations = append(w.violations, "prepared entry address is not synthetic")
			continue
		}
		index := id / w.flows
		w.entry[prepared.Generation.ID()] = index
		w.digest[index] = sha256.Sum256(prepared.Envelope.Body)
	}
	return entries, err
}

func (w *walLoadAudit) CommitPrepared(ctx context.Context, g ingresswal.Generation) (ingresswal.PreparedOutcome, error) {
	delivered := w.sink.ioTotal() // read first: delivery precedes any legitimate commit
	w.mu.Lock()
	index, known := w.entry[g.ID()]
	if !known {
		w.violations = append(w.violations, "commit requested for a generation never prepared through the window")
	} else {
		covered := len(w.committed)
		if _, done := w.committed[index]; !done {
			covered++
		}
		if need := float64(covered * w.flows * walLoadBytesPerFlow); delivered < need {
			w.violations = append(w.violations, fmt.Sprintf("entry %d commit requested with %g bytes delivered, %g required for %d committed entries", index, delivered, need, covered))
		}
	}
	w.mu.Unlock()
	outcome, err := w.WAL.CommitPrepared(ctx, g)
	if err == nil && outcome == ingresswal.PreparedRetired && known {
		w.mu.Lock()
		w.commits[index]++
		if _, done := w.committed[index]; !done {
			w.committed[index] = time.Now()
		}
		w.mu.Unlock()
	}
	return outcome, err
}

// walLoadCadence summarizes distinct exported timestamps per series. Retried
// deliveries of one collection repeat a timestamp and count once. rate is the
// aggregate samples per series per interval over each series' own lifetime.
func walLoadCadence(series map[string][]uint64, interval time.Duration) (samples int, rate float64, minGap time.Duration, withGaps int) {
	var gaps int
	var span time.Duration
	minGap = -1
	for _, stamps := range series {
		unique := slices.Clone(stamps)
		slices.Sort(unique)
		unique = slices.Compact(unique)
		samples += len(unique)
		if len(unique) < 2 {
			continue
		}
		withGaps++
		gaps += len(unique) - 1
		span += time.Duration(unique[len(unique)-1] - unique[0])
		for i := 1; i < len(unique); i++ {
			if gap := time.Duration(unique[i] - unique[i-1]); minGap < 0 || gap < minGap {
				minGap = gap
			}
		}
	}
	if span > 0 {
		rate = float64(gaps) * float64(interval) / float64(span)
	}
	return samples, rate, minGap, withGaps
}

// walSustainedBound is the configured WAL entry cap for a case: the entries that
// can arrive while one completion is outstanding. Completion needs the entry to
// be prepared and applied, a scheduled slot after its effect, a delivery (which
// this sink delays) and, when a delivery overruns, a later slot: four intervals
// plus twice the sink delay. An outage adds its duration plus the exporter's own
// first retry delay. Measured peaks sit well below this; the cap is never
// raised to make a run pass.
func walSustainedBound(tc walSustainedCase, p walSustainedParams) int {
	lag := time.Duration(cmp.Or(p.lagIntervals, 4))*p.interval + 2*tc.delay + tc.outage
	if tc.outage > 0 {
		lag += p.retryAllowance
	}
	return min(p.entries, int((lag+p.gap-1)/p.gap)+2)
}

func runWALSustained(tb testing.TB, tc walSustainedCase, p walSustainedParams) walSustainedResult {
	tb.Helper()
	if p.gap <= 0 || p.interval <= 0 || p.entries <= 0 || p.flows <= 0 {
		tb.Fatalf("sustained workload needs positive entries, flows, arrival gap and interval: %+v", p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.deadline)
	defer cancel()
	result := walSustainedResult{name: tc.name, bound: walSustainedBound(tc, p)}
	fail := func(format string, args ...any) {
		result.failures = append(result.failures, fmt.Sprintf(format, args...))
	}

	var outageStart atomic.Int64
	sink := &walLoadSink{
		latest: make(map[string]map[string]float64),
		stamps: map[string]map[string][]uint64{flowlog.MetricIO: {}, walLoadStableMetric: {}},
	}
	sink.fail = func() bool {
		if tc.outage <= 0 {
			return false
		}
		// The window opens at the first export request, so it always overlaps
		// delivery: that request is itself rejected, however loaded the machine.
		outageStart.CompareAndSwap(0, time.Now().UnixNano())
		return time.Now().UnixNano() < outageStart.Load()+int64(tc.outage)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.serve(w, r, tc.delay)
	}))
	defer server.Close()

	providerStart := time.Now()
	prov, err := telemetry.NewProvider(ctx, telemetry.Options{
		Protocol: "http", Endpoint: server.URL, Insecure: true,
		ServiceName: "wal-load", MetricInterval: p.interval,
		MetricExportBatchSize: 10000, CardinalityLimit: p.entries*p.flows*4 + 1000,
		MetricTemporality: "cumulative",
		Transport:         telemetry.TransportOptions{Retry: p.retry},
	})
	if err != nil {
		tb.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := prov.Shutdown(closeCtx); err != nil {
			tb.Error(err)
		}
	}()
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "both"
	cfg.Cardinality.Flow.DestinationPort = true
	cfg.Collectors.Flowlogs.LogMode = "off"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Token = "local-load-token"
	cfg.IngressWAL.Enabled = true
	cfg.IngressWAL.Directory = tb.TempDir()
	cfg.IngressWAL.MaxBytes = 256 << 20
	cfg.IngressWAL.MaxEntries = result.bound
	a := newAppShell(cfg, "load", slog.New(slog.NewTextHandler(io.Discard, nil)),
		prov.Emitter(), prov.Tracer(), func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	client, err := tsapi.NewClient(tsapi.Options{Tailnet: "example.com", BaseURL: server.URL, APIKey: "local-only"})
	if err != nil {
		tb.Fatal(err)
	}
	a.addRuntimeConfigured("example.com", "example.com", prov.Emitter(), nil, nil,
		prov, provider.Tailscale(client), false)
	if err := a.buildIngressWAL(a.buildReceivers()); err != nil {
		tb.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := a.Close(closeCtx); err != nil {
			tb.Error(err)
		}
	}()
	audit := &walLoadAudit{
		WAL: a.ingressWAL.wal, sink: sink, flows: p.flows,
		entry: map[string]int{}, digest: map[int][sha256.Size]byte{},
		commits: map[int]int{}, committed: map[int]time.Time{},
	}
	a.ingressWAL.wal = audit // before any worker or receiver goroutine exists

	// A stable collector-like metric unrelated to ingress shares the provider.
	prov.Emitter().Counter(walLoadStableMetric, "1", "stable synthetic metric", 1, nil)

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = a.ingressWAL.Run(workerCtx) }()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			stopWorker()
			<-workerDone
		}
	}
	defer stop()

	type backlogSample struct {
		at      time.Duration
		entries int
		bytes   int64
	}
	var samplesMu sync.Mutex
	var samples []backlogSample
	samplerCtx, stopSampler := context.WithCancel(ctx)
	samplerDone := make(chan struct{})
	runStart := time.Now()
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(walLoadSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-samplerCtx.Done():
				return
			case <-ticker.C:
				h := a.ingressWAL.Health().WAL
				samplesMu.Lock()
				samples = append(samples, backlogSample{time.Since(runStart), h.PendingEntries, h.PendingBytes})
				samplesMu.Unlock()
			}
		}
	}()

	// Paced arrivals through the production stream receiver.
	admitAt := make([]time.Time, p.entries)
	wantDigest := make(map[int][sha256.Size]byte, p.entries)
	arrivalsStart := time.Now()
	for entry := range p.entries {
		if wait := time.Until(arrivalsStart.Add(time.Duration(entry) * p.gap)); wait > 0 {
			time.Sleep(wait)
		}
		var body bytes.Buffer
		walLoadWriteEntry(&body, entry, p.flows)
		wantDigest[entry] = sha256.Sum256(body.Bytes())
		request := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, &body)
		request.SetBasicAuth("", "local-load-token")
		response := httptest.NewRecorder()
		a.streamSrv.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			fail("admission of entry %d refused with HTTP %d (WAL bound %d exceeded?)", entry, response.Code, result.bound)
			break
		}
		admitAt[entry] = time.Now()
	}
	lastArrival := time.Now()

	// Wait for the production worker to complete everything it admitted.
	for ctx.Err() == nil && a.ingressWAL.Health().WAL.PendingEntries != 0 {
		time.Sleep(walLoadSampleInterval)
	}
	drained := time.Now()
	stopSampler()
	<-samplerDone
	if ctx.Err() != nil {
		fail("did not drain before the %s case deadline; pending %+v", p.deadline, a.ingressWAL.Health())
	}
	stop()
	stats := prov.CollectionStats()
	wall := time.Since(providerStart)

	// Backlog.
	samplesMu.Lock()
	var steady []int
	for _, s := range samples {
		result.peakEntries = max(result.peakEntries, s.entries)
		result.peakBytes = max(result.peakBytes, s.bytes)
		// Steady state: after the first interval of warm-up, while arrivals continue.
		if s.at >= p.interval && s.at <= lastArrival.Sub(runStart) {
			steady = append(steady, s.entries)
		}
	}
	samplesMu.Unlock()
	if len(steady) > 0 {
		slices.Sort(steady)
		result.steadyEntries = steady[len(steady)/2]
	}
	if result.peakEntries > result.bound {
		fail("backlog peaked at %d entries, above the configured bound %d", result.peakEntries, result.bound)
	}
	if bytesBound := cfg.IngressWAL.MaxBytes; result.peakBytes > bytesBound {
		fail("backlog peaked at %d bytes, above the configured bound %d", result.peakBytes, bytesBound)
	}
	if health := a.ingressWAL.Health(); health.WAL.PendingEntries != 0 || health.WAL.PendingBytes != 0 || health.State == ingressWALStateFailed {
		fail("WAL not empty after recovery: %+v", health)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.err != nil {
		fail("sink decode error: %v", sink.err)
	}
	result.requests, result.rejected = sink.requests, sink.rejected
	if tc.outage > 0 && sink.rejected == 0 {
		fail("outage case rejected no export request: the failure window never overlapped delivery, so this run was a healthy case")
	}

	// Exported totals against admitted work.
	result.admitted = float64(p.entries * p.flows * walLoadBytesPerFlow)
	for _, name := range []string{flowlog.MetricIO, flowlog.MetricIORollup} {
		var got float64
		for _, value := range sink.latest[name] {
			got += value
		}
		if name == flowlog.MetricIO {
			result.exported = got
		}
		if got != result.admitted {
			fail("sink-observed %s bytes = %g, admitted %g", name, got, result.admitted)
		}
	}

	// Cadence: real exported timestamps, not request or ForceFlush counts.
	stable := sink.stamps[walLoadStableMetric]
	result.stableSamples, result.stableRate, result.stableMinGap, _ = walLoadCadence(stable, p.interval)
	_, result.ingressRate, result.ingressMinGap, result.ingressSeries = walLoadCadence(sink.stamps[flowlog.MetricIO], p.interval)
	result.scheduled, result.terminal = stats.ScheduledAttempts, stats.TerminalAttempts
	floor := time.Duration(float64(p.interval) * cmp.Or(p.cadenceFloor, walLoadCadenceFloor))
	if result.stableSamples < 2 || result.ingressSeries == 0 {
		fail("run too short to observe cadence: %d stable samples, %d ingress series with two samples", result.stableSamples, result.ingressSeries)
	}
	if result.stableMinGap >= 0 && result.stableMinGap < floor {
		fail("stable metric sampled %s apart, closer than %s (schedule %s)", result.stableMinGap, floor, p.interval)
	}
	if result.ingressMinGap >= 0 && result.ingressMinGap < floor {
		fail("ingress metric sampled %s apart, closer than %s (schedule %s)", result.ingressMinGap, floor, p.interval)
	}
	if result.stableRate > walLoadRateCeiling || result.ingressRate > walLoadRateCeiling {
		fail("average samples per series per interval exceed the schedule: stable %.2f, ingress %.2f (ceiling %.2f)", result.stableRate, result.ingressRate, walLoadRateCeiling)
	}
	if limit := uint64(wall/p.interval) + 1; stats.ScheduledAttempts > limit {
		fail("%d scheduled collections in %s exceeds the clock's %d", stats.ScheduledAttempts, wall.Round(time.Millisecond), limit)
	}
	if stats.TerminalAttempts != 0 {
		fail("%d terminal collections before shutdown", stats.TerminalAttempts)
	}
	if uint64(result.stableSamples) > stats.ScheduledAttempts {
		fail("%d distinct stable samples exceed %d scheduled collections", result.stableSamples, stats.ScheduledAttempts)
	}

	// Commit safety and entry identity.
	audit.mu.Lock()
	defer audit.mu.Unlock()
	result.violations = slices.Clone(audit.violations)
	var lags []time.Duration
	combined := sha256.New()
	for entry := range p.entries {
		if audit.digest[entry] != wantDigest[entry] {
			fail("WAL entry %d differs from the generated body (or was never prepared)", entry)
		}
		want := wantDigest[entry]
		combined.Write(want[:])
		switch n := audit.commits[entry]; {
		case n == 0:
			fail("entry %d never committed", entry)
		case n > 1:
			fail("entry %d committed %d times", entry, n)
		default:
			result.commits++
			if committed := audit.committed[entry]; !admitAt[entry].IsZero() {
				lags = append(lags, committed.Sub(admitAt[entry]))
			}
		}
	}
	for _, v := range audit.violations {
		fail("commit safety: %s", v)
	}
	if len(lags) > 0 {
		slices.Sort(lags)
		result.lagP50, result.lagMax = lags[len(lags)/2], lags[len(lags)-1]
	}
	result.entries = p.entries
	result.digest = fmt.Sprintf("%x", combined.Sum(nil)[:6])
	result.drainAfterLast = drained.Sub(lastArrival)
	result.total = drained.Sub(arrivalsStart)
	result.flowsPerSecond = float64(p.entries*p.flows) / result.total.Seconds()
	return result
}

func (r walSustainedResult) log(tb testing.TB, p walSustainedParams) {
	tb.Helper()
	verdict := "PASS"
	if len(r.failures) > 0 {
		verdict = "FAIL"
	}
	safety := "no entry committed before its cover; every entry committed once; nothing lost"
	if len(r.violations) > 0 || r.commits != r.entries {
		safety = fmt.Sprintf("VIOLATED (%d premature, %d/%d committed)", len(r.violations), r.commits, r.entries)
	}
	totals := "match"
	if r.exported != r.admitted {
		totals = "MISMATCH"
	}
	tb.Logf("%s [%s] digest=%s entries=%d flows/entry=%d gap=%s interval=%s\n"+
		"  backlog    peak=%d entries/%d bytes  steady(median)=%d entries  bound=%d entries\n"+
		"  cadence    stable: %d samples, %.2f samples/interval, min gap %s | ingress: %d series, %.2f samples/series/interval, min gap %s | scheduled collections %d, terminal %d\n"+
		"  totals     exported=%g admitted=%g (%s)  sink requests=%d rejected=%d\n"+
		"  commit     %s; completion lag p50=%s max=%s\n"+
		"  throughput %.0f flows/s over %s (arrivals %s; drain after last arrival %s)",
		r.name, verdict, r.digest, r.entries, p.flows, p.gap, p.interval,
		r.peakEntries, r.peakBytes, r.steadyEntries, r.bound,
		r.stableSamples, r.stableRate, r.stableMinGap.Round(time.Millisecond), r.ingressSeries, r.ingressRate, r.ingressMinGap.Round(time.Millisecond), r.scheduled, r.terminal,
		r.exported, r.admitted, totals, r.requests, r.rejected,
		safety, r.lagP50.Round(time.Millisecond), r.lagMax.Round(time.Millisecond),
		r.flowsPerSecond, r.total.Round(time.Millisecond), time.Duration(p.entries)*p.gap, r.drainAfterLast.Round(time.Millisecond))
	if len(r.failures) > 0 {
		tb.Fatalf("%s failed:\n  %s", r.name, strings.Join(r.failures, "\n  "))
	}
}

func walSustainedCases(fast, slow, outage time.Duration) []walSustainedCase {
	return []walSustainedCase{
		{name: "sustained_healthy", delay: fast},
		{name: "sustained_slow_exporter", delay: slow},
		{name: "failing_then_recovering", delay: fast, outage: outage},
	}
}

// Small enough for `just check`; the benchmark below uses the larger defaults.
// It runs the same assertions, and is sized so the WAL entry cap (19, 28 and 23
// of 30 entries for the healthy, slow and failing cases) is strictly below the
// admitted count: a backlog that stops draining fails the cap and the refused
// admission checks. It uses a 100 ms exporter retry instead of the production
// 5 s, so the outage resolves within the short run, and six lag intervals
// instead of four because stressed -race, 2-CPU runs reached 14 of a 16-entry
// healthy cap. The gap floor stays at the benchmark's 0.4 of the interval.
func TestIngressWALLoadSustainedAccounting(t *testing.T) {
	p := walSustainedParams{
		entries: 30, flows: 8, gap: 150 * time.Millisecond, interval: 400 * time.Millisecond, deadline: time.Minute,
		lagIntervals:   6,
		retry:          &telemetry.RetryPolicy{Enabled: true, InitialInterval: 100 * time.Millisecond, MaxInterval: 200 * time.Millisecond, MaxElapsedTime: 10 * time.Second},
		retryAllowance: 400 * time.Millisecond,
	}
	for _, tc := range walSustainedCases(2*time.Millisecond, 700*time.Millisecond, 300*time.Millisecond) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runWALSustained(t, tc, p).log(t, p)
		})
	}
}

func BenchmarkIngressWALSustained(b *testing.B) {
	interval := time.Duration(walLoadInt(b, "WAL_LOAD_INTERVAL_MS", 1000, 60000)) * time.Millisecond
	p := walSustainedParams{
		entries:  walLoadInt(b, "WAL_LOAD_SUSTAINED_ENTRIES", 60, 256),
		flows:    walLoadInt(b, "WAL_LOAD_FLOWS", 256, 4096),
		gap:      time.Duration(walLoadInt(b, "WAL_LOAD_ARRIVAL_MS", max(1, int(interval/time.Millisecond)/4), 10000)) * time.Millisecond,
		interval: interval,
		deadline: 5 * time.Minute,
		// Production exporter default: 5 s initial backoff with up to 50% jitter.
		retryAllowance: 7500 * time.Millisecond,
	}
	if p.entries*p.flows > 65536 {
		b.Fatal("WAL load is limited to 65536 total flows per case")
	}
	fast := time.Duration(walLoadInt(b, "WAL_LOAD_DELAY_MS", 2, 100)) * time.Millisecond
	slow := time.Duration(walLoadInt(b, "WAL_LOAD_SLOW_MS", int(interval/time.Millisecond)*3/2, 60000)) * time.Millisecond
	outage := time.Duration(walLoadInt(b, "WAL_LOAD_OUTAGE_MS", int(interval/time.Millisecond)*2, 60000)) * time.Millisecond
	for _, tc := range walSustainedCases(fast, slow, outage) {
		b.Run(tc.name, func(b *testing.B) {
			var last walSustainedResult
			var flowsPerSecond float64
			for range b.N {
				last = runWALSustained(b, tc, p)
				last.log(b, p)
				flowsPerSecond += last.flowsPerSecond
			}
			b.ReportMetric(flowsPerSecond/float64(b.N), "flows/s")
			b.ReportMetric(float64(last.peakEntries), "peak-pending-entries")
			b.ReportMetric(float64(last.steadyEntries), "steady-pending-entries")
			b.ReportMetric(float64(last.peakBytes), "peak-pending-bytes")
			b.ReportMetric(float64(last.bound), "bound-entries")
			b.ReportMetric(last.ingressRate, "ingress-samples/series/interval")
			b.ReportMetric(last.stableRate, "stable-samples/interval")
			b.ReportMetric(last.exported, "exported-bytes")
			b.ReportMetric(last.admitted, "admitted-bytes")
			b.ReportMetric(float64(len(last.violations)), "premature-commits")
			b.ReportMetric(last.lagP50.Seconds(), "completion-lag-p50-s")
			b.ReportMetric(last.lagMax.Seconds(), "completion-lag-max-s")
			b.ReportMetric(last.drainAfterLast.Seconds(), "drain-after-last-arrival-s")
		})
	}
}
