package ingresswal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRequiresExplicitPositiveCapacity(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	tests := []struct {
		name string
		opts Options
	}{
		{name: "missing byte limit", opts: Options{Directory: dir, MaxEntries: 1}},
		{name: "missing entry limit", opts: Options{Directory: dir, MaxBytes: 1}},
		{name: "negative byte limit", opts: Options{Directory: dir, MaxBytes: -1, MaxEntries: 1}},
		{name: "negative entry limit", opts: Options{Directory: dir, MaxBytes: 1, MaxEntries: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts); err == nil {
				t.Fatal("New accepted capacity without explicit positive byte and entry limits")
			}
		})
	}
}

func TestAppendReplayCommitSurvivesRestartInFIFOOrder(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	store := mustNew(t, opts)

	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail-a", "hec", "flow", []byte("first"))
	second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail-b", "webhook", "audit", []byte("second"))
	if err := store.Append(context.Background(), second); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if err := store.Append(context.Background(), first); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := mustNew(t, opts)
	var got []Envelope
	if err := reopened.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		got = append(got, envelope)
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(got, []Envelope{second, first}) {
		t.Fatalf("Replay envelopes = %#v, want append FIFO %#v", got, []Envelope{second, first})
	}
	if health := reopened.Health(); health.PendingEntries != 0 || health.PendingBytes != 0 {
		t.Fatalf("Health after replay = %+v, want empty", health)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close reopened: %v", err)
	}

	empty := mustNew(t, opts)
	called := false
	if err := empty.Replay(context.Background(), func(context.Context, Envelope) error {
		called = true
		return nil
	}, nil); err != nil {
		t.Fatalf("second Replay: %v", err)
	}
	if called {
		t.Fatal("completed entries replayed after restart")
	}
}

func TestReplayObserverRunsInFIFOOrderAfterEachCommit(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("first"))
	second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("second"))
	for _, entry := range []Envelope{first, second} {
		if err := store.Append(context.Background(), entry); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	var events []string
	observer := CommitObserver(func(id string) {
		health := store.Health()
		events = append(events, fmt.Sprintf("commit:%s:%d", id, health.PendingEntries))
	})
	if err := store.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		events = append(events, "handle:"+envelope.ID)
		return nil
	}, observer); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := []string{
		"handle:" + first.ID,
		"commit:" + first.ID + ":1",
		"handle:" + second.ID,
		"commit:" + second.ID + ":0",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Replay events = %v, want %v", events, want)
	}
}

func TestReplayAllowsNilCommitObserver(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay with nil observer: %v", err)
	}
	if got := store.Health().PendingEntries; got != 0 {
		t.Fatalf("pending entries after nil-observer Replay = %d, want 0", got)
	}
}

func TestReplayHandlerFailureDoesNotNotifyObserver(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	boom := errors.New("handler failed")
	var observed []string
	err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		return boom
	}, func(id string) {
		observed = append(observed, id)
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Replay error = %v, want %v", err, boom)
	}
	if len(observed) != 0 {
		t.Fatalf("observer called after handler failure: %v", observed)
	}
}

func TestReplayCommitFailureNotifiesOnlyAfterSuccessfulRetry(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	realSyncDir := store.ops.syncDir
	boom := errors.New("completion marker directory sync failed")
	failSync := true
	store.ops.syncDir = func(directory *os.File) error {
		if failSync {
			failSync = false
			return boom
		}
		return realSyncDir(directory)
	}
	handlerCalls := 0
	var observed []string
	observer := CommitObserver(func(id string) {
		observed = append(observed, id)
	})
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, observer); !errors.Is(err, boom) {
		t.Fatalf("first Replay error = %v, want %v", err, boom)
	}
	if len(observed) != 0 {
		t.Fatalf("observer called after failed Commit: %v", observed)
	}
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, observer); err != nil {
		t.Fatalf("retry Replay: %v", err)
	}
	if handlerCalls != 2 {
		t.Fatalf("handler calls = %d, want 2 after pre-marker Commit retry", handlerCalls)
	}
	if !reflect.DeepEqual(observed, []string{entry.ID}) {
		t.Fatalf("observer IDs = %v, want one %s", observed, entry.ID)
	}
}

func TestReplayCompletedSnapshotCannotCommitReappendedGeneration(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	anchor := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("anchor"))
	target := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("target"))
	if anchor.ID > target.ID {
		anchor, target = target, anchor
	}
	for _, entry := range []Envelope{anchor, target} {
		if err := store.Append(context.Background(), entry); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	realRemove := store.ops.removeAt
	keepMarker := errors.New("retain completion marker")
	store.ops.removeAt = func(directory *os.File, name string) error {
		if strings.HasSuffix(name, entrySuffix) {
			return keepMarker
		}
		return realRemove(directory, name)
	}
	for _, entry := range []Envelope{anchor, target} {
		if err := store.Commit(context.Background(), entry.ID); !errors.Is(err, keepMarker) {
			t.Fatalf("Commit retaining marker for %s = %v, want %v", entry.ID, err, keepMarker)
		}
	}
	store.ops.removeAt = realRemove

	var handled, observed []string
	var appendErr error
	if err := store.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		handled = append(handled, envelope.ID)
		return nil
	}, func(id string) {
		observed = append(observed, id)
		if id == anchor.ID {
			retry := target
			retry.Accepted = retry.Accepted.Add(time.Hour)
			appendErr = store.Append(context.Background(), retry)
		}
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if appendErr != nil {
		t.Fatalf("Append re-admitted generation: %v", appendErr)
	}
	if !reflect.DeepEqual(handled, []string{target.ID}) {
		t.Fatalf("handled IDs = %v, want re-admitted generation %s", handled, target.ID)
	}
	if !reflect.DeepEqual(observed, []string{anchor.ID, target.ID}) {
		t.Fatalf("observer IDs = %v, want committed generations %v", observed, []string{anchor.ID, target.ID})
	}
	health := store.Health()
	if health.PendingEntries != 0 || health.PendingBytes != 0 ||
		health.CompletionMarkers != 0 || health.OrphanStages != 0 || health.OrphanBytes != 0 {
		t.Fatalf("generation replay retained accounted state: %+v", health)
	}
}

