package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type lifecycleReceiver struct {
	started chan struct{}
}

func (r *lifecycleReceiver) Handler() http.Handler { return http.NotFoundHandler() }

func (r *lifecycleReceiver) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestIngressWALReceiverUsesConfiguredIdentityAndDefersEffects(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "-"
	cfg.Cardinality.Flow.MetricsMode = "all"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Token = "token"
	cfg.IngressWAL.Enabled = true

	rec := telemetrytest.New()
	a := newAppShell(
		cfg,
		"vtest",
		nil,
		rec.Emitter(),
		nil,
		func(context.Context) error { return nil },
		collector.NewMemoryStore(),
	)
	a.buildProcessDeps()
	a.addRuntimeConfigured(
		"-",
		"resolved.example.com",
		rec.Emitter(),
		nil,
		nil,
		func(context.Context) error { return nil },
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		false,
	)

	routes := a.buildReceivers()
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, routes)
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	a.ingressWAL = coordinator

	body := `{"event":{"nodeId":"n1","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
	req := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, strings.NewReader(body))
	req.SetBasicAuth("", "token")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HEC status = %d, want 200", w.Code)
	}
	if got := metricTotal(rec, flowlog.MetricIO); got != 0 {
		t.Fatalf("flow telemetry before replay = %v, want 0", got)
	}
	if len(wal.appendCalls) != 1 {
		t.Fatalf("WAL append calls = %d, want 1", len(wal.appendCalls))
	}
	if got := wal.appendCalls[0].Tailnet; got != "-" {
		t.Fatalf("persisted tailnet = %q, want configured sentinel %q", got, "-")
	}

	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := metricTotal(rec, flowlog.MetricIO); got == 0 {
		t.Fatal("replay emitted no flow telemetry")
	}
}

func TestIngressWALDisabledKeepsSynchronousReceiverPath(t *testing.T) {
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "all"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Token = "token"

	rec := telemetrytest.New()
	a := newAppShell(
		cfg,
		"vtest",
		nil,
		rec.Emitter(),
		nil,
		func(context.Context) error { return nil },
		collector.NewMemoryStore(),
	)
	a.buildProcessDeps()
	a.addRuntime(
		"example.com",
		rec.Emitter(),
		nil,
		nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		false,
	)
	if routes := a.buildReceivers(); len(routes) != 0 {
		t.Fatalf("disabled WAL routes = %d, want 0", len(routes))
	}

	body := `{"event":{"nodeId":"n1","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
	req := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, strings.NewReader(body))
	req.SetBasicAuth("", "token")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HEC status = %d, want 200", w.Code)
	}
	if got := metricTotal(rec, flowlog.MetricIO); got == 0 {
		t.Fatal("disabled WAL changed synchronous receiver effects")
	}
}

func TestBuildIngressWALDisabledDoesNotTouchConfiguredDirectory(t *testing.T) {
	cfg := config.Default()
	cfg.IngressWAL.Directory = filepath.Join(t.TempDir(), "must-not-exist")
	a := &App{cfg: cfg}

	if err := a.buildIngressWAL(nil); err != nil {
		t.Fatalf("buildIngressWAL: %v", err)
	}
	if a.ingressWAL == nil ||
		a.ingressWAL.Health().State != ingressWALStateDisabled {
		t.Fatalf("disabled coordinator = %#v", a.ingressWAL)
	}
	if _, err := os.Stat(cfg.IngressWAL.Directory); !os.IsNotExist(err) {
		t.Fatalf("disabled WAL touched %q: %v", cfg.IngressWAL.Directory, err)
	}
}

func TestIngressWALMultiTailnetFlushesOnlyMatchingRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "all"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Routes = []config.StreamingRoute{
		{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a"},
		{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b"},
	}
	cfg.IngressWAL.Enabled = true

	recA, recB := telemetrytest.New(), telemetrytest.New()
	a := newAppShell(
		cfg,
		"vtest",
		nil,
		telemetrytest.New().Emitter(),
		nil,
		func(context.Context) error { return nil },
		collector.NewMemoryStore(),
	)
	a.buildProcessDeps()
	flushA, flushB := 0, 0
	a.addRuntimeConfigured(
		"acme.example.com",
		"acme.example.com",
		recA.Emitter(),
		nil,
		nil,
		func(context.Context) error { flushA++; return nil },
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		true,
	)
	a.addRuntimeConfigured(
		"beta.example.com",
		"beta.example.com",
		recB.Emitter(),
		nil,
		nil,
		func(context.Context) error { flushB++; return nil },
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		true,
	)
	routes := a.buildReceivers()
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, routes)
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	a.ingressWAL = coordinator

	body := `{"event":{"nodeId":"n-beta","start":"2026-07-26T10:00:00Z","end":"2026-07-26T10:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
	req := httptest.NewRequest(http.MethodPost, "/hec/beta", strings.NewReader(body))
	req.SetBasicAuth("", "token-b")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("beta HEC status = %d, want 200", w.Code)
	}
	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if flushA != 0 || flushB != 1 {
		t.Fatalf("force flushes acme/beta = %d/%d, want 0/1", flushA, flushB)
	}
	if got := metricTotal(recA, flowlog.MetricIO); got != 0 {
		t.Fatalf("beta replay leaked %v flow telemetry into acme", got)
	}
	if got := metricTotal(recB, flowlog.MetricIO); got == 0 {
		t.Fatal("beta replay emitted no beta flow telemetry")
	}
}

func TestAppRunReplaysIngressWALBeforeStartingReceivers(t *testing.T) {
	cfg := config.Default()
	rec := telemetrytest.New()
	a := newApp(
		cfg,
		"vtest",
		nil,
		rec.Emitter(),
		nil,
		func(context.Context) error { return nil },
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
		collector.NewMemoryStore(),
		NewAPIStats(),
	)
	receiver := &lifecycleReceiver{started: make(chan struct{})}
	a.streamSrv = receiver

	envelope := coordinatorEnvelope(
		t,
		"example.com",
		ingressWALSourceWebhook,
		ingressWALSignalWebhook,
		[]byte(`[]`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	route := testIngressRoute(
		"example.com",
		ingressWALSourceWebhook,
		ingressWALSignalWebhook,
	)
	route.flush = func(context.Context) error {
		close(flushStarted)
		<-releaseFlush
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	a.ingressWAL = coordinator

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	runFinished := false
	go func() { runDone <- a.Run(ctx) }()
	defer func() {
		select {
		case <-releaseFlush:
		default:
			close(releaseFlush)
		}
		cancel()
		if runFinished {
			return
		}
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
		}
	}()

	select {
	case <-flushStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("startup replay did not begin")
	}
	select {
	case <-receiver.started:
		t.Fatal("receiver started before startup WAL replay completed")
	default:
	}

	close(releaseFlush)
	select {
	case <-receiver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("receiver did not start after startup WAL replay completed")
	}
	cancel()
	select {
	case err := <-runDone:
		runFinished = true
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
