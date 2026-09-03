package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKubernetesCheckpointStore_ShardsCompressesAndRestoresState(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatalf("NewKubernetesCheckpointStore: %v", err)
	}
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	ordinary := "tailnet/flowlogs"
	replay := "tailnet/flowlogs/replay/seen/opaque"
	namespace := "objectstore/v1/dGFpbG5ldA/s3/flow/feed"
	seen := namespace + "/seen/opaque"
	gap := namespace + "/gap/opaque"
	for key := range map[string]struct{}{ordinary: {}, replay: {}, seen: {}, gap: {}} {
		if err := store.Set(key, at); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}
	if got := client.shardCount(); got != 2 {
		t.Fatalf("shard count = %d, want 2", got)
	}
	for _, object := range client.objects() {
		if len(object.Data) == 0 || object.Data[0] == '{' {
			t.Fatalf("shard %q stored non-gzip payload", object.Shard)
		}
		shard, rows, err := decodeKubernetesCheckpoint(object.Data)
		if err != nil {
			t.Fatalf("decode shard %q: %v", object.Shard, err)
		}
		if shard != object.Shard {
			t.Fatalf("payload shard = %q, want %q", shard, object.Shard)
		}
		if len(rows) == 0 {
			t.Fatalf("shard %q lost rows", object.Shard)
		}
	}
	reopened, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for _, key := range []string{ordinary, replay, seen, gap} {
		if got, ok := reopened.Get(key); !ok || !got.Equal(at) {
			t.Fatalf("reopened %q = %v/%v, want %v", key, got, ok, at)
		}
	}
}

