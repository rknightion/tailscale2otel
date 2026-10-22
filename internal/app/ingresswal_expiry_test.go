package app

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

func appendTestEnvelope(t *testing.T, store *ingresswal.Store, accepted time.Time, tailnet, source, signal, body string) {
	t.Helper()
	id, err := ingresswal.NewID(tailnet, source, signal, []byte(body))
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if err := store.Append(context.Background(), ingresswal.Envelope{
		ID: id, Tailnet: tailnet, Source: source, Signal: signal, Accepted: accepted.UTC(), Body: []byte(body),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestIngressWALCoordinator_ExpireStaleAccountsPerRoute pins that the
// coordinator retires only stale entries for configured routes and reports
// each one against the route's own delivery provider.
func TestIngressWALCoordinator_ExpireStaleAccountsPerRoute(t *testing.T) {
	store, err := ingresswal.New(ingresswal.Options{
		Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 16,
	})
	if err != nil {
		t.Fatalf("ingresswal.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	appendTestEnvelope(t, store, now.Add(-10*24*time.Hour), "example.com", ingressWALSourceStream, ingressWALSignalHEC, "stale-stream")
	appendTestEnvelope(t, store, now.Add(-9*24*time.Hour), "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, "stale-webhook")
	appendTestEnvelope(t, store, now.Add(-time.Minute), "example.com", ingressWALSourceStream, ingressWALSignalHEC, "fresh")

	stream := testIngressRoute(t, "example.com", ingressWALSourceStream, ingressWALSignalHEC)
	webhook := testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	coordinator, err := newIngressWALCoordinator(store, []ingressWALRoute{stream, webhook})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	type account struct {
		provider       *telemetry.Provider
		source, signal string
	}
	var got []account
	entries, bytesRetired, err := coordinator.ExpireStale(context.Background(), now.Add(-6*time.Hour),
		func(p *telemetry.Provider, e ingresswal.ExpiredEntry) {
			got = append(got, account{provider: p, source: e.Source, signal: e.Signal})
		})
	if err != nil {
		t.Fatalf("ExpireStale: %v", err)
	}
	if entries != 2 || bytesRetired <= 0 {
		t.Fatalf("ExpireStale = %d entries, %d bytes; want 2 entries and their bytes", entries, bytesRetired)
	}
	if len(got) != 2 || got[0].provider != stream.delivery || got[0].source != ingressWALSourceStream ||
		got[1].provider != webhook.delivery || got[1].signal != ingressWALSignalWebhook {
		t.Fatalf("accounting = %+v, want one per entry on its own route provider", got)
	}
	if pending := store.Health().PendingEntries; pending != 1 {
		t.Fatalf("pending after expiry = %d, want the fresh entry only", pending)
	}

	// Disabled policy (zero cutoff) retires nothing.
	if n, _, err := coordinator.ExpireStale(context.Background(), time.Time{}, nil); err != nil || n != 0 {
		t.Fatalf("zero cutoff ExpireStale = %d, %v; want 0, nil", n, err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestStartIngressWAL_ExpiresStaleBacklogBeforeReplay is the regression test
// for the lab failover: a former leader promoted onto a backlog accepted ten
// days earlier replayed it, the backend rejected every record as too old, and
// the loss advisory fired. The stale backlog must be retired before replay,
// with one WARN line, and must never reach the route.
func TestStartIngressWAL_ExpiresStaleBacklogBeforeReplay(t *testing.T) {
	store, err := ingresswal.New(ingresswal.Options{
		Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 16,
	})
	if err != nil {
		t.Fatalf("ingresswal.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	appendTestEnvelope(t, store, time.Now().Add(-10*24*time.Hour), "example.com", ingressWALSourceStream, ingressWALSignalHEC, "stale")

	delivery, _ := newTestIngressDelivery(t, testIngressInterval)
	applied := 0
	route := testIngressRouteOn("example.com", ingressWALSourceStream, ingressWALSignalHEC, delivery,
		func(context.Context, []byte, time.Time, telemetry.Emitter) error { applied++; return nil })
	coordinator, err := newIngressWALCoordinator(store, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	logs := &syncBuffer{}
	cfg := config.Default()
	cfg.SelfObservability.Enabled = false
	a := &App{
		cfg:                cfg,
		logger:             slog.New(slog.NewTextHandler(logs, nil)),
		readyState:         newComponentHealth(),
		ingressWAL:         coordinator,
		walPromotionBudget: 2 * time.Second,
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	walCtx, walCancel := context.WithCancel(context.Background())
	defer walCancel()

	done, open := a.startIngressWAL(ctx, walCtx)
	waitClosed(t, open, 5*time.Second, "receiver gate")
	walCancel()
	<-done

	if applied != 0 {
		t.Fatalf("stale entry was replayed %d time(s), want it retired without replay", applied)
	}
	if pending := store.Health().PendingEntries; pending != 0 {
		t.Fatalf("pending after startup = %d, want 0", pending)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "expired") || !strings.Contains(out, "entries=1") {
		t.Fatalf("startup logs missing the expiry WARN:\n%s", out)
	}
}