func TestReplayPendingSnapshotCannotCommitReappendedGeneration(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	replayErr := make(chan error, 1)
	var observed []string
	go func() {
		replayErr <- store.Replay(context.Background(), func(context.Context, Envelope) error {
			close(entered)
			<-release
			return nil
		}, func(id string) {
			observed = append(observed, id)
		})
	}()
	<-entered
	if err := store.Commit(context.Background(), entry.ID); err != nil {
		t.Fatalf("concurrent Commit: %v", err)
	}
	retry := entry
	retry.Accepted = retry.Accepted.Add(time.Hour)
	if err := store.Append(context.Background(), retry); err != nil {
		t.Fatalf("Append re-admitted generation: %v", err)
	}
	close(release)
	if err := <-replayErr; err != nil {
		t.Fatalf("first Replay: %v", err)
	}
	if len(observed) != 0 {
		t.Fatalf("observer acknowledged stale pending snapshot: %v", observed)
	}
	if health := store.Health(); health.PendingEntries != 1 || health.PendingBytes == 0 {
		t.Fatalf("stale pending snapshot removed re-admitted generation: %+v", health)
	}

	handled := 0
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handled++
		return nil
	}, func(id string) {
		observed = append(observed, id)
	}); err != nil {
		t.Fatalf("second Replay: %v", err)
	}
	if handled != 1 {
		t.Fatalf("second Replay handled %d generations, want 1", handled)
	}
	if !reflect.DeepEqual(observed, []string{entry.ID}) {
		t.Fatalf("observer IDs = %v, want only re-admitted generation %s", observed, entry.ID)
	}
}

func TestStartupMarkerCleanupCannotDeleteNewerPendingGeneration(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	store := mustNew(t, opts)
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	realRemove := store.ops.removeAt
	keepMarker := errors.New("retain completion marker")
	store.ops.removeAt = func(directory *os.File, name string) error {
		if strings.HasSuffix(name, entrySuffix) {
			return keepMarker
		}
		return realRemove(directory, name)
	}
	if err := store.Commit(context.Background(), entry.ID); !errors.Is(err, keepMarker) {
		t.Fatalf("Commit retaining marker = %v, want %v", err, keepMarker)
	}
	store.ops.removeAt = realRemove
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.Remove(filepath.Join(dir, entryName(1, entry.ID))); err != nil {
		t.Fatalf("remove old pending generation: %v", err)
	}
	retry := entry
	retry.Accepted = retry.Accepted.Add(time.Hour)
	data, err := encodeRecord(retry, 2)
	if err != nil {
		t.Fatalf("encode newer generation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, entryName(2, entry.ID)), data, 0o600); err != nil {
		t.Fatalf("write newer generation: %v", err)
	}

	reopened := mustNew(t, opts)
	if err := reopened.Commit(context.Background(), entry.ID); err != nil {
		t.Fatalf("direct Commit retry: %v", err)
	}
	healthAfterCommit := reopened.Health()
	if healthAfterCommit.PendingEntries != 1 || healthAfterCommit.PendingBytes == 0 ||
		healthAfterCommit.CompletionMarkers != 0 ||
		healthAfterCommit.OrphanStages != 0 || healthAfterCommit.OrphanBytes != 0 {
		t.Fatalf("direct Commit retry removed newer generation: %+v", healthAfterCommit)
	}
	handled := 0
	var healthDuringHandler Health
	var observed []string
	if err := reopened.Replay(context.Background(), func(context.Context, Envelope) error {
		handled++
		healthDuringHandler = reopened.Health()
		return nil
	}, func(id string) {
		observed = append(observed, id)
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if handled != 1 {
		t.Fatalf("Replay handled %d newer generations, want 1", handled)
	}
	if healthDuringHandler.PendingEntries != 1 || healthDuringHandler.PendingBytes == 0 ||
		healthDuringHandler.CompletionMarkers != 0 ||
		healthDuringHandler.OrphanStages != 0 || healthDuringHandler.OrphanBytes != 0 {
		t.Fatalf("state after stale marker cleanup and before new commit = %+v", healthDuringHandler)
	}
	if !reflect.DeepEqual(observed, []string{entry.ID}) {
		t.Fatalf("observer IDs = %v, want only newer generation %s", observed, entry.ID)
	}
	if health := reopened.Health(); health.PendingEntries != 0 || health.PendingBytes != 0 ||
		health.CompletionMarkers != 0 || health.OrphanStages != 0 || health.OrphanBytes != 0 {
		t.Fatalf("Replay retained accounted state: %+v", health)
	}
}

func TestCompletionMarkerAssociatesOutOfOrderCommitWithEntryGeneration(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("first"))
	second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("second"))
	if err := store.Append(context.Background(), first); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Append(context.Background(), second); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	realRemove := store.ops.removeAt
	keepMarker := errors.New("retain completion marker")
	store.ops.removeAt = func(directory *os.File, name string) error {
		if name == entryName(2, second.ID) {
			return keepMarker
		}
		return realRemove(directory, name)
	}
	if err := store.Commit(context.Background(), second.ID); !errors.Is(err, keepMarker) {
		t.Fatalf("out-of-order Commit = %v, want %v", err, keepMarker)
	}
	store.ops.removeAt = realRemove
	if err := store.Commit(context.Background(), second.ID); err != nil {
		t.Fatalf("direct Commit retry: %v", err)
	}

	health := store.Health()
	if health.PendingEntries != 1 || health.PendingBytes == 0 || health.CompletionMarkers != 0 {
		t.Fatalf("out-of-order marker cleanup retained wrong generation: %+v", health)
	}
	handled := 0
	if err := store.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		handled++
		if envelope.ID != first.ID {
			t.Fatalf("Replay handled ID %s, want remaining first generation %s", envelope.ID, first.ID)
		}
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if handled != 1 {
		t.Fatalf("Replay handled %d generations, want 1", handled)
	}
}

func TestStoreCreatesOwnerOnlyDirectoryAndFiles(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	store := mustNew(t, Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("secret body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	assertMode(t, dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, item := range entries {
		if item.IsDir() {
			t.Fatalf("unexpected subdirectory %q", item.Name())
		}
		assertMode(t, filepath.Join(dir, item.Name()), 0o600)
	}
}

func TestAppendRejectsEntryAndByteExhaustionWithoutEviction(t *testing.T) {
	t.Parallel()

	t.Run("entries", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wal")
		store := mustNew(t, Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 1})
		first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("first"))
		second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("second"))
		if err := store.Append(context.Background(), first); err != nil {
			t.Fatalf("Append first: %v", err)
		}
		var full *FullError
		if err := store.Append(context.Background(), second); !errors.As(err, &full) ||
			!errors.Is(err, ErrFull) || full.Limit != LimitEntries {
			t.Fatalf("Append second error = %v, want entry FullError", err)
		}
		assertPendingIDs(t, store, []string{first.ID})
	})

	t.Run("bytes", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wal")
		probe := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("first"))
		encoded, err := encodeRecord(probe, 1)
		if err != nil {
			t.Fatalf("encodeRecord: %v", err)
		}
		store := mustNew(t, Options{Directory: dir, MaxBytes: int64(len(encoded)), MaxEntries: 10})
		if err := store.Append(context.Background(), probe); err != nil {
			t.Fatalf("Append exact-fit entry: %v", err)
		}
		second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("x"))
		var full *FullError
		if err := store.Append(context.Background(), second); !errors.As(err, &full) ||
			!errors.Is(err, ErrFull) || full.Limit != LimitBytes {
			t.Fatalf("Append over byte limit error = %v, want byte FullError", err)
		}
		assertPendingIDs(t, store, []string{probe.ID})
	})
}