func TestKubernetesCheckpointStore_RejectsRowOwnedByAnotherShard(t *testing.T) {
	encoded, _, err := EncodeKubernetesCheckpointShard("flowlogs", map[string]time.Time{
		"auditlogs": time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{shards: map[string]KubernetesCheckpointObject{
		"flowlogs": {Shard: "flowlogs", ResourceVersion: "1", Data: encoded},
	}}
	if _, err := NewKubernetesCheckpointStore(context.Background(), client); err == nil || !strings.Contains(err.Error(), "owned by shard") {
		t.Fatalf("open mis-owned row = %v, want fail-closed ownership error", err)
	}
}

func TestKubernetesCheckpointStore_MigratesLegacySingleConfigMap(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	legacy, err := json.Marshal(map[string]time.Time{"flowlogs": at, "objectstore/v1/dA/s3/audit/feed/seen/a": at})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{legacy: KubernetesCheckpointObject{Data: legacy}, legacyExists: true}
	store, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	if client.shardCount() != 2 {
		t.Fatalf("migration shard count = %d, want 2", client.shardCount())
	}
	if got, ok := store.Get("flowlogs"); !ok || !got.Equal(at) {
		t.Fatalf("migrated cursor = %v/%v", got, ok)
	}
	if !client.legacy.LegacyMigrated {
		t.Fatal("legacy ConfigMap was not marked after every shard persisted")
	}
	assertLegacyMigrationResourceVersion(t, client, client.legacy.ResourceVersion)
}

func TestKubernetesCheckpointStore_ReconcilesRollbackEraLegacyWrites(t *testing.T) {
	initial := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	rollbackAudit := initial.Add(time.Minute)
	rollbackFlow := rollbackAudit.Add(time.Minute)
	legacy, err := json.Marshal(map[string]time.Time{
		"auditlogs": initial,
		"flowlogs":  initial,
		"pruned":    initial,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{
		legacy:       KubernetesCheckpointObject{ResourceVersion: "legacy-1", Data: legacy},
		legacyExists: true,
	}

	// First migrate the legacy release, then let the new release prune shard
	// state before an operator rolls back.
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if err := store.Delete("pruned"); err != nil {
		t.Fatalf("prune shard key: %v", err)
	}
	assertLegacyMigrationResourceVersion(t, client, client.legacy.ResourceVersion)

	// An unchanged legacy ConfigMap must still be ignored on a normal restart.
	writesBeforeUnchangedOpen := client.writeCalls()
	unchanged, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("reopen with unchanged legacy ConfigMap: %v", err)
	}
	if _, ok := unchanged.Get("pruned"); ok {
		t.Fatal("unchanged legacy ConfigMap resurrected a pruned key")
	}
	if got := client.writeCalls(); got != writesBeforeUnchangedOpen {
		t.Fatalf("unchanged legacy ConfigMap caused %d writes, want %d", got, writesBeforeUnchangedOpen)
	}

	// Simulate the former single-ConfigMap release writing during the rollback.
	rollbackLegacy, err := json.Marshal(map[string]time.Time{
		"auditlogs": rollbackAudit,
		"flowlogs":  rollbackFlow,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.writeLegacyForRollback(rollbackLegacy)

	reupgraded, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("re-upgrade after rollback write: %v", err)
	}
	if got, ok := reupgraded.Get("auditlogs"); !ok || !got.Equal(rollbackAudit) {
		t.Fatalf("rollback-era audit cursor = %v/%v, want %v/true", got, ok, rollbackAudit)
	}
	if got, ok := reupgraded.Get("flowlogs"); !ok || !got.Equal(rollbackFlow) {
		t.Fatalf("rollback-era flow cursor = %v/%v, want %v/true", got, ok, rollbackFlow)
	}
	if _, ok := reupgraded.Get("pruned"); ok {
		t.Fatal("rollback-era legacy ConfigMap without the key resurrected a pruned key")
	}
	if client.legacy.LegacyMigrated {
		t.Fatal("re-upgrade restored the legacy migration marker")
	}
	assertLegacyMigrationResourceVersion(t, client, client.legacy.ResourceVersion)

	writesBeforeIdempotentOpen := client.writeCalls()
	if _, err := NewKubernetesCheckpointStore(context.Background(), client); err != nil {
		t.Fatalf("idempotent reopen after rollback reconciliation: %v", err)
	}
	if got := client.writeCalls(); got != writesBeforeIdempotentOpen {
		t.Fatalf("idempotent reopen after rollback reconciliation caused %d writes, want %d", got, writesBeforeIdempotentOpen)
	}
}

func TestKubernetesCheckpointStore_IncompleteMigrationBaselinePreservesNewerShardRows(t *testing.T) {
	initial := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	legacyAdvance := initial.Add(time.Minute)
	shardNewer := legacyAdvance.Add(time.Minute)
	legacy, err := json.Marshal(map[string]time.Time{
		"auditlogs": legacyAdvance,
		"flowlogs":  legacyAdvance,
	})
	if err != nil {
		t.Fatal(err)
	}
	flowlogs, _, err := EncodeKubernetesCheckpointShard("flowlogs", map[string]time.Time{"flowlogs": shardNewer})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{
		shards: map[string]KubernetesCheckpointObject{
			"flowlogs": {Shard: "flowlogs", ResourceVersion: "1", Data: flowlogs},
		},
		legacy:       KubernetesCheckpointObject{ResourceVersion: "legacy-1", Data: legacy},
		legacyExists: true,
	}

	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatalf("resume incomplete migration: %v", err)
	}
	if got, ok := store.Get("auditlogs"); !ok || !got.Equal(legacyAdvance) {
		t.Fatalf("missing shard cursor = %v/%v, want %v/true", got, ok, legacyAdvance)
	}
	if got, ok := store.Get("flowlogs"); !ok || !got.Equal(shardNewer) {
		t.Fatalf("incomplete migration regressed newer shard cursor to %v/%v, want %v/true", got, ok, shardNewer)
	}
}

func TestKubernetesCheckpointStore_RepairsInterruptedMigrationBaseline(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	legacy, err := json.Marshal(map[string]time.Time{"flowlogs": at})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{
		legacy:       KubernetesCheckpointObject{ResourceVersion: "legacy-1", Data: legacy},
		legacyExists: true,
		failUpdate:   "flowlogs",
	}
	if _, err := NewKubernetesCheckpointStore(context.Background(), client); !errors.Is(err, ErrKubernetesCheckpointConflict) {
		t.Fatalf("initial migration with interrupted baseline = %v, want conflict", err)
	}
	if !client.legacy.LegacyMigrated {
		t.Fatal("legacy ConfigMap marker was not persisted before interrupted baseline write")
	}
	for _, object := range client.objects() {
		if object.LegacyMigrationResourceVersion != "" {
			t.Fatalf("interrupted shard baseline = %q, want empty", object.LegacyMigrationResourceVersion)
		}
	}

	client.failUpdate = ""
	reopened, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("reopen after interrupted baseline: %v", err)
	}
	if got, ok := reopened.Get("flowlogs"); !ok || !got.Equal(at) {
		t.Fatalf("recovered cursor = %v/%v, want %v/true", got, ok, at)
	}
	assertLegacyMigrationResourceVersion(t, client, client.legacy.ResourceVersion)
}

func TestKubernetesCheckpointStore_ResumesInterruptedLegacyMigration(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	legacy, err := json.Marshal(map[string]time.Time{
		"auditlogs": at,
		"flowlogs":  at,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{
		legacy:       KubernetesCheckpointObject{ResourceVersion: "legacy-1", Data: legacy},
		legacyExists: true,
		failCreate:   "flowlogs",
	}
	if _, err := NewKubernetesCheckpointStore(context.Background(), client); err == nil {
		t.Fatal("interrupted migration unexpectedly succeeded")
	}
	if client.shardCount() != 1 || client.legacy.LegacyMigrated {
		t.Fatalf("partial migration = %d shards, marked=%v; want one unmarked shard", client.shardCount(), client.legacy.LegacyMigrated)
	}
	client.failCreate = ""
	store, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	if client.shardCount() != 2 || !client.legacy.LegacyMigrated {
		t.Fatalf("resumed migration = %d shards, marked=%v; want two marked shards", client.shardCount(), client.legacy.LegacyMigrated)
	}
	for _, key := range []string{"auditlogs", "flowlogs"} {
		if got, ok := store.Get(key); !ok || !got.Equal(at) {
			t.Fatalf("resumed key %q = %v/%v, want %v", key, got, ok, at)
		}
	}
}

func TestKubernetesCheckpointStore_ResumesLegacyMigrationAfterShardConflict(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	legacy, err := json.Marshal(map[string]time.Time{
		"flowlogs":                    at,
		"flowlogs/replay/seen/opaque": at,
	})
	if err != nil {
		t.Fatal(err)
	}
	existing, _, err := EncodeKubernetesCheckpointShard("flowlogs", map[string]time.Time{"flowlogs": at})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeKubernetesCheckpointClient{
		shards:       map[string]KubernetesCheckpointObject{"flowlogs": {Shard: "flowlogs", ResourceVersion: "1", Data: existing}},
		legacy:       KubernetesCheckpointObject{ResourceVersion: "legacy-1", Data: legacy},
		legacyExists: true,
		failUpdate:   "flowlogs",
	}
	if _, err := NewKubernetesCheckpointStore(context.Background(), client); !errors.Is(err, ErrKubernetesCheckpointConflict) {
		t.Fatalf("migration update = %v, want conflict", err)
	}
	if client.legacy.LegacyMigrated {
		t.Fatal("legacy ConfigMap marked after conflicting shard update")
	}

	client.failUpdate = ""
	store, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	if !client.legacy.LegacyMigrated {
		t.Fatal("legacy ConfigMap not marked after conflict resolved")
	}
	if got, ok := store.Get("flowlogs/replay/seen/opaque"); !ok || !got.Equal(at) {
		t.Fatalf("resumed replay row = %v/%v, want %v", got, ok, at)
	}
}

func TestKubernetesCheckpointStore_PersistsHighCardinalitySeenSetAndGap(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	namespace := "objectstore/v1/dGFpbG5ldA/s3/flow/feed"
	for i := range 5000 {
		if err := store.Set(fmt.Sprintf("%s/seen/%064x", namespace, i), at); err != nil {
			t.Fatalf("Set seen %d: %v", i, err)
		}
	}
	gap := namespace + "/gap/pending"
	if err := store.Set(gap, at); err != nil {
		t.Fatalf("Set gap: %v", err)
	}
	if err := flushCheckpoint(t, store); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := client.shardCount(); got != 1 {
		t.Fatalf("shard count = %d, want 1", got)
	}
	reopened, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Get(gap); !ok || !got.Equal(at) {
		t.Fatalf("reopened gap = %v/%v, want %v", got, ok, at)
	}
	if got := len(reopened.Keys()); got != 5001 {
		t.Fatalf("reopened rows = %d, want 5001", got)
	}
}

func TestKubernetesCheckpointStore_PersistsMeasuredObjectStoreConfigurations(t *testing.T) {
	cases := []struct {
		name   string
		scopes int
	}{
		{name: "one feed one tailnet", scopes: 1},
		{name: "three feeds one tailnet", scopes: 3},
		{name: "three feeds three tailnets", scopes: 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeKubernetesCheckpointClient{}
			store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 9, 2, 12, 0, 0, 999999999, time.UTC)
			for scope := range tc.scopes {
				namespace := fmt.Sprintf("objectstore/v1/dGFpbG5ldC0%d/s3/flow/feed-%d", scope/3, scope)
				for row := range 5000 {
					if err := store.Set(fmt.Sprintf("%s/seen/%064x", namespace, row), at); err != nil {
						t.Fatalf("Set scope %d row %d: %v", scope, row, err)
					}
				}
			}
			if err := flushCheckpoint(t, store); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if got := client.shardCount(); got != tc.scopes {
				t.Fatalf("shards = %d, want %d", got, tc.scopes)
			}
			reopened, err := NewKubernetesCheckpointStore(context.Background(), client)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := len(reopened.Keys()), tc.scopes*5000; got != want {
				t.Fatalf("reopened rows = %d, want %d", got, want)
			}
		})
	}
}

func TestKubernetesCheckpointStore_CompressedShardMayExceedRawConfigMapLimit(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 12, 0, 0, 999999999, time.UTC)
	namespace := "objectstore/v1/dGFpbG5ldA/s3/flow/feed"
	for row := range 10000 {
		if err := store.Set(fmt.Sprintf("%s/seen/%064x", namespace, row), at); err != nil {
			t.Fatal(err)
		}
	}
	if err := flushCheckpoint(t, store); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	objects := client.objects()
	if len(objects) != 1 {
		t.Fatalf("objects = %d, want 1", len(objects))
	}
	_, rows, err := decodeKubernetesCheckpoint(objects[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	_, rawBytes, err := EncodeKubernetesCheckpointShard(namespace, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rawBytes <= KubernetesCheckpointDataLimit || len(objects[0].Data) >= KubernetesCheckpointDataLimit {
		t.Fatalf("raw=%d compressed=%d limit=%d; want raw over and compressed under", rawBytes, len(objects[0].Data), KubernetesCheckpointDataLimit)
	}
}

func TestKubernetesCheckpointStore_LogsShardHeadroom(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("flowlogs", time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"checkpoint.shard_count=", "checkpoint.compressed_size=", "checkpoint.uncompressed_size=", "checkpoint.compression_ratio="} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("persist log %q does not contain %q", logs.String(), field)
		}
	}
}

func TestKubernetesCheckpointStore_ConflictIsPerShardAndNeverClobbers(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	leader, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	initial := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
	if err := leader.Set("flowlogs", initial); err != nil {
		t.Fatal(err)
	}
	if err := leader.Set("auditlogs", initial); err != nil {
		t.Fatal(err)
	}
	deposed, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if err := leader.Set("flowlogs", current); err != nil {
		t.Fatal(err)
	}
	if err := deposed.Set("flowlogs", current.Add(-time.Hour)); !errors.Is(err, ErrKubernetesCheckpointConflict) {
		t.Fatalf("stale write = %v, want conflict", err)
	}
	// The stale flowlogs resourceVersion must not poison an independent shard
	// whose resourceVersion is still current.
	if err := deposed.Set("auditlogs", current); err != nil {
		t.Fatalf("independent shard after conflict: %v", err)
	}
	fresh, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fresh.Get("flowlogs"); !got.Equal(current) {
		t.Fatalf("conflict clobbered winner: got %v want %v", got, current)
	}
	if got, _ := fresh.Get("auditlogs"); !got.Equal(current) {
		t.Fatalf("independent shard did not advance: got %v want %v", got, current)
	}
}

func TestKubernetesCheckpointStore_BatchPersistsIndependentShardsAfterFailure(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{failCreate: "auditlogs"}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	err = UpdateCheckpointBatch(store, map[string]time.Time{
		"auditlogs": at,
		"flowlogs":  at,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "auditlogs") {
		t.Fatalf("batch error = %v, want failed auditlogs shard", err)
	}
	if got := client.shardCount(); got != 1 {
		t.Fatalf("shards after partial batch = %d, want independent flowlogs shard persisted", got)
	}
	if _, ok := client.shards["flowlogs"]; !ok {
		t.Fatal("flowlogs shard was skipped after auditlogs failed")
	}

	client.failCreate = ""
	if err := flushCheckpoint(t, store); err != nil {
		t.Fatalf("Flush failed shard: %v", err)
	}
	if got := client.shardCount(); got != 2 {
		t.Fatalf("shards after retry = %d, want 2", got)
	}
}

func TestKubernetesCheckpointStore_BoundsWriteContext(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{requireWriteDeadline: true}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("flowlogs", time.Unix(1, 0).UTC()); err != nil {
		t.Fatalf("Set without bounded write context: %v", err)
	}
}

func TestKubernetesCheckpointStore_BatchSharesOneWriteDeadline(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1, 0).UTC()
	if err := UpdateCheckpointBatch(store, map[string]time.Time{"auditlogs": at, "flowlogs": at}, nil); err != nil {
		t.Fatal(err)
	}
	deadlines := client.deadlines()
	if len(deadlines) != 2 {
		t.Fatalf("write deadlines = %v, want two", deadlines)
	}
	if !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("batch used per-shard deadlines %v, want one shared deadline", deadlines)
	}
}

func TestKubernetesCheckpointStore_AmbiguousWriteRequestsRestart(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed bool
	}{
		{name: "create"},
		{name: "update", seed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeKubernetesCheckpointClient{}
			if tc.seed {
				seed, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
				if err != nil {
					t.Fatal(err)
				}
				if err := seed.Set("flowlogs", time.Unix(1, 0).UTC()); err != nil {
					t.Fatal(err)
				}
				client.commitUpdateThenFail = "flowlogs"
			} else {
				client.commitCreateThenFail = "flowlogs"
			}
			fatal := make(chan error, 1)
			store, err := NewKubernetesCheckpointStore(context.Background(), client,
				WithKubernetesCheckpointWriteDebounce(0),
				WithKubernetesCheckpointFatalWrite(func(err error) { fatal <- err }),
			)
			if err != nil {
				t.Fatal(err)
			}
			err = store.Set("flowlogs", time.Unix(2, 0).UTC())
			if !errors.Is(err, ErrKubernetesCheckpointWriteUncertain) {
				t.Fatalf("ambiguous %s = %v, want ErrKubernetesCheckpointWriteUncertain", tc.name, err)
			}
			select {
			case got := <-fatal:
				if !errors.Is(got, ErrKubernetesCheckpointWriteUncertain) {
					t.Fatalf("fatal %s = %v, want ErrKubernetesCheckpointWriteUncertain", tc.name, got)
				}
			default:
				t.Fatalf("ambiguous %s did not request a process restart", tc.name)
			}
		})
	}
}

