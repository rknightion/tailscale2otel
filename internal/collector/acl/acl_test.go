package acl

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeAPI implements the narrow acl api interface for tests.
type fakeAPI struct {
	acl   *tsclient.RawACL
	err   error
	calls int
}

func (f *fakeAPI) PolicyFileRaw(_ context.Context) (*tsclient.RawACL, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.acl, nil
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0, nil)
	if c.Name() != "acl" {
		t.Fatalf("Name() = %q, want acl", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
	c2 := New(&fakeAPI{}, 5*time.Minute, nil)
	if got := c2.DefaultInterval(); got != 5*time.Minute {
		t.Fatalf("DefaultInterval() = %v, want 5m", got)
	}
}

// lastChanged returns the single tailscale.acl.last_changed gauge value.
func lastChanged(t *testing.T, rec *telemetrytest.Recorder) float64 {
	t.Helper()
	pts := rec.MetricPoints("tailscale.acl.last_changed")
	if len(pts) != 1 {
		t.Fatalf("last_changed points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("last_changed kind = %q, want gauge", p.Kind)
	}
	if p.Unit != "s" {
		t.Fatalf("last_changed unit = %q, want s", p.Unit)
	}
	return p.Value
}

func TestLastChangedTracksETag(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}

	// Deterministic, advancing clock.
	clock := time.Unix(1_000_000, 0).UTC()
	now := func() time.Time { return clock }

	c := New(api, 0, now)

	// First observation: record etag, last_changed = now().
	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect#1: %v", err)
	}
	first := lastChanged(t, rec1)
	if first != float64(clock.Unix()) {
		t.Fatalf("first last_changed = %v, want %v", first, clock.Unix())
	}

	// Second poll, SAME etag, later wall clock: value must NOT change.
	clock = clock.Add(time.Hour)
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect#2: %v", err)
	}
	if got := lastChanged(t, rec2); got != first {
		t.Fatalf("unchanged etag changed last_changed: got %v, want %v", got, first)
	}

	// Third poll, CHANGED etag at a new wall clock: value updates to now().
	clock = clock.Add(time.Hour)
	api.acl = &tsclient.RawACL{HuJSON: `{"acls":[{"action":"accept"}]}`, ETag: "etag-2"}
	rec3 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("Collect#3: %v", err)
	}
	if got := lastChanged(t, rec3); got != float64(clock.Unix()) {
		t.Fatalf("changed etag last_changed = %v, want %v", got, clock.Unix())
	}
}

func TestDefaultNowWhenNil(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: "{}", ETag: "e"}}
	c := New(api, 0, nil)
	before := time.Now().Add(-time.Second).Unix()
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	after := time.Now().Add(time.Second).Unix()
	got := lastChanged(t, rec)
	if got < float64(before) || got > float64(after) {
		t.Fatalf("last_changed = %v, want within [%d,%d]", got, before, after)
	}
}

func TestCollectPropagatesError(t *testing.T) {
	api := &fakeAPI{err: context.DeadlineExceeded}
	rec := telemetrytest.New()
	if err := New(api, 0, time.Now).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect: expected error, got nil")
	}
}
