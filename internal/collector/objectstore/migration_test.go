package objectstore

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	storeapi "github.com/rknightion/tailscale2otel/v4/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

func TestMigrateLegacyStatePreservesEveryDurableRowAndCanonicalWins(t *testing.T) {
	cp := collector.NewMemoryStore()
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	legacyIdentity := "flow/2026/07/24/10:00:00.json"
	// Use Unix formatting directly; keeping the row construction explicit makes
	// this test exercise the historical on-disk format, not its decoder.
	legacyGap := legacyGapPrefix + "pending/2/1784887320/" +
		base64.RawURLEncoding.EncodeToString([]byte(legacyIdentity))
	legacyRows := map[string]time.Time{
		legacyCursorKey:                       at,
		legacySeenPrefix + legacyIdentity:     at.Add(time.Minute),
		legacyScanPrefix + "Zmxvdw/-":         at.Add(2 * time.Minute),
		legacyGap:                             at.Add(3 * time.Minute),
		"unrelated.collector.checkpoint":      at.Add(4 * time.Minute),
		"another/objectstore.flowlogs.cursor": at.Add(5 * time.Minute),
	}
	for key, value := range legacyRows {
		if err := cp.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	scope := CheckpointScope{
		Tailnet:  "tailnet.example",
		Provider: "s3",
		Signal:   "flow",
		Feed:     FeedID("endpoint", "bucket", "flow"),
	}
	namespace, err := scope.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	canonicalCursor := namespace + "/" + cursorKey
	canonicalValue := at.Add(-time.Hour)
	if err := cp.Set(canonicalCursor, canonicalValue); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyState(cp, "", namespace); err != nil {
		t.Fatal(err)
	}

	if got, ok := cp.Get(canonicalCursor); !ok || got != canonicalValue {
		t.Fatalf("canonical cursor = %v, %v; want existing value %v", got, ok, canonicalValue)
	}
	wantSuffixes := []string{
		seenRow(legacyIdentity),
		scanPrefix + "Zmxvdw/-",
		gapPrefix,
	}
	for _, suffix := range wantSuffixes {
		var found bool
		for _, key := range cp.Keys() {
			if strings.HasPrefix(key, namespace+"/") && strings.Contains(key, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no migrated canonical row contains %q", suffix)
		}
	}
	for key := range legacyRows {
		if key == "unrelated.collector.checkpoint" ||
			key == "another/objectstore.flowlogs.cursor" {
			if _, ok := cp.Get(key); !ok {
				t.Errorf("unrelated row %q was removed", key)
			}
			continue
		}
		if _, ok := cp.Get(key); ok {
			t.Errorf("legacy row %q remains after migration", key)
		}
	}

	if err := migrateLegacyState(cp, "", namespace); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
}

func TestMigrateLegacyStateRejectsMalformedDurableStateWithoutDeletingIt(t *testing.T) {
	cp := collector.NewMemoryStore()
	bad := legacyGapPrefix + "not-a-gap"
	if err := cp.Set(bad, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyState(cp, "", "objectstore/v1/dA/s3/flow/feed"); err == nil {
		t.Fatal("migration accepted malformed legacy state")
	}
	if _, ok := cp.Get(bad); !ok {
		t.Fatal("migration deleted malformed legacy state")
	}
}

// emptyBackend serves no objects. New only needs a non-nil backend.
type emptyBackend struct{}

func (emptyBackend) List(context.Context, string, string, int) (storeapi.ListResult, error) {
	return storeapi.ListResult{}, nil
}

func (emptyBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("no objects")
}

// The pre-v1 layout is FLOW-only — its keys are literally objectstore.flowlogs.*
// — so New must not offer the migration to any other signal. An audit collector
// that ran it would adopt flow's cursor, seen set and gaps as its own AND delete
// them out from under the flow collector, silently rewinding or skipping flow
// ingestion depending on which one started first (#288).
func TestNew_LegacyMigrationIsFlowOnly(t *testing.T) {
	cp := collector.NewMemoryStore()
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	if err := cp.Set(legacyCursorKey, at); err != nil {
		t.Fatal(err)
	}

	scope := CheckpointScope{
		Tailnet:  "tailnet.example",
		Provider: "s3",
		Signal:   semconv.IngestSignalAudit,
		Feed:     FeedID("endpoint", "bucket", "audit"),
	}
	namespace, err := scope.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(emptyBackend{}, NewAuditSignal(audit.NewProcessor()), cp, Options{Scope: scope}); err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, ok := cp.Get(legacyCursorKey); !ok {
		t.Error("the flow legacy cursor was consumed by an AUDIT collector")
	}
	if _, ok := cp.Get(namespace + "/" + cursorKey); ok {
		t.Error("the audit namespace adopted the flow legacy cursor as its own")
	}
}
