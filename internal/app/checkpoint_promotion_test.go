package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type promotionStore struct {
	collector.CheckpointStore
	reload func(context.Context) error
}

func (s *promotionStore) Reload(ctx context.Context) error { return s.reload(ctx) }

type promotionCollector struct {
	initialize func() error
	collect    func(time.Time) error
}

func (*promotionCollector) Name() string                   { return "flowlogs" }
func (*promotionCollector) DefaultInterval() time.Duration { return time.Minute }
func (*promotionCollector) Lag() time.Duration             { return 0 }
func (c *promotionCollector) InitializeCheckpoints() error { return c.initialize() }
func (c *promotionCollector) CollectWindow(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
	return to, c.collect(from)
}

func TestCoordinatedPromotionReloadsCheckpoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := &promotionStore{CheckpointStore: collector.NewMemoryStore()}
		at := time.Now().Add(-time.Minute)
		reloaded := false
		store.reload = func(context.Context) error { reloaded = true; return store.Set("previous/flowlogs", at) }
		cfg := config.Default()
		cfg.Admin.Enabled = false
		cfg.Coordination.Mode = "kubernetes"
		a := newApp(cfg, "test", discardLogger(), telemetrytest.New().Emitter(), tracenoop.NewTracerProvider().Tracer("test"), func(context.Context) error { return nil }, provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), store, NewAPIStats())
		reg := collector.NewRegistry()
		a.runtimes[0].registry = reg
		initialized, collected := false, false
		reg.RegisterWindow(&promotionCollector{
			initialize: func() error {
				initialized = true
				got, ok := store.Get("flowlogs")
				if !reloaded || !ok || !got.Equal(at) {
					t.Errorf("initialization before shared reload and migration: %v/%v", got, ok)
				}
				return nil
			},
			collect: func(from time.Time) error {
				collected = true
				if !initialized || !from.Equal(at) {
					t.Errorf("scheduler cursor %v, want %v after initialization", from, at)
				}
				cancel()
				return nil
			},
		}, time.Minute, time.Hour, time.Hour)
		if err := a.runPromoted(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if !collected {
			t.Fatal("scheduler never collected")
		}
	})
}

func TestCoordinatedPromotionReloadFailureStartsNothing(t *testing.T) {
	sentinel := errors.New("reload failed")
	store := &promotionStore{CheckpointStore: collector.NewMemoryStore(), reload: func(context.Context) error { return sentinel }}
	// Deliberately no runtime infrastructure: reaching runActive would be a defect.
	a := &App{store: store}
	if err := a.runPromoted(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("promotion error %v", err)
	}
}

func TestCheckpointMigrationRunsOnceOnRunOnce(t *testing.T) {
	store := collector.NewMemoryStore()
	at := time.Unix(1000, 0).UTC()
	if err := store.Set("previous/flowlogs", at); err != nil {
		t.Fatal(err)
	}
	a := appWithWindowRuntimes(store, "example")
	a.cfg = config.Default()
	a.logger = discardLogger()
	if _, ok := store.Get("flowlogs"); ok {
		t.Fatal("construction migrated checkpoint")
	}
	a.RunOnce(context.Background())
	if got, ok := store.Get("flowlogs"); !ok || !got.Equal(at) {
		t.Fatalf("one-shot cursor %v/%v", got, ok)
	}
	if err := store.Delete("flowlogs"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("previous/flowlogs", at); err != nil {
		t.Fatal(err)
	}
	a.RunOnce(context.Background())
	if _, ok := store.Get("flowlogs"); ok {
		t.Fatal("one-shot migration repeated")
	}
}

func TestNewDefersCheckpointKeyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	store, err := collector.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1000, 0).UTC()
	if err := store.Set("previous/flowlogs", at); err != nil {
		t.Fatal(err)
	}
	if flusher, ok := store.(collector.CheckpointFlusher); ok {
		if err := flusher.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example"
	cfg.Tailscale.Auth.Method = "apikey"
	cfg.Tailscale.Auth.APIKey = "synthetic-key"
	cfg.Checkpoint.Store = "file"
	cfg.Checkpoint.FilePath = path
	a, err := New(context.Background(), cfg, "test", discardLogger(), WithTelemetryOverride(func(o telemetry.Options) telemetry.Options {
		o.Protocol = "stdout"
		o.StdoutWriter = discardWriter{}
		return o
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	if _, ok := a.store.Get("flowlogs"); ok {
		t.Fatal("New migrated checkpoint before active lifecycle")
	}
	if _, ok := a.store.Get("previous/flowlogs"); !ok {
		t.Fatal("New removed legacy checkpoint")
	}
	if err := a.initializeCheckpointState(); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.store.Get("flowlogs"); !ok || !got.Equal(at) {
		t.Fatalf("active cursor %v/%v", got, ok)
	}
}
