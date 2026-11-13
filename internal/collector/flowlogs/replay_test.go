package flowlogs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

var _ collector.ReplayWindowCollector = (*Collector)(nil)

func TestReplayOverlapAndDisabledSettings(t *testing.T) {
	store := collector.NewMemoryStore()
	if got := New(&fakeAPI{}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store)).ReplayOverlap(); got != 5*time.Minute {
		t.Fatalf("ReplayOverlap() = %v, want 5m", got)
	}

	for _, tc := range []struct {
		name     string
		overlap  time.Duration
		capacity int
		store    collector.CheckpointStore
	}{
		{name: "zero overlap", capacity: 8, store: store},
		{name: "zero capacity", overlap: time.Minute, store: store},
		{name: "nil store", overlap: time.Minute, capacity: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(&fakeAPI{}, newProcessor(), 0, 0, nil, nil,
				WithReplay(tc.overlap, tc.capacity, tc.store))
			if got := c.ReplayOverlap(); got != 0 {
				t.Fatalf("ReplayOverlap() = %v, want disabled (0)", got)
			}
		})
	}
}

func TestCollectWindow_ReplayStateSuppressesRestartAndKeepsCurrentLateRecord(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := collector.NewMemoryStore()
	first := oneTCPResponse()

	firstCollector := New(&fakeAPI{resp: first}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
	firstRecorder := telemetrytest.New()
	if _, err := firstCollector.CollectWindow(context.Background(), now.Add(-time.Minute), now, firstRecorder.Emitter()); err != nil {
		t.Fatalf("first CollectWindow() error = %v", err)
	}
	if got, want := sumIO(firstRecorder), float64(1800); got != want {
		t.Fatalf("first io total = %v, want %v", got, want)
	}

	keys := store.Keys()
	if len(keys) != 1 {
		t.Fatalf("replay keys = %v, want one digest", keys)
	}
	if !strings.HasPrefix(keys[0], replayKeyPrefix) || len(strings.TrimPrefix(keys[0], replayKeyPrefix)) != 64 {
		t.Fatalf("replay key = %q, want %s plus SHA-256 hex digest", keys[0], replayKeyPrefix)
	}
	for _, raw := range []string{"n-laptop", "100.64.0.1", "100.64.0.2", ":12345", ":443"} {
		if strings.Contains(keys[0], raw) {
			t.Fatalf("replay key %q leaks raw identity fragment %q", keys[0], raw)
		}
	}

	// A fresh collector simulates a process restart. The old record is in the
	// scheduler's overlap, while the appended connection is a current late
	// record that must still be emitted.
	replay := first
	replay.Logs = append([]flowlog.FlowLog(nil), first.Logs...)
	replay.Logs[0].VirtualTraffic = append(replay.Logs[0].VirtualTraffic, flowlog.ConnectionCounts{
		Proto: 6, Src: "100.64.0.1:23456", Dst: "100.64.0.2:443",
		TxPkts: 10, TxBytes: 1000, RxPkts: 8, RxBytes: 800,
	})
	secondCollector := New(&fakeAPI{resp: replay}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
	secondRecorder := telemetrytest.New()
	if _, err := secondCollector.CollectWindow(context.Background(), now, now.Add(time.Minute), secondRecorder.Emitter()); err != nil {
		t.Fatalf("replay CollectWindow() error = %v", err)
	}
	if got, want := sumIO(secondRecorder), float64(1800); got != want {
		t.Fatalf("replay io total = %v, want %v (only current late record)", got, want)
	}

	// Durable replay intentionally stores only an identity digest, so it also
	// suppresses revised counters after a restart rather than inventing a
	// cross-process conflict fingerprint.
	if got := len(secondRecorder.MetricPoints(flowlog.MetricDedupConflicts)); got != 0 {
		t.Fatalf("replay conflict points = %d, want 0 for durable identity suppression", got)
	}
}

func TestReplayStateExpiryIncludesHorizonBoundary(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	requestTo := now.Add(-2 * time.Minute)
	store := collector.NewMemoryStore()
	resp := oneTCPResponse()
	seed := New(&fakeAPI{resp: resp}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
	if _, err := seed.CollectWindow(context.Background(), requestTo.Add(-time.Minute), requestTo, telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("seed CollectWindow() error = %v", err)
	}

	atBoundary := New(&fakeAPI{resp: resp}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return requestTo.Add(5 * time.Minute) }))
	boundaryRecorder := telemetrytest.New()
	if _, err := atBoundary.CollectWindow(context.Background(), requestTo, requestTo.Add(time.Minute), boundaryRecorder.Emitter()); err != nil {
		t.Fatalf("boundary CollectWindow() error = %v", err)
	}
	if got := sumIO(boundaryRecorder); got != 0 {
		t.Fatalf("boundary io total = %v, want 0 (expiry remains valid through replay horizon)", got)
	}

	afterBoundary := New(&fakeAPI{resp: resp}, newProcessor(), 0, 0, nil, nil,
		WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return requestTo.Add(5*time.Minute + time.Nanosecond) }))
	afterRecorder := telemetrytest.New()
	if _, err := afterBoundary.CollectWindow(context.Background(), requestTo.Add(time.Second), requestTo.Add(time.Minute), afterRecorder.Emitter()); err != nil {
		t.Fatalf("post-boundary CollectWindow() error = %v", err)
	}
	if got, want := sumIO(afterRecorder), float64(1800); got != want {
		t.Fatalf("post-boundary io total = %v, want %v (expired identity is accepted)", got, want)
	}
}