func TestConcurrentAppendReservesCapacityUnderOneMutex(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	store := mustNew(t, Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 8})

	const attempts = 32
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := testEnvelope(
				t,
				time.Unix(1_700_000_000+int64(i), 0),
				"tail",
				"hec",
				"flow",
				fmt.Appendf(nil, "body-%d", i),
			)
			errs <- store.Append(context.Background(), entry)
		}()
	}
	wg.Wait()
	close(errs)

	success, full := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrFull):
			full++
		default:
			t.Fatalf("Append returned unexpected error: %v", err)
		}
	}
	if success != 8 || full != attempts-8 {
		t.Fatalf("append outcomes success/full = %d/%d, want 8/%d", success, full, attempts-8)
	}
	if health := store.Health(); health.PendingEntries != 8 {
		t.Fatalf("pending entries = %d, want 8", health.PendingEntries)
	}
}

func TestSecondWriterIsRefusedUntilOwnerCloses(t *testing.T) {
	t.Parallel()

	opts := Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 10}
	first := mustNew(t, opts)

	_, err := New(opts)
	var ownership *OwnershipError
	if !errors.As(err, &ownership) || !errors.Is(err, ErrOwnership) {
		t.Fatalf("second New error = %v, want OwnershipError", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}
	second := mustNew(t, opts)
	if err := second.Close(); err != nil {
		t.Fatalf("Close second: %v", err)
	}
}

func TestNewIDIsDeterministicDigestOfRouteAndExactBody(t *testing.T) {
	t.Parallel()

	first, err := NewID("tail", "hec", "flow", []byte{0, 1, 2, 255})
	if err != nil {
		t.Fatalf("NewID first: %v", err)
	}
	same, err := NewID("tail", "hec", "flow", []byte{0, 1, 2, 255})
	if err != nil {
		t.Fatalf("NewID same: %v", err)
	}
	if first != same {
		t.Fatalf("same route/body IDs differ: %q != %q", first, same)
	}
	const want = "a6a02a18577efb10b70628c599b30ed07eb463c25652fcd8f6082686dc34a1df"
	if first != want {
		t.Fatalf("NewID fixture = %q, want SHA-256 contract %q", first, want)
	}
	for _, changed := range []struct {
		tailnet string
		source  string
		signal  string
		body    []byte
	}{
		{tailnet: "tail-2", source: "hec", signal: "flow", body: []byte{0, 1, 2, 255}},
		{tailnet: "tail", source: "webhook", signal: "flow", body: []byte{0, 1, 2, 255}},
		{tailnet: "tail", source: "hec", signal: "audit", body: []byte{0, 1, 2, 255}},
		{tailnet: "tail", source: "hec", signal: "flow", body: []byte{0, 1, 2, 254}},
	} {
		got, idErr := NewID(changed.tailnet, changed.source, changed.signal, changed.body)
		if idErr != nil {
			t.Fatalf("NewID changed field: %v", idErr)
		}
		if got == first {
			t.Fatalf("changed route/body retained digest %q", got)
		}
	}
}

func TestEnvelopeBoundsAreStrict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tailnet string
		source  string
		signal  string
		body    []byte
	}{
		{name: "tailnet", tailnet: strings.Repeat("t", maxTailnetBytes+1), source: "hec", signal: "flow"},
		{name: "source", tailnet: "tail", source: strings.Repeat("s", maxSourceBytes+1), signal: "flow"},
		{name: "signal", tailnet: "tail", source: "hec", signal: strings.Repeat("g", maxSignalBytes+1)},
		{name: "body", tailnet: "tail", source: "hec", signal: "flow", body: make([]byte, maxBodyBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewID(tt.tailnet, tt.source, tt.signal, tt.body); err == nil {
				t.Fatalf("NewID accepted over-limit %s", tt.name)
			}
		})
	}
}

