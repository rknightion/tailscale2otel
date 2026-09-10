package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// This is a finite-backlog experiment, not a steady-state capacity test. All
// cases use the production coordinator. Coalescing HEC bodies BEFORE admission
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
	points   int
	latest   map[string]map[string]float64
	err      error
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
					if metric.Name != flowlog.MetricIO && metric.Name != flowlog.MetricIORollup {
						continue
					}
					if s.latest[metric.Name] == nil {
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

func runWALLoad(tb testing.TB, tc walLoadCase, entries, flows int, delay time.Duration) walLoadResult {
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
		ServiceName: "wal-load", MetricInterval: time.Hour,
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
		p.ForceFlush, provider.Tailscale(client), false)
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
			for flow := range flows {
				id := entry*flows + flow
				// Reserved synthetic IPv6 space, unique endpoint per flow; no
				// private captures, external lookups or real identities.
				fmt.Fprintf(&body, `{"event":{"nodeId":"synthetic","start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::%x:%x]:443","txBytes":10,"rxBytes":20,"txPkts":1,"rxPkts":2}]}}`, id/65535, id%65535+1)
			}
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
			runWALLoad(t, tc, 9, 3, 0) // Includes a partial coalesced group.
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
	for _, tc := range walLoadCases() {
		b.Run(tc.name, func(b *testing.B) {
			var drain, seed time.Duration
			var requests, points, pending, walEntries float64
			for range b.N {
				r := runWALLoad(b, tc, entries, flows, delay)
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
