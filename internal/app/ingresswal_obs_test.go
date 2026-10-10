package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// This test uses the initialization-time config mapping, real OTLP HTTP SDK
// exporters, scheduled reader, disk WAL and live replay worker. No direct
// reader manipulation or test-only Options mapping can retire the disk entry.
func TestIngressWALPermanentDropRuntime(t *testing.T) {
	for _, signal := range []string{"metrics", "logs"} {
		t.Run(signal, func(t *testing.T) {
			var healthy atomic.Bool
			var accepted atomic.Int64
			var mu sync.Mutex
			var firstTimestamp uint64
			var rejected int
			var loss float64
			var lossTailnet string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				reject := false
				next := false
				if strings.HasSuffix(r.URL.Path, "/metrics") {
					var req metrics.ExportMetricsServiceRequest
					if err := proto.Unmarshal(body, &req); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					for _, rm := range req.ResourceMetrics {
						for _, sm := range rm.ScopeMetrics {
							for _, m := range sm.Metrics {
								if m.Name == "fixture.permanent" {
									for _, dp := range m.GetGauge().GetDataPoints() {
										if signal == "metrics" && dp.GetAsDouble() == 8 {
											if firstTimestamp == 0 {
												firstTimestamp = dp.TimeUnixNano
											}
											reject = dp.TimeUnixNano == firstTimestamp
										}
										if dp.GetAsDouble() == 21 {
											next = true
										}
									}
								}
								if m.Name == appcatalog.MetricIngressWALPermanentDrops {
									for _, dp := range m.GetSum().GetDataPoints() {
										var gotSignal, tailnet string
										for _, a := range dp.Attributes {
											if a.Key == semconv.AttrIngestSignal {
												gotSignal = a.Value.GetStringValue()
											}
											if a.Key == semconv.AttrTailnet {
												tailnet = a.Value.GetStringValue()
											}
										}
										if gotSignal == signal {
											loss = dp.GetAsDouble()
											lossTailnet = tailnet
										}
									}
								}
							}
						}
					}
				} else if strings.HasSuffix(r.URL.Path, "/logs") {
					var req logs.ExportLogsServiceRequest
					if err := proto.Unmarshal(body, &req); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					for _, rl := range req.ResourceLogs {
						for _, sl := range rl.ScopeLogs {
							for _, log := range sl.LogRecords {
								if signal == "logs" && log.Body.GetStringValue() == "rejected" {
									reject = true
								}
								if log.Body.GetStringValue() == "next healthy original" {
									next = true
								}
							}
						}
					}
				}
				if reject && !healthy.Load() {
					rejected++
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/"+signal) && next {
					accepted.Add(1)
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer server.Close()
			cfg := config.Default()
			cfg.OTLP.Protocol = "http"
			cfg.OTLP.Endpoint = server.URL
			cfg.OTLP.TLS.Insecure = true
			cfg.OTLP.MetricInterval = config.Duration(500 * time.Millisecond)
			// Nondefault N=2 must reach the real reader via telemetryOptions.
			cfg.IngressWAL.MaxPermanentRejections = 2
			cfg.IngressWAL.Enabled = true
			cfg.IngressWAL.Directory = t.TempDir()
			cfg.IngressWAL.MaxEntries = 1
			set, err := telemetry.NewProviderSet(context.Background(), telemetryOptions(cfg, "fixture"), []telemetry.PerTailnetOptions{{Name: "example.com", InstanceID: "fixture-tailnet"}})
			if err != nil {
				t.Fatal(err)
			}
			p := set.Tailnet("example.com")
			defer func() {
				healthy.Store(true)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = set.Shutdown(ctx)
			}()
			a := &App{cfg: cfg}
			route := ingressWALRoute{tailnet: "example.com", source: ingressWALSourceWebhook, signal: ingressWALSignalWebhook, delivery: p,
				prepare: func(_ context.Context, body []byte, _ time.Time) (telemetry.IngressWork, error) {
					return telemetry.IngressWork{Bounds: telemetry.WorkBounds{InputBytes: int64(len(body)), MetricOps: 1, MetricBytes: 4096, Logs: telemetry.LogBounds{Records: 1, Bytes: 4096}}, Apply: func(ctx context.Context, e telemetry.Emitter) error {
						e.Gauge("fixture.permanent", "1", "fixture", float64(len(body)), nil)
						if signal == "logs" {
							e.LogEventCtx(ctx, telemetry.Event{Name: "fixture.permanent", Body: string(body)})
						}
						return nil
					}}, nil
				}}
			if err := a.buildIngressWAL([]ingressWALRoute{route}); err != nil {
				t.Fatal(err)
			}
			defer a.ingressWAL.Close()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- a.ingressWAL.Run(ctx) }()
			defer func() { cancel(); <-done }()
			appendBody := a.ingressWAL.appender(route.tailnet, route.source, route.signal)
			if err := appendBody(ctx, []byte("rejected"), time.Now()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(4 * time.Second)
			for a.ingressWAL.Health().WAL.PendingEntries != 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := a.ingressWAL.Health().WAL.PendingEntries; n != 0 {
				t.Fatalf("permanent rejection blocked real disk WAL: pending=%d", n)
			}
			mu.Lock()
			if rejected != 2 {
				t.Errorf("rejections before WAL retirement=%d, want nondefault N=2", rejected)
			}
			mu.Unlock()
			stats := p.CollectionStats()
			if signal == "metrics" && stats.PermanentMetricsDropped != 1 {
				t.Errorf("metric drop units=%d", stats.PermanentMetricsDropped)
			}
			if signal == "logs" && stats.PermanentLogBatchesDropped != 1 {
				t.Errorf("log drop units=%d", stats.PermanentLogBatchesDropped)
			}
			healthy.Store(true)
			if err := appendBody(ctx, []byte("next healthy original"), time.Now()); err != nil {
				t.Fatalf("new append after drop: %v", err)
			}
			deadline = time.Now().Add(3 * time.Second)
			for a.ingressWAL.Health().WAL.PendingEntries != 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if a.ingressWAL.Health().WAL.PendingEntries != 0 || accepted.Load() == 0 {
				t.Fatal("next healthy original did not export and retire")
			}
			// Logs can retire before the next metrics slot ships the loss counter.
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				mu.Lock()
				observed := loss == 1 && lossTailnet == "example.com"
				mu.Unlock()
				if observed {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			if loss != 1 || lossTailnet != "example.com" {
				t.Fatalf("exported loss=%v tailnet=%q, want one monotonic unit with standard tailnet attribute", loss, lossTailnet)
			}
		})
	}
}

func TestEmitIngressWALHealthUsesProcessGlobalAttributeFreeGauges(t *testing.T) {
	rec := telemetrytest.New()
	wal := &coordinatorWAL{}
	wal.pending = []ingresswal.Envelope{
		{Body: []byte("one")},
		{Body: []byte("three")},
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	emitIngressWALHealth(rec.Emitter(), coordinator)

	for name, want := range map[string]float64{
		appcatalog.MetricIngressWALPendingEntries:     2,
		appcatalog.MetricIngressWALPendingSize:        8,
		appcatalog.MetricIngressWALPendingEntriesFill: 0.02,
		appcatalog.MetricIngressWALPendingSizeFill:    8.0 / (1 << 20),
		appcatalog.MetricIngressWALOrphanStages:       0,
		appcatalog.MetricIngressWALOrphanSize:         0,
		appcatalog.MetricIngressWALCompletionMarkers:  0,
	} {
		points := rec.MetricPoints(name)
		if len(points) != 1 {
			t.Fatalf("%s points = %d, want 1", name, len(points))
		}
		if points[0].Value != want {
			t.Errorf("%s value = %v, want %v", name, points[0].Value, want)
		}
		if len(points[0].Attrs) != 0 {
			t.Errorf("%s attrs = %v, want none", name, points[0].Attrs)
		}
	}
}