func TestAppendFsyncsFileBeforePublishAndDirectoryBeforeSuccess(t *testing.T) {
	t.Parallel()

	var events []string
	ops := realFileOps
	realMkdir := ops.mkdirAt
	realWrite := ops.write
	realSyncFile := ops.syncFile
	realPublish := ops.publishNoReplace
	realSyncDir := ops.syncDir
	ops.mkdirAt = func(parent *os.File, name string, mode os.FileMode) error {
		events = append(events, "mkdir-final")
		return realMkdir(parent, name, mode)
	}
	ops.write = func(file *os.File, data []byte) (int, error) {
		events = append(events, "write")
		return realWrite(file, data)
	}
	ops.syncFile = func(file *os.File) error {
		events = append(events, "file-sync")
		return realSyncFile(file)
	}
	ops.publishNoReplace = func(directory *os.File, oldPath, newPath string) error {
		events = append(events, "link-no-replace")
		return realPublish(directory, oldPath, newPath)
	}
	ops.syncDir = func(directory *os.File) error {
		events = append(events, "dir-sync")
		return realSyncDir(directory)
	}

	opts := Options{Directory: filepath.Join(t.TempDir(), "wal"), MaxBytes: 1 << 20, MaxEntries: 10}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// New durably creates the final directory first (parent dir-sync). The
	// append itself is write -> file sync -> no-replace link -> dir sync.
	if want := []string{"mkdir-final", "dir-sync", "write", "file-sync", "link-no-replace", "dir-sync"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("append events = %v, want %v", events, want)
	}
}

func TestNewRequiresExistingParentAndCreatesOnlyFinalDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	missingParent := filepath.Join(root, "missing", "wal")
	if _, err := New(Options{Directory: missingParent, MaxBytes: 1 << 20, MaxEntries: 10}); err == nil {
		t.Fatal("New created missing parent directories")
	}
	if _, err := os.Lstat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("New mutated missing parent path: %v", err)
	}

	final := filepath.Join(root, "wal")
	store := mustNew(t, Options{Directory: final, MaxBytes: 1 << 20, MaxEntries: 10})
	_ = store
	assertMode(t, final, 0o700)
}

func TestNewRetriesParentDurabilityAfterCreationSyncFailure(t *testing.T) {
	t.Parallel()

	opts := Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	}
	ops := realFileOps
	realSync := ops.syncDir
	boom := errors.New("simulated parent fsync failure")
	calls := 0
	ops.syncDir = func(directory *os.File) error {
		calls++
		if calls == 1 {
			return boom
		}
		return realSync(directory)
	}
	if store, err := newStore(opts, ops); !errors.Is(err, boom) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("first newStore error = %v, want %v", err, boom)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("retry newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if calls != 2 {
		t.Fatalf("parent sync calls = %d, want retry to sync visible prior creation", calls)
	}
}

func TestAppendPublishNeverReplacesExistingDestination(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realPublish := ops.publishNoReplace
	injected := false
	ops.publishNoReplace = func(directory *os.File, stage, destination string) error {
		if !injected {
			injected = true
			file, err := platformCreateExclusiveAt(directory, destination, 0o600)
			if err != nil {
				return err
			}
			if _, err := file.Write([]byte("sentinel")); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
		return realPublish(directory, stage, destination)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); !errors.Is(err, ErrOwnership) {
		t.Fatalf("Append collision error = %v, want ErrOwnership", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, entryName(1, entry.ID)))
	if err != nil {
		t.Fatalf("ReadFile destination: %v", err)
	}
	if string(got) != "sentinel" {
		t.Fatalf("destination = %q, want pre-existing sentinel", got)
	}
}

func TestAppendUsesHeldDirectoryAndDetectsPathReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "wal")
	moved := filepath.Join(root, "wal-moved")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realPublish := ops.publishNoReplace
	swapped := false
	ops.publishNoReplace = func(directory *os.File, stage, destination string) error {
		if !swapped {
			swapped = true
			if err := os.Rename(dir, moved); err != nil {
				return err
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, destination), []byte("replacement sentinel"), 0o600); err != nil {
				return err
			}
		}
		return realPublish(directory, stage, destination)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); !errors.Is(err, ErrOwnership) {
		t.Fatalf("Append after directory replacement = %v, want ErrOwnership", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, entryName(1, entry.ID)))
	if err != nil {
		t.Fatalf("ReadFile replacement sentinel: %v", err)
	}
	if string(got) != "replacement sentinel" {
		t.Fatalf("replacement directory destination = %q, want untouched sentinel", got)
	}
	if got, err := os.ReadFile(filepath.Join(moved, entryName(1, entry.ID))); err != nil || len(got) == 0 {
		t.Fatalf("held directory did not receive landed entry: %d bytes/%v", len(got), err)
	}
}

func TestAppendFailureDoesNotPublishOrConsumeCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(*fileOps, error)
		want   error
	}{
		{
			name: "short write",
			inject: func(ops *fileOps, _ error) {
				ops.write = func(file *os.File, data []byte) (int, error) {
					return file.Write(data[:len(data)-1])
				}
			},
			want: io.ErrShortWrite,
		},
		{
			name: "disk full",
			inject: func(ops *fileOps, boom error) {
				ops.write = func(*os.File, []byte) (int, error) { return 0, boom }
			},
		},
		{
			name: "file fsync",
			inject: func(ops *fileOps, boom error) {
				ops.syncFile = func(*os.File) error { return boom }
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "wal")
			ops := realFileOps
			boom := errors.New("simulated storage failure")
			tt.inject(&ops, boom)
			store, err := newStore(Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 1}, ops)
			if err != nil {
				t.Fatalf("newStore: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("secret body"))
			err = store.Append(context.Background(), entry)
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("Append error = %v, want wrapping %v", err, tt.want)
				}
			} else if !errors.Is(err, boom) {
				t.Fatalf("Append error = %v, want wrapping %v", err, boom)
			}
			if strings.Contains(err.Error(), "secret body") {
				t.Fatalf("Append error leaked payload: %v", err)
			}
			if health := store.Health(); health.PendingEntries != 0 || health.PendingBytes != 0 {
				t.Fatalf("failed append consumed capacity: %+v", health)
			}
			assertNoOwnedStagesOrEntries(t, dir)
		})
	}
}

