package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestKubernetesCheckpointStore_CoalescesSetsAndFlushes(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client,
		WithKubernetesCheckpointWriteDebounce(time.Hour))
	if err != nil {
		t.Fatalf("NewKubernetesCheckpointStore: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.Set("tailnet/cursor", time.Unix(int64(i+1), 0).UTC()); err != nil {
				t.Errorf("Set(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := client.updateCalls(); got != 0 {
		t.Fatalf("updates before Flush = %d, want 0", got)
	}
	if err := flushCheckpoint(t, store); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := client.updateCalls(); got != 1 {
		t.Fatalf("updates after Flush = %d, want 1", got)
	}

	reopened, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := reopened.Get("tailnet/cursor"); !ok || got.IsZero() {
		t.Fatalf("reopened cursor = %v/%v, want persisted cursor", got, ok)
	}
}

func TestKubernetesCheckpointStore_StaleResourceVersionFailsWithoutClobbering(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	leader, err := NewKubernetesCheckpointStore(context.Background(), client,
		WithKubernetesCheckpointWriteDebounce(time.Hour))
	if err != nil {
		t.Fatalf("New leader store: %v", err)
	}
	deposed, err := NewKubernetesCheckpointStore(context.Background(), client,
		WithKubernetesCheckpointWriteDebounce(time.Hour))
	if err != nil {
		t.Fatalf("New deposed store: %v", err)
	}

	current := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := leader.Set("tailnet/flowlogs", current); err != nil {
		t.Fatalf("leader Set: %v", err)
	}
	if err := flushCheckpoint(t, leader); err != nil {
		t.Fatalf("leader Flush: %v", err)
	}

	if err := deposed.Set("tailnet/flowlogs", current.Add(-time.Hour)); err != nil {
		t.Fatalf("deposed Set: %v", err)
	}
	err = flushCheckpoint(t, deposed)
	if !errors.Is(err, ErrKubernetesCheckpointConflict) {
		t.Fatalf("deposed Flush error = %v, want visible ErrKubernetesCheckpointConflict", err)
	}

	fresh, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("New fresh store: %v", err)
	}
	if got, ok := fresh.Get("tailnet/flowlogs"); !ok || !got.Equal(current) {
		t.Fatalf("cursor after stale write = %v/%v, want winning leader cursor %v", got, ok, current)
	}
}

func TestKubernetesCheckpointStore_HandoverUsesPersistedCursorNotInitialLookback(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	leader, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("New leader store: %v", err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * time.Minute)
	if err := leader.Set("flowlogs", last); err != nil {
		t.Fatalf("leader Set: %v", err)
	}
	if err := flushCheckpoint(t, leader); err != nil {
		t.Fatalf("leader Flush: %v", err)
	}

	failover, err := NewKubernetesCheckpointStore(context.Background(), client)
	if err != nil {
		t.Fatalf("New failover store: %v", err)
	}
	window := &replayWindow{name: "flowlogs", returnHWM: now}
	scheduler := NewScheduler(telemetrytest.New().Emitter(), failover,
		WithClock(func() time.Time { return now }))
	if err := scheduler.runWindow(context.Background(), window, Entry{
		Collector:       window,
		InitialLookback: time.Hour,
		MaxWindow:       time.Hour,
	}); err != nil {
		t.Fatalf("failover runWindow: %v", err)
	}
	if !window.from.Equal(last) {
		t.Fatalf("failover window starts at %v, want persisted cursor %v rather than initial lookback", window.from, last)
	}
}

func TestKubernetesCheckpointStore_RejectsMapBeyondConfigMapDataLimit(t *testing.T) {
	client := &fakeKubernetesCheckpointClient{}
	store, err := NewKubernetesCheckpointStore(context.Background(), client,
		WithKubernetesCheckpointWriteDebounce(0))
	if err != nil {
		t.Fatalf("NewKubernetesCheckpointStore: %v", err)
	}
	err = store.Set(strings.Repeat("x", KubernetesCheckpointDataLimit), time.Unix(1, 0).UTC())
	if !errors.Is(err, ErrKubernetesCheckpointTooLarge) {
		t.Fatalf("Set oversize map error = %v, want ErrKubernetesCheckpointTooLarge", err)
	}
	if got := client.updateCalls(); got != 0 {
		t.Fatalf("updates after rejected oversize map = %d, want 0", got)
	}
}

