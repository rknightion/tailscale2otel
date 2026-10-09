package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/annotations"
	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
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
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Default()
		cfg.Tailscale.Tailnet = "-"
		cfg.Cardinality.Flow.MetricsMode = "all"
		cfg.Streaming.Enabled = true
		cfg.Streaming.Listen = "127.0.0.1:0"
		cfg.Streaming.Token = "token"
		cfg.IngressWAL.Enabled = true

		delivery, sink := newTestIngressDelivery(t, testIngressInterval)
		a := newAppShell(
			cfg,
			"vtest",
			nil,
			delivery.Emitter(),
			nil,
			func(context.Context) error { return nil },
			collector.NewMemoryStore(),
		)
		a.buildProcessDeps()
		a.addRuntimeConfigured(
			"-",
			"resolved.example.com",
			delivery.Emitter(),
			nil,
			nil,
			delivery,
			provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
			false,
		)

		routes := a.buildReceivers()
		wal := newCoordinatorWAL(t)
		coordinator, err := newIngressWALCoordinator(wal, routes)
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		a.ingressWAL = coordinator

		body := `{"event":{"nodeId":"n1","start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
		req := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, strings.NewReader(body))
		req.SetBasicAuth("", "token")
		w := httptest.NewRecorder()
		a.streamSrv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("HEC status = %d, want 200", w.Code)
		}
		// A normal slot elapses with no replay: the accepted body has no effects.
		advanceIngressSlot()
		if got := cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sink.Bytes()), flowlog.MetricIO); got != 0 {
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
		if got := cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sink.Bytes()), flowlog.MetricIO); got == 0 {
			t.Fatal("replay emitted no flow telemetry")
		}
	})
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

func TestIngressWALMultiTailnetDeliversOnlyMatchingRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := config.Default()
		cfg.Cardinality.Flow.MetricsMode = "all"
		cfg.Streaming.Enabled = true
		cfg.Streaming.Listen = "127.0.0.1:0"
		cfg.Streaming.Routes = []config.StreamingRoute{
			{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a"},
			{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b"},
		}
		cfg.IngressWAL.Enabled = true

		deliveryA, sinkA := newTestIngressDelivery(t, testIngressInterval)
		deliveryB, sinkB := newTestIngressDelivery(t, testIngressInterval)
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
		a.addRuntimeConfigured(
			"acme.example.com",
			"acme.example.com",
			deliveryA.Emitter(),
			nil,
			nil,
			deliveryA,
			provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
			true,
		)
		a.addRuntimeConfigured(
			"beta.example.com",
			"beta.example.com",
			deliveryB.Emitter(),
			nil,
			nil,
			deliveryB,
			provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")),
			true,
		)
		routes := a.buildReceivers()
		wal := newCoordinatorWAL(t)
		coordinator, err := newIngressWALCoordinator(wal, routes)
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		a.ingressWAL = coordinator

		body := `{"event":{"nodeId":"n-beta","start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":10,"rxBytes":20}]}}`
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
		if a, b := deliveryA.CollectionStats().RequiredSnapshotsAcknowledged, deliveryB.CollectionStats().RequiredSnapshotsAcknowledged; a != 0 || b != 1 {
			t.Fatalf("required first covers acme/beta = %d/%d, want 0/1", a, b)
		}
		if got := cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sinkA.Bytes()), flowlog.MetricIO); got != 0 {
			t.Fatalf("beta replay leaked %v flow telemetry into acme", got)
		}
		if got := cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sinkB.Bytes()), flowlog.MetricIO); got == 0 {
			t.Fatal("beta replay emitted no beta flow telemetry")
		}
	})
}

// parkedIngressRoute holds the startup replay inside its first preparation
// until release closes; its provider uses a short real-time slot so the
// released original is covered, acknowledged and committed promptly.
func parkedIngressRoute(t *testing.T, started, release chan struct{}) ingressWALRoute {
	t.Helper()
	delivery, _ := newTestIngressDelivery(t, 10*time.Millisecond)
	route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery, nil)
	prepare := route.prepare
	var once sync.Once
	route.prepare = func(ctx context.Context, body []byte, accepted time.Time) (telemetry.IngressWork, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return telemetry.IngressWork{}, ctx.Err()
		}
		return prepare(ctx, body, accepted)
	}
	return route
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
	wal := newCoordinatorWAL(t, envelope)
	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	route := parkedIngressRoute(t, flushStarted, releaseFlush)
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

// TestAppRunEmitsTelemetryWhileIngressWALReplays is the regression test for the
// outage itself. The startup drain used to run inline, ahead of the heartbeat
// and every self-obs reporter, so a leader promoted onto a large inherited
// backlog emitted NOTHING for the whole drain — the exporter looked dead, the
// Exporter-down rule fired on NoData, and the one gauge that explains a slow
// drain was gated behind the drain it describes.
//
// It shares TestAppRunReplaysIngressWALBeforeStartingReceivers' shape: the
// replay is parked inside the route preparation for the whole assertion window, so
// anything observed here was emitted DURING the drain, not after it.
func TestAppRunEmitsTelemetryWhileIngressWALReplays(t *testing.T) {
	cfg := config.Default()
	if !cfg.SelfObservability.Enabled {
		t.Fatal("self-observability is off by default; this test asserts what it emits")
	}
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

	envelope := coordinatorEnvelope(
		t,
		"example.com",
		ingressWALSourceWebhook,
		ingressWALSignalWebhook,
		[]byte(`[]`),
	)
	wal := newCoordinatorWAL(t, envelope)
	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	route := parkedIngressRoute(t, flushStarted, releaseFlush)
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	a.ingressWAL = coordinator

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(ctx) }()
	defer func() {
		select {
		case <-releaseFlush:
		default:
			close(releaseFlush)
		}
		cancel()
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

	// The drain is parked. Both of these are emitted immediately on start by
	// their reporters, so a short poll is enough and nothing here depends on a
	// tick interval.
	want := []string{appcatalog.MetricUp, appcatalog.DocIngressWALPendingEntries.Name}
	deadline := time.Now().Add(2 * time.Second)
	for _, name := range want {
		for len(rec.MetricPoints(name)) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("%s was not emitted while the ingress WAL was still replaying; "+
					"a promoted leader is dark for the whole drain (emitted: %v)",
					name, rec.MetricNames())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	select {
	case <-releaseFlush:
		t.Fatal("the replay finished before the assertions; they proved nothing")
	default:
	}
}

// TestIngressWALDurableAuditReachesAnnotationTee is the regression test for
// durable WAL application bypassing the runtime's annotation tee. With
// ingress_wal on, a streamed configuration audit record must still produce its
// Grafana annotation when the original is applied, as the synchronous receiver
// path does, while its metrics still wait for the normal scheduled collection.
func TestIngressWALDurableAuditReachesAnnotationTee(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		posts = append(posts, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"message":"Annotation added"}`))
	}))
	defer grafana.Close()
	annotationPosts := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, p := range posts {
			if !strings.Contains(p, "started") {
				out = append(out, p)
			}
		}
		return out
	}

	delivery, _ := newTestIngressDelivery(t, 20*time.Millisecond)
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Listen = "127.0.0.1:0"
	cfg.Streaming.Token = "token"
	cfg.IngressWAL.Enabled = true
	a := newAppShell(cfg, "vtest", nil, delivery.Emitter(), nil, func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	annotator, err := annotations.Start(context.Background(), annotations.Options{
		Config: annotations.Config{
			Client:       annotations.ClientConfig{URL: grafana.URL, Token: "synthetic", Timeout: 2 * time.Second},
			Categories:   map[annotations.Category]annotations.CategoryConfig{annotations.CategoryConfigChange: {Enabled: true}},
			QueueSize:    16,
			MaxPerMinute: 600,
		},
		Emitter: delivery.Emitter(),
	})
	if err != nil {
		t.Fatalf("annotations.Start: %v", err)
	}
	a.annotator = annotator
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = annotator.Close(ctx)
	}()
	a.addRuntimeConfigured("example.com", "example.com", a.annotator.Decorate("example.com", delivery.Emitter()), nil, nil, delivery,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), false)
	coordinator, err := newIngressWALCoordinator(newCoordinatorWAL(t), a.buildReceivers())
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	a.ingressWAL = coordinator

	body := `{"event":{"actor":{"loginName":"synthetic@example.com","type":"USER"},"action":"CREATE","eventGroupID":"g-annotation","origin":"ADMIN_CONSOLE","target":{"id":"n-annotation","name":"node-annotation","type":"NODE"},"eventTime":"2026-07-26T10:00:00Z"}}`
	req := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, strings.NewReader(body))
	req.SetBasicAuth("", "token")
	w := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HEC status = %d, want 200", w.Code)
	}
	time.Sleep(200 * time.Millisecond)
	if got := annotationPosts(); len(got) != 0 {
		t.Fatalf("annotation published before the durable original was applied: %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := coordinator.Replay(ctx); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(annotationPosts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := annotationPosts()
	if len(got) != 1 || !strings.Contains(got[0], "device") {
		t.Fatalf("annotations after durable application = %v, want exactly one device-change annotation", got)
	}
}

// TestRuntimeProducerRegistrationFailureIsAConstructionError pins that a
// tailnet provider which cannot take the runtime's scheduled rollup producer
// fails construction instead of degrading silently. Without the hook nothing
// drains the active rollup accumulator on the schedule (there is no
// independent ticker any more), so rollup metrics would only appear at
// shutdown, and with ingress_wal on the durable routes would have no delivery.
func TestRuntimeProducerRegistrationFailureIsAConstructionError(t *testing.T) {
	for _, walEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("ingress_wal=%v", walEnabled), func(t *testing.T) {
			cfg := config.Default()
			cfg.Tailscale.Tailnet = "example.com"
			cfg.Streaming.Enabled = true
			cfg.Streaming.Listen = "127.0.0.1:0"
			cfg.Streaming.Token = "token"
			cfg.IngressWAL.Enabled = walEnabled
			cfg.IngressWAL.Directory = t.TempDir()
			delivery, _ := newTestIngressDelivery(t, testIngressInterval)
			// Another producer already owns this provider's collection hook.
			if err := delivery.SetBeforeCollect(func() telemetry.CollectionStage { return nil }); err != nil {
				t.Fatal(err)
			}
			a := newAppShell(cfg, "vtest", nil, delivery.Emitter(), nil, func(context.Context) error { return nil }, collector.NewMemoryStore())
			a.buildProcessDeps()
			a.addRuntimeConfigured("example.com", "example.com", delivery.Emitter(), nil, nil, delivery,
				provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), false)
			err := a.buildIngressWAL(a.buildReceivers())
			if err == nil {
				t.Fatal("construction succeeded although the runtime's rollup producer could not be registered")
			}
			if a.ingressWAL != nil {
				_ = a.ingressWAL.Close()
			}
		})
	}
}