func TestFailedStageCleanupBlocksAppendAndKeepsBytesAccounted(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realWrite := ops.write
	writeBoom := errors.New("simulated write failure after sensitive bytes landed")
	writeCalls := 0
	ops.write = func(file *os.File, data []byte) (int, error) {
		writeCalls++
		n, err := realWrite(file, data)
		if err != nil {
			return n, err
		}
		if writeCalls == 1 {
			return n, writeBoom
		}
		return n, nil
	}
	realRemove := ops.removeAt
	unlinkBoom := errors.New("simulated staging unlink failure")
	stageRemoveAttempts := 0
	ops.removeAt = func(directory *os.File, name string) error {
		if inStageNamespace(name) {
			stageRemoveAttempts++
			if stageRemoveAttempts <= 2 {
				return unlinkBoom
			}
		}
		return realRemove(directory, name)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("sensitive"))
	if err := store.Append(context.Background(), first); !errors.Is(err, writeBoom) {
		t.Fatalf("first Append error = %v, want %v", err, writeBoom)
	}
	health := store.Health()
	if health.PendingEntries != 0 || health.OrphanStages != 1 || health.OrphanBytes == 0 {
		t.Fatalf("failed cleanup was not separately accounted: %+v", health)
	}

	second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("second"))
	if err := store.Append(context.Background(), second); !errors.Is(err, unlinkBoom) {
		t.Fatalf("blocked Append error = %v, want cleanup error %v", err, unlinkBoom)
	}
	if health := store.Health(); health.PendingEntries != 0 || health.OrphanStages != 1 {
		t.Fatalf("blocked Append published new state: %+v", health)
	}
	if err := store.Append(context.Background(), second); err != nil {
		t.Fatalf("Append after orphan cleanup: %v", err)
	}
	health = store.Health()
	if health.PendingEntries != 1 || health.OrphanStages != 0 || health.OrphanBytes != 0 {
		t.Fatalf("orphan cleanup did not transfer capacity to new pending entry: %+v", health)
	}
}

func TestReplayBlocksPendingEntriesUntilOrphanCleanupSucceeds(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("pending"))
	if err := store.Append(context.Background(), first); err != nil {
		t.Fatalf("Append pending entry: %v", err)
	}

	realWrite := store.ops.write
	writeBoom := errors.New("simulated write failure after staged bytes landed")
	failWrite := true
	store.ops.write = func(file *os.File, data []byte) (int, error) {
		n, err := realWrite(file, data)
		if err != nil {
			return n, err
		}
		if failWrite {
			failWrite = false
			return n, writeBoom
		}
		return n, nil
	}
	realRemove := store.ops.removeAt
	unlinkBoom := errors.New("simulated orphan cleanup failure")
	stageRemoveAttempts := 0
	store.ops.removeAt = func(directory *os.File, name string) error {
		if inStageNamespace(name) {
			stageRemoveAttempts++
			if stageRemoveAttempts <= 2 {
				return unlinkBoom
			}
		}
		return realRemove(directory, name)
	}
	second := testEnvelope(t, time.Unix(1_700_000_001, 0), "tail", "hec", "flow", []byte("orphan"))
	if err := store.Append(context.Background(), second); !errors.Is(err, writeBoom) {
		t.Fatalf("Append creating orphan error = %v, want %v", err, writeBoom)
	}

	handlerCalls := 0
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, nil); !errors.Is(err, unlinkBoom) {
		t.Fatalf("Replay with unremovable orphan error = %v, want %v", err, unlinkBoom)
	}
	if handlerCalls != 0 {
		t.Fatalf("Replay handled %d pending entries before orphan cleanup", handlerCalls)
	}

	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay after orphan cleanup: %v", err)
	}
	if handlerCalls != 1 {
		t.Fatalf("Replay calls after orphan cleanup = %d, want 1", handlerCalls)
	}
	if health := store.Health(); health.PendingEntries != 0 || health.OrphanStages != 0 {
		t.Fatalf("Replay cleanup retained pending/orphan state: %+v", health)
	}
}

func TestAppendCancellationBeforePublicationRemovesStage(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	ctx, cancel := context.WithCancel(context.Background())
	realSync := store.ops.syncFile
	store.ops.syncFile = func(file *os.File) error {
		if err := realSync(file); err != nil {
			return err
		}
		cancel()
		return nil
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(ctx, entry); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append error = %v, want context cancellation before publication", err)
	}
	if health := store.Health(); health.PendingEntries != 0 || health.OrphanStages != 0 {
		t.Fatalf("canceled pre-publication append landed state: %+v", health)
	}
	assertNoOwnedStagesOrEntries(t, store.opts.Directory)
}

func TestAppendIgnoresCancellationAfterPublicationBegins(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	ctx, cancel := context.WithCancel(context.Background())
	realPublish := store.ops.publishNoReplace
	store.ops.publishNoReplace = func(directory *os.File, stage, destination string) error {
		err := realPublish(directory, stage, destination)
		cancel()
		return err
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(ctx, entry); err != nil {
		t.Fatalf("Append after publication began: %v", err)
	}
	if health := store.Health(); health.PendingEntries != 1 {
		t.Fatalf("post-publication cancellation rolled back entry: %+v", health)
	}
}

func TestAppendDirectorySyncFailureReturnsErrorButReplaysLandedEntry(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	store, err := newStore(opts, realFileOps)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	boom := errors.New("simulated directory fsync failure")
	store.ops.syncDir = func(*os.File) error { return boom }
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); !errors.Is(err, boom) {
		t.Fatalf("Append error = %v, want %v", err, boom)
	}
	if health := store.Health(); health.PendingEntries != 1 {
		t.Fatalf("landed append missing from active index after fsync ambiguity: %+v", health)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := mustNew(t, opts)
	var got []Envelope
	stop := errors.New("stop before commit")
	if err := reopened.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		got = append(got, envelope)
		return stop
	}, nil); !errors.Is(err, stop) {
		t.Fatalf("Replay error = %v, want %v", err, stop)
	}
	if !reflect.DeepEqual(got, []Envelope{entry}) {
		t.Fatalf("Replay = %#v, want %#v", got, []Envelope{entry})
	}
}

