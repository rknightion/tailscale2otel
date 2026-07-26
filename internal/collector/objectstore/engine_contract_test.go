package objectstore_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/ingest"
	storeapi "github.com/rknightion/tailscale2otel/v3/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

type contractBackend struct {
	object    storeapi.Object
	body      []byte
	getIDs    []string
	listCalls []listCall
}

func (b *contractBackend) List(
	_ context.Context,
	prefix, startAfter string,
	limit int,
) (storeapi.ListResult, error) {
	b.listCalls = append(b.listCalls, listCall{
		Prefix:     prefix,
		StartAfter: startAfter,
		Limit:      limit,
	})
	return storeapi.ListResult{Objects: []storeapi.Object{b.object}}, nil
}

func (b *contractBackend) Get(_ context.Context, identity string) (io.ReadCloser, error) {
	b.getIDs = append(b.getIDs, identity)
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

type contractSignal struct {
	name      string
	processed int
}

func (s *contractSignal) Signal() string { return s.name }

func (s *contractSignal) PrepareRecord(
	_ context.Context,
	_ []byte,
	_ time.Time,
) (objectstore.PreparedRecord, error) {
	return contractPrepared{s: s}, nil
}

type contractPrepared struct {
	s *contractSignal
}

func (r contractPrepared) Commit(telemetry.Emitter) objectstore.RecordTimestamps {
	r.s.processed++
	return objectstore.RecordTimestamps{
		EventTime:   now.Add(-2 * time.Minute),
		CaptureTime: now.Add(-time.Minute),
	}
}

func TestEngineUsesProviderNeutralIdentityAndSignalContracts(t *testing.T) {
	const (
		identity = "opaque-provider-id/customer-private-object"
		rawFeed  = "https://store.example|customer-bucket|flow"
	)
	key := officialKeyAt(now.Add(-10*time.Minute), ".json")
	backend := &contractBackend{
		object: storeapi.Object{
			Identity: identity,
			Key:      key,
			Size:     int64(len("one record\n")),
		},
		body: []byte("one record\n"),
	}
	signal := &contractSignal{name: "audit"}
	cp := collector.NewMemoryStore()
	rec := telemetrytest.New()
	var logs bytes.Buffer
	var accepted, ingested int

	col, err := objectstore.New(backend, signal, cp, objectstore.Options{
		Prefix: "flow",
		Now:    func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Scope: objectstore.CheckpointScope{
			Tailnet:  "tailnet.example",
			Provider: "testprovider",
			Signal:   "audit",
			Feed:     objectstore.FeedID(rawFeed),
		},
		OnAccepted: func(event ingest.AcceptedEvent) {
			accepted++
			if event.Signal != "audit" {
				t.Errorf("accepted signal = %q, want audit", event.Signal)
			}
		},
		OnIngest: func(source, gotSignal string, records, _ int) {
			ingested++
			if source != "objectstore" || gotSignal != "audit" || records != 1 {
				t.Errorf("OnIngest = (%q,%q,%d), want (objectstore,audit,1)", source, gotSignal, records)
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("second Collect: %v", err)
	}

	if got := backend.getIDs; len(got) != 1 || got[0] != identity {
		t.Fatalf("Get identities = %v, want [%q] exactly once", got, identity)
	}
	if signal.processed != 1 || accepted != 1 || ingested != 1 {
		t.Fatalf("processed=%d accepted=%d ingested=%d, want 1/1/1", signal.processed, accepted, ingested)
	}
	if len(backend.listCalls) < 2 {
		t.Fatalf("list calls = %v, want one per collection", backend.listCalls)
	}

	for _, checkpointKey := range cp.Keys() {
		for _, raw := range []string{identity, key, rawFeed} {
			if strings.Contains(checkpointKey, raw) {
				t.Errorf("checkpoint key %q contains raw provider value %q", checkpointKey, raw)
			}
		}
	}
	for _, name := range rec.MetricNames() {
		for _, point := range rec.MetricPoints(name) {
			for attr, value := range point.Attrs {
				if strings.Contains(attr, identity) || strings.Contains(value, identity) ||
					strings.Contains(attr, key) || strings.Contains(value, key) {
					t.Errorf("metric %s attribute %q=%q exposes object identity", name, attr, value)
				}
			}
		}
	}
	if strings.Contains(logs.String(), identity) || strings.Contains(logs.String(), key) {
		t.Errorf("local logs expose raw object identity: %s", logs.String())
	}
}

func TestNewRejectsSignalScopeMismatch(t *testing.T) {
	_, err := objectstore.New(&contractBackend{}, &contractSignal{name: "audit"},
		collector.NewMemoryStore(), objectstore.Options{
			Scope: objectstore.CheckpointScope{
				Tailnet:  "tailnet.example",
				Provider: "testprovider",
				Signal:   "flow",
				Feed:     objectstore.FeedID("feed"),
			},
		})
	if err == nil || !strings.Contains(err.Error(), "signal") {
		t.Fatalf("New signal mismatch error = %v, want an error naming signal", err)
	}
}

func TestCheckpointScopeIncludesEveryIsolationDimension(t *testing.T) {
	base := objectstore.CheckpointScope{
		Tailnet:  "tailnet-a.example",
		Provider: "s3",
		Signal:   "flow",
		Feed:     objectstore.FeedID("endpoint", "bucket", "prefix"),
	}
	baseNamespace, err := base.Namespace()
	if err != nil {
		t.Fatalf("base Namespace: %v", err)
	}
	variants := []objectstore.CheckpointScope{
		{Tailnet: "tailnet-b.example", Provider: base.Provider, Signal: base.Signal, Feed: base.Feed},
		{Tailnet: base.Tailnet, Provider: "gcs", Signal: base.Signal, Feed: base.Feed},
		{Tailnet: base.Tailnet, Provider: base.Provider, Signal: "audit", Feed: base.Feed},
		{Tailnet: base.Tailnet, Provider: base.Provider, Signal: base.Signal, Feed: objectstore.FeedID("other")},
	}
	for _, variant := range variants {
		got, err := variant.Namespace()
		if err != nil {
			t.Fatalf("variant Namespace: %v", err)
		}
		if got == baseNamespace {
			t.Errorf("variant %+v shares namespace %q with base", variant, got)
		}
	}
}

func TestFeedIDFramesComponentsUnambiguously(t *testing.T) {
	if got, collision := objectstore.FeedID("a\x00b", "c"), objectstore.FeedID("a", "b\x00c"); got == collision {
		t.Fatalf("FeedID component framing collided: %q", got)
	}
}

func TestCollectRejectsMalformedScopedSeenIdentity(t *testing.T) {
	scope := objectstore.CheckpointScope{
		Tailnet:  "tailnet.example",
		Provider: "testprovider",
		Signal:   "audit",
		Feed:     objectstore.FeedID("feed"),
	}
	namespace, err := scope.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	cp := collector.NewMemoryStore()
	if err := cp.Set(namespace+"/seen/not+base64", now); err != nil {
		t.Fatal(err)
	}
	col, err := objectstore.New(
		&contractBackend{object: storeapi.Object{
			Identity: "valid-identity",
			Key:      officialKeyAt(now.Add(-time.Minute), ".json"),
		}},
		&contractSignal{name: "audit"},
		cp,
		objectstore.Options{Scope: scope, Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = col.Collect(context.Background(), telemetrytest.New().Emitter())
	if err == nil || !strings.Contains(err.Error(), "seen") {
		t.Fatalf("Collect error = %v, want malformed seen checkpoint error", err)
	}
}