// TestKubernetesCheckpointStore_SizeEvidence pins the storage decision's
// capacity evidence. The ordinary map is the two polling cursor rows
// (flowlogs and auditlogs) for two tailnets. The other two maps model only
// their configured bounded cardinalities: 131072 replay hashes
// for one poll tailnet, and three object-store seen sets of 5000 opaque IDs.
// The latter IDs have no application-level byte cap, so their real payload can
// be larger than this fixed-width synthetic sample.
func TestKubernetesCheckpointStore_SizeEvidence(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ordinary := map[string]time.Time{}
	for _, tailnet := range []string{"tailnet-a", "tailnet-b"} {
		for _, name := range []string{"flowlogs", "auditlogs"} {
			ordinary[tailnet+"/"+name] = at
		}
	}
	ordinaryBytes := checkpointJSONSize(t, ordinary)
	if ordinaryBytes >= KubernetesCheckpointDataLimit {
		t.Fatalf("two-tailnet ordinary cursor map = %d bytes, want below ConfigMap limit %d", ordinaryBytes, KubernetesCheckpointDataLimit)
	}

	replay := map[string]time.Time{}
	for i := range 131072 { // collectors.flowlogs.replay_seen_capacity default
		replay[fmt.Sprintf("tailnet-a/flowlogs/replay/seen/%064x", i)] = at
	}
	replayBytes := checkpointJSONSize(t, replay)
	if replayBytes <= KubernetesCheckpointDataLimit {
		t.Fatalf("full replay map = %d bytes, want above ConfigMap limit %d", replayBytes, KubernetesCheckpointDataLimit)
	}

	objectstore := map[string]time.Time{}
	for _, signal := range []string{"flow", "audit", "k8s_audit"} {
		for i := range 5000 { // each object-store destination's max_seen_keys default
			objectstore[fmt.Sprintf("tailnet-a/%s/seen/%064x", signal, i)] = at
		}
	}
	objectstoreBytes := checkpointJSONSize(t, objectstore)
	if objectstoreBytes <= KubernetesCheckpointDataLimit {
		t.Fatalf("full object-store seen map = %d bytes, want above ConfigMap limit %d", objectstoreBytes, KubernetesCheckpointDataLimit)
	}
	t.Logf("ConfigMap capacity evidence: ordinary two-tailnet cursors=%d B; one-tailnet replay bound=%d B; one-tailnet object-store seen bound=%d B; ConfigMap data limit=%d B", ordinaryBytes, replayBytes, objectstoreBytes, KubernetesCheckpointDataLimit)
}

func checkpointJSONSize(t *testing.T, m map[string]time.Time) int {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal checkpoint map: %v", err)
	}
	return len(data)
}

func TestFileStore_RemainsSynchronousByDefault(t *testing.T) {
	path := t.TempDir() + "/checkpoints.json"
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Set("flowlogs", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := reopened.Get("flowlogs"); !ok || !got.Equal(want) {
		t.Fatalf("reopened checkpoint = %v/%v, want %v", got, ok, want)
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
	mu      sync.Mutex
	object  KubernetesCheckpointObject
	exists  bool
	updates int
}

func (c *fakeKubernetesCheckpointClient) GetCheckpoint(context.Context) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.exists {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointNotFound
	}
	return c.object, nil
}

func (c *fakeKubernetesCheckpointClient) CreateCheckpoint(_ context.Context, object KubernetesCheckpointObject) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exists {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointAlreadyExists
	}
	object.ResourceVersion = "1"
	c.object = object
	c.exists = true
	return c.object, nil
}

func (c *fakeKubernetesCheckpointClient) UpdateCheckpoint(_ context.Context, object KubernetesCheckpointObject) (KubernetesCheckpointObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.exists || object.ResourceVersion != c.object.ResourceVersion {
		return KubernetesCheckpointObject{}, ErrKubernetesCheckpointConflict
	}
	c.updates++
	object.ResourceVersion = string(rune('1' + c.updates))
	c.object = object
	return c.object, nil
}

func (c *fakeKubernetesCheckpointClient) updateCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updates
}