func TestRecoveryBarrierBlocksAmbiguousEntryUntilDirectorySyncSucceeds(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	store := mustNew(t, opts)
	appendSyncBoom := errors.New("simulated append directory fsync ambiguity")
	store.ops.syncDir = func(*os.File) error { return appendSyncBoom }
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); !errors.Is(err, appendSyncBoom) {
		t.Fatalf("Append error = %v, want %v", err, appendSyncBoom)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recoveryOps := realFileOps
	realSyncDir := recoveryOps.syncDir
	recoverySyncBoom := errors.New("simulated recovery directory fsync failure")
	syncCalls := 0
	recoveryOps.syncDir = func(directory *os.File) error {
		syncCalls++
		if syncCalls == 2 {
			return recoverySyncBoom
		}
		return realSyncDir(directory)
	}
	handlerCalls := 0
	reopened, err := newStore(opts, recoveryOps)
	if err == nil {
		err = reopened.Replay(context.Background(), func(context.Context, Envelope) error {
			handlerCalls++
			return nil
		}, nil)
	}
	if reopened != nil {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Fatalf("Close reopened store: %v", closeErr)
		}
	}
	if !errors.Is(err, recoverySyncBoom) {
		t.Fatalf("recovery before directory barrier error = %v, want %v", err, recoverySyncBoom)
	}
	if handlerCalls != 0 {
		t.Fatalf("recovery handled ambiguous entry %d times before directory barrier", handlerCalls)
	}

	retried := mustNew(t, opts)
	if err := retried.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay after recovery barrier: %v", err)
	}
	if handlerCalls != 1 {
		t.Fatalf("Replay calls after recovery barrier = %d, want 1", handlerCalls)
	}
}

func TestAppendRetryAfterDirectorySyncFailureMustRetryDurabilityBarrier(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realSyncDir := ops.syncDir
	boom := errors.New("simulated first directory fsync failure")
	syncCalls := 0
	ops.syncDir = func(directory *os.File) error {
		syncCalls++
		if syncCalls == 2 {
			return boom
		}
		return realSyncDir(directory)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))

	if err := store.Append(context.Background(), entry); !errors.Is(err, boom) {
		t.Fatalf("first Append error = %v, want %v", err, boom)
	}
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("retry Append: %v", err)
	}
	if syncCalls != 3 {
		t.Fatalf("directory sync calls = %d, want 3 including creation; retry returned success without retrying durability", syncCalls)
	}
}

func TestAppendRejectsIDThatDoesNotMatchRouteAndBody(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	accepted := time.Unix(1_700_000_000, 0)
	id, err := NewID("tail", "hec", "flow", []byte("different"))
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	entry := Envelope{
		ID: id, Tailnet: "tail", Source: "hec", Signal: "flow",
		Accepted: accepted, Body: []byte("body"),
	}
	if err := store.Append(context.Background(), entry); err == nil {
		t.Fatal("Append accepted an ID whose digest disagrees with route/body")
	}
}

func TestOpenRejectsCorruptAndIncompatibleEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{
			name: "truncated",
			mutate: func(data []byte) []byte {
				return data[:len(data)-1]
			},
			want: ErrCorrupt,
		},
		{
			name: "checksum",
			mutate: func(data []byte) []byte {
				data[len(data)-1] ^= 0xff
				return data
			},
			want: ErrCorrupt,
		},
		{
			name: "version",
			mutate: func(data []byte) []byte {
				data[len(recordMagic)] = recordVersion + 1
				return data
			},
			want: ErrIncompatible,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "wal")
			opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
			store := mustNew(t, opts)
			entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("secret payload"))
			if err := store.Append(context.Background(), entry); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			path := filepath.Join(dir, entryName(1, entry.ID))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if err := os.WriteFile(path, tt.mutate(data), 0o600); err != nil {
				t.Fatalf("WriteFile mutation: %v", err)
			}

			_, err = New(opts)
			if !errors.Is(err, tt.want) {
				t.Fatalf("New error = %v, want wrapping %v", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret payload") {
				t.Fatalf("corruption error leaked payload: %v", err)
			}
		})
	}
}

func TestOpenNeverFollowsWALSymlinks(t *testing.T) {
	t.Parallel()

	t.Run("directory", func(t *testing.T) {
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "wal")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		_, err := New(Options{Directory: link, MaxBytes: 1 << 20, MaxEntries: 10})
		if !errors.Is(err, ErrOwnership) {
			t.Fatalf("New symlink directory error = %v, want ErrOwnership", err)
		}
	})

	t.Run("entry", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "wal")
		opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
		store := mustNew(t, opts)
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		id, err := NewID("tail", "hec", "flow", []byte("body"))
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		target := filepath.Join(t.TempDir(), "sentinel")
		if err := os.WriteFile(target, []byte("do not read"), 0o600); err != nil {
			t.Fatalf("WriteFile sentinel: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, entryName(1, id))); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		_, err = New(opts)
		if !errors.Is(err, ErrOwnership) {
			t.Fatalf("New symlink entry error = %v, want ErrOwnership", err)
		}
		got, readErr := os.ReadFile(target)
		if readErr != nil || string(got) != "do not read" {
			t.Fatalf("symlink target changed: %q/%v", got, readErr)
		}
	})
}