func TestKubernetesCheckpointStore_RejectsOversizeCompressedShardWithoutWrite(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client, WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatal(err)
	}
	// Pseudorandom printable key material keeps the compressed payload over the limit.
	var b strings.Builder
	b.Grow(KubernetesCheckpointDataLimit + 200000)
	x := uint64(1)
	for range KubernetesCheckpointDataLimit + 200000 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b.WriteByte(byte(33 + (x>>32)%94))
	}
	key := "x/" + b.String()
	encoded, _, err := EncodeKubernetesCheckpointShard(key, map[string]time.Time{key: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= KubernetesCheckpointDataLimit {
		t.Fatalf("oversize fixture compressed to %d bytes, want over %d", len(encoded), KubernetesCheckpointDataLimit)
	}
	err = store.Set(key, time.Unix(1, 0).UTC())
	if !errors.Is(err, ErrKubernetesCheckpointTooLarge) {
		t.Fatalf("oversize write = %v, want ErrKubernetesCheckpointTooLarge", err)
	}
	if got := client.writeCalls(); got != 0 {
		t.Fatalf("writes after rejected payload = %d, want 0", got)
	}
}

func TestKubernetesCheckpointEnvelopeRejectsExcessiveDecodedSize(t *testing.T) {
	rows := map[string]time.Time{"flowlogs": time.Unix(1, 0).UTC()}
	encoded, _, err := encodeKubernetesCheckpointShard("flowlogs", rows, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeKubernetesCheckpointWithLimit(encoded, 64); !errors.Is(err, ErrKubernetesCheckpointDecodedTooLarge) {
		t.Fatalf("decode over limit = %v, want ErrKubernetesCheckpointDecodedTooLarge", err)
	}
}

func TestKubernetesCheckpointEnvelopeRefusesStateItCannotReopen(t *testing.T) {
	rows := map[string]time.Time{"flowlogs": time.Unix(1, 0).UTC()}
	if _, _, err := encodeKubernetesCheckpointShard("flowlogs", rows, 64); !errors.Is(err, ErrKubernetesCheckpointDecodedTooLarge) {
		t.Fatalf("encode over decoded limit = %v, want ErrKubernetesCheckpointDecodedTooLarge", err)
	}
}

func flushCheckpoint(t *testing.T, store CheckpointStore) error {
	t.Helper()
	flusher, ok := store.(CheckpointFlusher)
	if !ok {
		t.Fatal("checkpoint store does not implement CheckpointFlusher")
	}
	return flusher.Flush()
}

type fakeKubernetesCheckpointClient struct {
	mu                   sync.Mutex
	shards               map[string]KubernetesCheckpointObject
	legacy               KubernetesCheckpointObject
	legacyExists         bool
	failCreate           string
	failUpdate           string
	requireWriteDeadline bool
	writeDeadlines       []time.Time
	creates              int
	updates              int
	commitCreateThenFail string
	commitUpdateThenFail string
}

func (c *fakeKubernetesCheckpointClient) ListCheckpoints(context.Context) ([]KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]KubernetesCheckpointObject, 0, len(c.shards))
	for _, o := range c.shards {
		out = append(out, o)
	}
	return out, nil
}
func (c *fakeKubernetesCheckpointClient) GetLegacyCheckpoint(context.Context) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.legacyExists {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointNotFound
	}
	return c.legacy, nil
}
func (c *fakeKubernetesCheckpointClient) MarkLegacyCheckpointMigrated(_ context.Context, resourceVersion string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.legacyExists {
		return "", ErrKubernetesCheckpointNotFound
	}
	if resourceVersion != "" && c.legacy.ResourceVersion != resourceVersion {
		return "", ErrKubernetesCheckpointConflict
	}
	c.legacy.LegacyMigrated = true
	if c.legacy.ResourceVersion == "" {
		c.legacy.ResourceVersion = "legacy-marked"
	} else {
		c.legacy.ResourceVersion += "-marked"
	}
	return c.legacy.ResourceVersion, nil
}
func (c *fakeKubernetesCheckpointClient) writeLegacyForRollback(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.legacy.Data = data
	c.legacy.LegacyMigrated = false
	c.legacy.ResourceVersion += "-rollback"
}
func (c *fakeKubernetesCheckpointClient) CreateCheckpoint(ctx context.Context, o KubernetesCheckpointObject) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requireWriteDeadline {
		if _, ok := ctx.Deadline(); !ok {
			return KubernetesCheckpointObject{}, errors.New("checkpoint create context has no deadline")
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		c.writeDeadlines = append(c.writeDeadlines, deadline)
	}
	if o.Shard == c.failCreate {
		return KubernetesCheckpointObject{}, errors.New("injected shard create failure")
	}
	if c.shards == nil {
		c.shards = map[string]KubernetesCheckpointObject{}
	}
	if _, ok := c.shards[o.Shard]; ok {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointAlreadyExists
	}
	o.ResourceVersion = "1"
	c.shards[o.Shard] = o
	c.creates++
	if o.Shard == c.commitCreateThenFail {
		return KubernetesCheckpointObject{}, context.DeadlineExceeded
	}
	return o, nil
}
func (c *fakeKubernetesCheckpointClient) UpdateCheckpoint(ctx context.Context, o KubernetesCheckpointObject) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requireWriteDeadline {
		if _, ok := ctx.Deadline(); !ok {
			return KubernetesCheckpointObject{}, errors.New("checkpoint update context has no deadline")
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		c.writeDeadlines = append(c.writeDeadlines, deadline)
	}
	if o.Shard == c.failUpdate {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointConflict
	}
	old, ok := c.shards[o.Shard]
	if !ok || old.ResourceVersion != o.ResourceVersion {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointConflict
	}
	c.updates++
	o.ResourceVersion = fmt.Sprintf("%d", c.updates+1)
	c.shards[o.Shard] = o
	if o.Shard == c.commitUpdateThenFail {
		return KubernetesCheckpointObject{}, context.DeadlineExceeded
	}
	return o, nil
}
func (c *fakeKubernetesCheckpointClient) shardCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.shards)
}
func (c *fakeKubernetesCheckpointClient) objects() []KubernetesCheckpointObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []KubernetesCheckpointObject
	for _, o := range c.shards {
		out = append(out, o)
	}
	return out
}
func (c *fakeKubernetesCheckpointClient) writeCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creates + c.updates
}

func assertLegacyMigrationResourceVersion(t *testing.T, client *fakeKubernetesCheckpointClient, want string) {
	t.Helper()
	for _, object := range client.objects() {
		if object.LegacyMigrationResourceVersion != want {
			t.Fatalf("shard %q migration resourceVersion = %q, want %q", object.Shard, object.LegacyMigrationResourceVersion, want)
		}
	}
}

func (c *fakeKubernetesCheckpointClient) deadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.writeDeadlines...)
}