func TestReplayStateLoadPrunesCapacityDeterministically(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := collector.NewMemoryStore()
	expired := replayKeyPrefix + strings.Repeat("0", 64)
	keepA := replayKeyPrefix + strings.Repeat("a", 64)
	keepB := replayKeyPrefix + strings.Repeat("b", 64)
	dropC := replayKeyPrefix + strings.Repeat("c", 64)
	for key, expiry := range map[string]time.Time{
		expired: now.Add(-time.Nanosecond),
		keepB:   now.Add(time.Minute),
		dropC:   now.Add(time.Minute),
		keepA:   now.Add(time.Minute),
	} {
		if err := store.Set(key, expiry); err != nil {
			t.Fatalf("seed checkpoint %q: %v", key, err)
		}
	}

	_ = New(&fakeAPI{}, newProcessor(), 0, 0, nil, nil,
		WithReplay(time.Minute, 2, store), withClock(func() time.Time { return now }))
	got := store.Keys()
	if len(got) != 2 || !contains(got, keepA) || !contains(got, keepB) {
		t.Fatalf("replay keys after deterministic prune = %v, want %q and %q", got, keepA, keepB)
	}
}

func TestCollectWindow_ReplayPersistenceFailureDoesNotAdvanceAfterEmission(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	wantErr := errors.New("checkpoint unavailable")
	c := New(&fakeAPI{resp: oneTCPResponse()}, newProcessor(), 0, 0, nil, nil,
		WithReplay(time.Minute, 8, failingStore{err: wantErr}), withClock(func() time.Time { return now }))
	rec := telemetrytest.New()
	hwm, err := c.CollectWindow(context.Background(), now.Add(-time.Minute), now, rec.Emitter())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CollectWindow() error = %v, want %v", err, wantErr)
	}
	if !hwm.IsZero() {
		t.Fatalf("CollectWindow() high-water mark = %v, want zero after persistence failure", hwm)
	}
	points := rec.MetricPoints("tailscale2otel.checkpoint.persist.errors")
	if len(points) != 1 || points[0].Value != 1 || points[0].Attrs["tailscale.collector"] != "flowlogs" {
		t.Fatalf("persist error counter = %+v", points)
	}
	if got, want := sumIO(rec), float64(1800); got != want {
		t.Fatalf("io total = %v, want %v (accepted data remains emitted)", got, want)
	}
}

func contains(keys []string, want string) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

type failingStore struct{ err error }

func (s failingStore) Get(string) (time.Time, bool) { return time.Time{}, false }
func (s failingStore) Set(string, time.Time) error  { return s.err }
func (failingStore) Keys() []string                 { return nil }
func (s failingStore) Delete(string) error          { return s.err }

func TestReplayDigestSurvivesRestartInsideOverlap(t *testing.T) {
	for _, age := range []time.Duration{0, 6 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
			to := now.Add(-2*time.Minute - age)
			store := collector.NewMemoryStore()
			makeCollector := func() *Collector {
				return New(&fakeAPI{resp: oneTCPResponse()}, newProcessor(), 0, 0, nil, nil,
					WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
			}
			first := telemetrytest.New()
			if _, err := makeCollector().CollectWindow(context.Background(), to.Add(-time.Minute), to, first.Emitter()); err != nil {
				t.Fatal(err)
			}
			if got := sumIO(first); got != 1800 {
				t.Fatalf("initial io = %v", got)
			}
			now = now.Add(4 * time.Minute)
			next := telemetrytest.New()
			if _, err := makeCollector().CollectWindow(context.Background(), to.Add(-5*time.Minute), to.Add(4*time.Minute), next.Emitter()); err != nil {
				t.Fatal(err)
			}
			if got := sumIO(next); got != 0 {
				t.Fatalf("restarted collector replayed %v bytes", got)
			}
		})
	}
}

func TestReplayDigestRetiredByWindowPosition(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	to := now.Add(-time.Hour)
	for _, offset := range []time.Duration{0, 700 * time.Millisecond, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			store := collector.NewMemoryStore()
			seed := New(&fakeAPI{resp: oneTCPResponse()}, newProcessor(), 0, 0, nil, nil, WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
			if _, err := seed.CollectWindow(context.Background(), to.Add(-time.Minute), to, telemetrytest.New().Emitter()); err != nil {
				t.Fatal(err)
			}
			next := New(&fakeAPI{resp: oneTCPResponse()}, newProcessor(), 0, 0, nil, nil, WithReplay(5*time.Minute, 8, store), withClock(func() time.Time { return now }))
			rec := telemetrytest.New()
			if _, err := next.CollectWindow(context.Background(), to.Add(offset), to.Add(time.Minute), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			want := float64(0)
			if offset >= time.Second {
				want = 1800
			}
			if got := sumIO(rec); got != want {
				t.Fatalf("io = %v, want %v", got, want)
			}
		})
	}
}

func TestReplayNoDuplicatesAboveDedupCapacity(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	api := &fakeAPI{resp: oneTCPResponse()}
	extra := api.resp.Logs[0].VirtualTraffic[0]
	extra.Src = "100.64.0.1:23456"
	api.resp.Logs[0].VirtualTraffic = append(api.resp.Logs[0].VirtualTraffic, extra)
	c := New(api, newProcessor(), 0, 0, nil, nil, WithReplay(5*time.Minute, 8, collector.NewMemoryStore()), WithDedupCapacity(1), withClock(func() time.Time { return now }))
	rec := telemetrytest.New()
	to := now.Add(-2 * time.Minute)
	for i := 0; i < 7; i++ {
		if _, err := c.CollectWindow(context.Background(), to.Add(-5*time.Minute), now.Add(-2*time.Minute), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		to = now.Add(-2 * time.Minute)
		now = now.Add(time.Minute)
	}
	if got := sumIO(rec); got != 3600 {
		t.Fatalf("io = %v, want two distinct connections (3600)", got)
	}
}