func TestCommitDirectorySyncFailureLeavesEntryPending(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realSyncDir := ops.syncDir
	syncCalls := 0
	boom := errors.New("simulated completion directory fsync failure")
	ops.syncDir = func(directory *os.File) error {
		syncCalls++
		if syncCalls == 3 {
			return boom
		}
		return realSyncDir(directory)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Commit(context.Background(), entry.ID); !errors.Is(err, boom) {
		t.Fatalf("Commit error = %v, want %v", err, boom)
	}
	if health := store.Health(); health.PendingEntries != 1 {
		t.Fatalf("failed completion lost pending state: %+v", health)
	}
	if _, err := os.Lstat(filepath.Join(dir, markerName(1, entry.ID))); !os.IsNotExist(err) {
		t.Fatalf("ambiguous completion marker was not rolled back: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close after failed completion: %v", err)
	}
	reopened := mustNew(t, opts)
	called := 0
	if err := reopened.Replay(context.Background(), func(context.Context, Envelope) error {
		called++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay after restored sync: %v", err)
	}
	if called != 1 {
		t.Fatalf("Replay calls = %d, want 1", called)
	}
}

func TestReplayRetriesMarkerBackedCleanupWithoutReinvokingHandler(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realRemove := ops.removeAt
	failEntryRemove := true
	boom := errors.New("simulated entry unlink failure")
	ops.removeAt = func(directory *os.File, name string) error {
		if failEntryRemove && strings.HasSuffix(name, entrySuffix) {
			failEntryRemove = false
			return boom
		}
		return realRemove(directory, name)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	handlerCalls := 0
	var observed []string
	observer := CommitObserver(func(id string) {
		observed = append(observed, id)
	})
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, observer); !errors.Is(err, boom) {
		t.Fatalf("first Replay error = %v, want cleanup error %v", err, boom)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after first Replay = %d, want 1", handlerCalls)
	}
	if len(observed) != 0 {
		t.Fatalf("observer called before marker-backed cleanup succeeded: %v", observed)
	}
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		handlerCalls++
		return nil
	}, observer); err != nil {
		t.Fatalf("cleanup Replay: %v", err)
	}
	if handlerCalls != 1 {
		t.Fatalf("cleanup Replay reinvoked handler; calls = %d, want 1", handlerCalls)
	}
	health := store.Health()
	if health.PendingEntries != 0 || health.PendingBytes != 0 || health.CompletionMarkers != 0 {
		t.Fatalf("cleanup did not release entry/marker capacity: %+v", health)
	}
	if !reflect.DeepEqual(observed, []string{entry.ID}) {
		t.Fatalf("observer IDs after cleanup retry = %v, want one %s", observed, entry.ID)
	}
}

func TestCompletionMarkerSequenceRenameFailsClosed(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realRemove := ops.removeAt
	boom := errors.New("keep marker by failing entry unlink")
	ops.removeAt = func(directory *os.File, name string) error {
		if strings.HasSuffix(name, entrySuffix) {
			return boom
		}
		return realRemove(directory, name)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Commit(context.Background(), entry.ID); !errors.Is(err, boom) {
		t.Fatalf("Commit error = %v, want %v", err, boom)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	oldName := filepath.Join(dir, markerName(1, entry.ID))
	newName := filepath.Join(dir, markerName(9, entry.ID))
	if err := os.Rename(oldName, newName); err != nil {
		t.Fatalf("rename marker sequence: %v", err)
	}
	reopened, err := New(opts)
	if reopened != nil {
		_ = reopened.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("New after valid-format marker sequence rename = %v, want ErrCorrupt", err)
	}
}

func TestRestartFinishesMarkerBackedCleanupWithoutReinvokingHandler(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	ops := realFileOps
	realRemove := ops.removeAt
	boom := errors.New("keep marker and entry for restart")
	failEntryRemove := true
	ops.removeAt = func(directory *os.File, name string) error {
		if failEntryRemove && strings.HasSuffix(name, entrySuffix) {
			failEntryRemove = false
			return boom
		}
		return realRemove(directory, name)
	}
	store, err := newStore(opts, ops)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Commit(context.Background(), entry.ID); !errors.Is(err, boom) {
		t.Fatalf("Commit error = %v, want %v", err, boom)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := mustNew(t, opts)
	calls := 0
	if err := reopened.Replay(context.Background(), func(context.Context, Envelope) error {
		calls++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay cleanup after restart: %v", err)
	}
	if calls != 0 {
		t.Fatalf("Replay reinvoked handler after durable completion marker: %d calls", calls)
	}
	if health := reopened.Health(); health.PendingEntries != 0 || health.CompletionMarkers != 0 {
		t.Fatalf("restart cleanup retained transient state: %+v", health)
	}
	if err := reopened.Append(context.Background(), entry); err != nil {
		t.Fatalf("later identical append after restart cleanup: %v", err)
	}
}

func TestAppendSameIDIsIdempotentOnlyForExactEnvelope(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte{0, 1, 2, 255})
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	retry := entry
	retry.Accepted = retry.Accepted.Add(time.Hour)
	if err := store.Append(context.Background(), retry); err != nil {
		t.Fatalf("idempotent Append: %v", err)
	}
	var replayed Envelope
	stop := errors.New("stop")
	if err := store.Replay(context.Background(), func(_ context.Context, got Envelope) error {
		replayed = got
		return stop
	}, nil); !errors.Is(err, stop) {
		t.Fatalf("Replay error = %v, want %v", err, stop)
	}
	if !replayed.Accepted.Equal(entry.Accepted) {
		t.Fatalf("pending retry replaced first durable Accepted: got %v, want %v", replayed.Accepted, entry.Accepted)
	}
	different := entry
	different.Body = []byte("different")
	if err := store.Append(context.Background(), different); err == nil {
		t.Fatal("same ID with different body was accepted")
	}
	if health := store.Health(); health.PendingEntries != 1 {
		t.Fatalf("duplicate append changed entry count: %+v", health)
	}
}

func TestSameAcceptedTimeReplaysInTrueAppendOrder(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	accepted := time.Unix(1_700_000_000, 123)
	first := testEnvelope(t, accepted, "tail", "hec", "flow", []byte("z-first"))
	second := testEnvelope(t, accepted, "tail", "hec", "flow", []byte("a-second"))
	third := testEnvelope(t, accepted, "tail", "hec", "flow", []byte("m-third"))
	for _, entry := range []Envelope{first, second, third} {
		if err := store.Append(context.Background(), entry); err != nil {
			t.Fatalf("Append %q: %v", entry.Body, err)
		}
	}
	var got []string
	if err := store.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		got = append(got, string(envelope.Body))
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := []string{"z-first", "a-second", "m-third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Replay order = %v, want append FIFO %v", got, want)
	}
}

func TestSuccessfulCompletionMarkerIsTransientAndDuplicateAppendsAgain(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 2}
	store := mustNew(t, opts)
	first := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("first"))
	if err := store.Append(context.Background(), first); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Commit(context.Background(), first.ID); err != nil {
		t.Fatalf("Commit first: %v", err)
	}
	if health := store.Health(); health.PendingEntries != 0 || health.CompletionMarkers != 0 {
		t.Fatalf("successful completion retained transient state: %+v", health)
	}
	retry := first
	retry.Accepted = retry.Accepted.Add(time.Hour)
	if err := store.Append(context.Background(), retry); err != nil {
		t.Fatalf("completed retry: %v", err)
	}
	if got := store.Health().PendingEntries; got != 1 {
		t.Fatalf("later identical external batch pending = %d, want 1", got)
	}
}

func TestReplaySkipsInProcessEntryUntilAppendDurabilityRetry(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	realSync := store.ops.syncDir
	boom := errors.New("simulated append directory fsync ambiguity")
	store.ops.syncDir = func(*os.File) error { return boom }
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); !errors.Is(err, boom) {
		t.Fatalf("Append error = %v, want %v", err, boom)
	}
	calls := 0
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		calls++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay while append durability ambiguous: %v", err)
	}
	if calls != 0 {
		t.Fatalf("Replay handled non-durable in-process entry %d times", calls)
	}
	store.ops.syncDir = realSync
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("exact Append durability retry: %v", err)
	}
	if err := store.Replay(context.Background(), func(context.Context, Envelope) error {
		calls++
		return nil
	}, nil); err != nil {
		t.Fatalf("Replay after durability retry: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Replay calls after durability retry = %d, want 1", calls)
	}
}

func TestConcurrentReplayInvokesHandlerOncePerEntry(t *testing.T) {
	t.Parallel()

	store := mustNew(t, Options{
		Directory: filepath.Join(t.TempDir(), "wal"),
		MaxBytes:  1 << 20, MaxEntries: 10,
	})
	entry := testEnvelope(t, time.Unix(1_700_000_000, 0), "tail", "hec", "flow", []byte("body"))
	if err := store.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	firstEntered := make(chan struct{})
	release := make(chan struct{})
	errs := make(chan error, 2)
	var firstCalls, secondCalls int
	var firstEnteredOnce sync.Once
	firstHandler := func(context.Context, Envelope) error {
		firstCalls++
		firstEnteredOnce.Do(func() { close(firstEntered) })
		<-release
		return nil
	}
	secondHandler := func(context.Context, Envelope) error {
		secondCalls++
		<-release
		return nil
	}
	go func() { errs <- store.Replay(context.Background(), firstHandler, nil) }()
	<-firstEntered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		errs <- store.Replay(context.Background(), secondHandler, nil)
	}()
	// The competing Replay has been launched while the first handler is held.
	// Release is the explicit barrier; joining both calls below then proves the
	// first entry was handled once and the serialized second call saw no entry.
	<-secondStarted
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Replay: %v", err)
		}
	}
	if firstCalls != 1 {
		t.Fatalf("first Replay handler calls = %d, want 1", firstCalls)
	}
	if secondCalls != 0 {
		t.Fatalf("concurrent Replay invoked the handler %d extra times for one entry", secondCalls)
	}
}

func TestOpenFailsClosedOnMalformedOwnedNames(t *testing.T) {
	t.Parallel()

	tests := []string{
		"not-a-valid-entry.wal",
		completionPrefix + "not-a-valid-marker" + completionSuffix,
		appendStagePrefix + stageSuffix,
		doneStagePrefix + "not-hex" + stageSuffix,
	}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "wal")
			opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
			store := mustNew(t, opts)
			if err := store.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("owned namespace"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			reopened, err := New(opts)
			if reopened != nil {
				_ = reopened.Close()
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("New with malformed owned name %q = %v, want ErrCorrupt", name, err)
			}
		})
	}
}

func TestOpenRemovesEveryExactStageImmediatelyAndRejectsStageSymlink(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "wal")
	opts := Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
	store := mustNew(t, opts)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stage := filepath.Join(dir, appendStagePrefix+strings.Repeat("a", 32)+stageSuffix)
	if err := os.WriteFile(stage, []byte("fresh torn write"), 0o600); err != nil {
		t.Fatalf("WriteFile stage: %v", err)
	}
	reopened := mustNew(t, opts)
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("fresh exact stage was not removed immediately: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close reopened: %v", err)
	}

	target := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(target, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}
	stageLink := filepath.Join(dir, doneStagePrefix+strings.Repeat("b", 32)+stageSuffix)
	if err := os.Symlink(target, stageLink); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	unsafe, err := New(opts)
	if unsafe != nil {
		_ = unsafe.Close()
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("New with exact stage symlink = %v, want ErrOwnership", err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "sentinel" {
		t.Fatalf("stage symlink target = %q/%v, want untouched", got, readErr)
	}
}

func mustNew(t *testing.T, opts Options) *Store {
	t.Helper()
	store, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertNoOwnedStagesOrEntries(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if inStageNamespace(entry.Name()) || strings.HasSuffix(entry.Name(), entrySuffix) ||
			strings.HasPrefix(entry.Name(), completionPrefix) {
			t.Fatalf("unexpected WAL state file %q", entry.Name())
		}
	}
}

func testEnvelope(t *testing.T, accepted time.Time, tailnet, source, signal string, body []byte) Envelope {
	t.Helper()
	id, err := NewID(tailnet, source, signal, body)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return Envelope{
		ID:       id,
		Tailnet:  tailnet,
		Source:   source,
		Signal:   signal,
		Accepted: accepted.UTC(),
		Body:     append([]byte(nil), body...),
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func assertPendingIDs(t *testing.T, store *Store, want []string) {
	t.Helper()
	var got []string
	err := store.Replay(context.Background(), func(_ context.Context, envelope Envelope) error {
		got = append(got, envelope.ID)
		return errors.New("stop before commit")
	}, nil)
	if err == nil {
		t.Fatal("Replay unexpectedly succeeded")
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("first pending replay ID = %v, want first of %v", got, want)
	}
}
