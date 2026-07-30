package objectstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	storeapi "github.com/rknightion/tailscale2otel/v4/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

type atomicityBackend struct {
	object storeapi.Object
	body   []byte
}

func (b *atomicityBackend) List(
	context.Context,
	string,
	string,
	int,
) (storeapi.ListResult, error) {
	return storeapi.ListResult{Objects: []storeapi.Object{b.object}}, nil
}

func (b *atomicityBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

type atomicSignal struct {
	prepareOrder []string
	commitOrder  []string
	failOn       string
	cancelOn     string
	cancel       context.CancelFunc
}

func (*atomicSignal) Signal() string { return "flow" }

func (s *atomicSignal) PrepareRecord(
	_ context.Context,
	line []byte,
	_ time.Time,
) (PreparedRecord, error) {
	row := string(line)
	s.prepareOrder = append(s.prepareOrder, row)
	if row == s.cancelOn {
		s.cancel()
	}
	switch row {
	case s.failOn:
		return atomicPrepared{s: s, row: "ignored"}, errors.New("late preparation failure")
	case "decode":
		return nil, errors.Join(errors.New("malformed row"), ErrRecordDecode)
	case "invalid":
		return atomicPrepared{s: s, row: row},
			errors.Join(errors.New("invalid row"), ErrRecordInvalid)
	case "dual_nil":
		return nil, errors.Join(ErrRecordDecode, ErrRecordInvalid)
	case "dual_prepared":
		return atomicPrepared{s: s, row: row},
			errors.Join(ErrRecordDecode, ErrRecordInvalid)
	default:
		return atomicPrepared{s: s, row: row}, nil
	}
}

type atomicPrepared struct {
	s   *atomicSignal
	row string
}

func (p atomicPrepared) Commit(e telemetry.Emitter) RecordTimestamps {
	p.s.commitOrder = append(p.s.commitOrder, p.row)
	e.Counter("test.atomicity.committed", "{record}", "test-only commit counter", 1, telemetry.Attrs{})
	if p.row == "invalid" {
		e.Counter("test.atomicity.invalid", "{record}", "test-only invalid counter", 1, telemetry.Attrs{})
		return RecordTimestamps{}
	}
	return RecordTimestamps{EventTime: time.Unix(int64(len(p.row)), 0)}
}

func atomicityOptions(at *time.Time) Options {
	return Options{
		Prefix: "flow",
		Now:    func() time.Time { return *at },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Scope: CheckpointScope{
			Tailnet:  "tailnet.example",
			Provider: "test",
			Signal:   "flow",
			Feed:     FeedID("atomicity"),
		},
	}
}

func TestIngestLatePreparationFailureDiscardsEveryRowEffect(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	backend := &atomicityBackend{
		object: storeapi.Object{
			Identity: "opaque-id",
			Key:      "flow/2026/07/26/11:55:00.json",
		},
		body: []byte("first\ninvalid\ndecode\nfail\n"),
	}
	signal := &atomicSignal{failOn: "fail"}
	cp := collector.NewMemoryStore()
	var accepted, ingested int
	opts := atomicityOptions(&at)
	opts.OnAccepted = func(ingest.AcceptedEvent) { accepted++ }
	opts.OnIngest = func(string, string, int, int) { ingested++ }
	col, err := New(backend, signal, cp, opts)
	if err != nil {
		t.Fatal(err)
	}
	rec := telemetrytest.New()
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}

	if got := signal.commitOrder; len(got) != 0 {
		t.Fatalf("commit order = %v, want no commits after late preparation failure", got)
	}
	if accepted != 0 || ingested != 0 {
		t.Fatalf("accepted/ingested = %d/%d, want 0/0", accepted, ingested)
	}
	for _, name := range []string{"test.atomicity.committed", "test.atomicity.invalid"} {
		if got := len(rec.MetricPoints(name)); got != 0 {
			t.Fatalf("%s points = %d, want 0", name, got)
		}
	}
	for _, point := range rec.MetricPoints(docSkipped.Name) {
		if reason := point.Attrs[attrReason]; reason == reasonDecodeError || reason == reasonSemanticInvalid {
			t.Fatalf("row-local summary emitted before atomic preparation completed: %+v", point)
		}
	}
	var gap, seen bool
	for _, checkpointKey := range cp.Keys() {
		gap = gap || strings.Contains(checkpointKey, "/gap/")
		seen = seen || strings.Contains(checkpointKey, "/seen/")
	}
	if !gap || seen {
		t.Fatalf("checkpoint state gap=%v seen=%v, want a durable gap and no seen identity", gap, seen)
	}
}

func TestIngestPreparesAllRowsBeforeOrderedCommit(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	signal := &atomicSignal{}
	backend := &atomicityBackend{body: []byte("first\ninvalid\ndecode\nlast\n")}
	var accepted []ingest.AcceptedEvent
	col := &Collector{
		api:    backend,
		signal: signal,
		opts: Options{
			MaxObjectWireBytes:         1 << 20,
			MaxObjectDecompressedBytes: 1 << 20,
			MaxObjectRecords:           10,
			OnAccepted: func(event ingest.AcceptedEvent) {
				if len(signal.prepareOrder) != 4 {
					t.Errorf("OnAccepted ran after %d prepares, want all 4", len(signal.prepareOrder))
				}
				if len(signal.commitOrder) == 0 ||
					signal.commitOrder[len(signal.commitOrder)-1] == "invalid" {
					t.Errorf("OnAccepted ran before its accepted commit: %v", signal.commitOrder)
				}
				accepted = append(accepted, event)
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:    func() time.Time { return at },
	}
	rec := telemetrytest.New()
	result, err := col.ingest(context.Background(), "opaque-id", compNone, rec.Emitter(), ingestLimits{
		wireBytes:         1 << 20,
		decompressedBytes: 1 << 20,
		records:           10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := signal.prepareOrder, []string{"first", "invalid", "decode", "last"}; !slices.Equal(got, want) {
		t.Fatalf("prepare order = %v, want %v", got, want)
	}
	if got, want := signal.commitOrder, []string{"first", "invalid", "last"}; !slices.Equal(got, want) {
		t.Fatalf("commit order = %v, want %v", got, want)
	}
	if result.acceptedRecords != 2 || len(accepted) != 2 {
		t.Fatalf("accepted result/events = %d/%d, want 2/2", result.acceptedRecords, len(accepted))
	}
	if got := len(rec.MetricPoints("test.atomicity.invalid")); got != 1 {
		t.Fatalf("deferred invalid points = %d, want 1", got)
	}
	reasons := map[string]float64{}
	for _, point := range rec.MetricPoints(docSkipped.Name) {
		reasons[point.Attrs[attrReason]] += point.Value
	}
	if reasons[reasonDecodeError] != 1 || reasons[reasonSemanticInvalid] != 1 {
		t.Fatalf("row-local summary reasons = %v, want one decode and one semantic invalid", reasons)
	}
}

func TestIngestCancellationBeforeCommitDiscardsPreparedRows(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	signal := &atomicSignal{cancelOn: "last", cancel: cancel}
	col := &Collector{
		api:    &atomicityBackend{body: []byte("first\nlast\n")},
		signal: signal,
		opts: Options{
			MaxObjectWireBytes:         1 << 20,
			MaxObjectDecompressedBytes: 1 << 20,
			MaxObjectRecords:           10,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:    func() time.Time { return at },
	}
	rec := telemetrytest.New()
	_, err := col.ingest(ctx, "opaque-id", compNone, rec.Emitter(), ingestLimits{
		wireBytes:         1 << 20,
		decompressedBytes: 1 << 20,
		records:           10,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ingest error = %v, want context cancellation", err)
	}
	if len(signal.commitOrder) != 0 || len(rec.MetricPoints("test.atomicity.committed")) != 0 {
		t.Fatalf("commit order/points = %v/%d, want no row effects",
			signal.commitOrder, len(rec.MetricPoints("test.atomicity.committed")))
	}
}

func TestDualRowLocalSentinelsFailTheObjectAndRemainRetryable(t *testing.T) {
	for _, dualRow := range []string{"dual_nil", "dual_prepared"} {
		t.Run(dualRow, func(t *testing.T) {
			at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
			backend := &atomicityBackend{
				object: storeapi.Object{
					Identity: "opaque-dual-id",
					Key:      "flow/2026/07/26/11:55:00.json",
				},
				body: []byte("first\n" + dualRow + "\nlast\n"),
			}
			cp := collector.NewMemoryStore()
			signal := &atomicSignal{}
			var accepted, ingested int
			opts := atomicityOptions(&at)
			opts.OnAccepted = func(ingest.AcceptedEvent) { accepted++ }
			opts.OnIngest = func(string, string, int, int) { ingested++ }
			col, err := New(backend, signal, cp, opts)
			if err != nil {
				t.Fatal(err)
			}
			rec := telemetrytest.New()
			if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}

			if len(signal.commitOrder) != 0 || accepted != 0 || ingested != 0 {
				t.Fatalf("commits/accepted/ingested = %v/%d/%d, want no row effects",
					signal.commitOrder, accepted, ingested)
			}
			for _, point := range rec.MetricPoints(docSkipped.Name) {
				if reason := point.Attrs[attrReason]; reason == reasonDecodeError ||
					reason == reasonSemanticInvalid {
					t.Fatalf("dual-sentinel row received a row-local disposition: %+v", point)
				}
			}
			var gap, seen bool
			for _, checkpointKey := range cp.Keys() {
				gap = gap || strings.Contains(checkpointKey, "/gap/")
				seen = seen || strings.Contains(checkpointKey, "/seen/")
			}
			if !gap || seen {
				t.Fatalf("checkpoint state gap=%v seen=%v, want retryable gap and no seen identity", gap, seen)
			}

			// Recreate the collector after its retry deadline with a corrected
			// processor that accepts the same row. The durable gap must remain
			// eligible and resolve without requiring listing rediscovery.
			at = at.Add(2 * time.Minute)
			corrected := &acceptAllAtomicSignal{}
			restarted, err := New(backend, corrected, cp, opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			if corrected.commits != 3 || accepted != 3 || ingested != 1 {
				t.Fatalf("retry commits/accepted/ingested = %d/%d/%d, want 3/3/1",
					corrected.commits, accepted, ingested)
			}
			for _, checkpointKey := range cp.Keys() {
				if strings.Contains(checkpointKey, "/gap/") {
					t.Fatalf("resolved retry gap remains: %q", checkpointKey)
				}
			}
		})
	}
}

func TestDualRowLocalSentinelInvariantErrorIsBounded(t *testing.T) {
	const rawRow = "customer-private-payload"
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	col := &Collector{
		api:    &atomicityBackend{body: []byte(rawRow + "\n")},
		signal: dualErrorSignal{},
		opts: Options{
			MaxObjectWireBytes:         1 << 20,
			MaxObjectDecompressedBytes: 1 << 20,
			MaxObjectRecords:           10,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:    func() time.Time { return at },
	}
	_, err := col.ingest(
		context.Background(),
		"opaque-id",
		compNone,
		telemetrytest.New().Emitter(),
		ingestLimits{wireBytes: 1 << 20, decompressedBytes: 1 << 20, records: 10},
	)
	if err == nil || !strings.Contains(err.Error(), "matched both row-local sentinels") {
		t.Fatalf("invariant error = %v, want bounded dual-sentinel diagnosis", err)
	}
	if strings.Contains(err.Error(), rawRow) {
		t.Fatalf("invariant error exposed raw row: %v", err)
	}
	if errors.Is(err, ErrRecordDecode) || errors.Is(err, ErrRecordInvalid) {
		t.Fatalf("invariant error remained classifiable as a row-local disposition: %v", err)
	}
}

type dualErrorSignal struct{}

func (dualErrorSignal) Signal() string { return "flow" }

func (dualErrorSignal) PrepareRecord(
	context.Context,
	[]byte,
	time.Time,
) (PreparedRecord, error) {
	return nil, errors.Join(ErrRecordDecode, ErrRecordInvalid)
}

type acceptAllAtomicSignal struct {
	commits int
}

func (*acceptAllAtomicSignal) Signal() string { return "flow" }

func (s *acceptAllAtomicSignal) PrepareRecord(
	context.Context,
	[]byte,
	time.Time,
) (PreparedRecord, error) {
	return acceptAllAtomicPrepared{s: s}, nil
}

type acceptAllAtomicPrepared struct {
	s *acceptAllAtomicSignal
}

func (p acceptAllAtomicPrepared) Commit(telemetry.Emitter) RecordTimestamps {
	p.s.commits++
	return RecordTimestamps{}
}

type failAfterSignal struct {
	delegate SignalProcessor
	failAt   int
	calls    int
}

func (s *failAfterSignal) Signal() string { return s.delegate.Signal() }

func (s *failAfterSignal) PrepareRecord(
	ctx context.Context,
	line []byte,
	now time.Time,
) (PreparedRecord, error) {
	s.calls++
	if s.calls == s.failAt {
		return atomicPrepared{s: &atomicSignal{}, row: "ignored"},
			errors.New("injected late preparation failure")
	}
	return s.delegate.PrepareRecord(ctx, line, now)
}

type countingFlowStore struct {
	observations []flowstore.Observation
}

func (s *countingFlowStore) Record(observation flowstore.Observation) {
	s.observations = append(s.observations, observation)
}

type countingResolver struct {
	calls int
}

func (r *countingResolver) LookupName(netip.Addr) (string, bool) {
	r.calls++
	return "external.example", true
}

func atomicFlowRecord(nodeID string, srcPort int, at time.Time) []byte {
	body, _ := json.Marshal(map[string]any{
		"nodeId": nodeID,
		"start":  at.Format(time.RFC3339),
		"end":    at.Add(5 * time.Second).Format(time.RFC3339),
		"logged": at.Add(7 * time.Second).Format(time.RFC3339),
		"srcNode": map[string]any{
			"nodeId":    nodeID,
			"name":      "source.tailnet.ts.net",
			"addresses": []string{"100.64.0.1"},
		},
		"virtualTraffic": []map[string]any{{
			"proto": 6,
			"src": netip.AddrPortFrom(
				netip.MustParseAddr("100.64.0.1"),
				uint16(srcPort), //nolint:gosec // bounded synthetic fixture
			).String(),
			"dst":     "8.8.8.8:443",
			"txBytes": 1000,
			"rxBytes": 800,
			"txPkts":  10,
			"rxPkts":  8,
		}},
	})
	return body
}

func TestLatePreparationFailureLeavesRealFlowProcessorUntouchedUntilRestartRetry(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	first := atomicFlowRecord("node-one", 41000, at.Add(-5*time.Minute))
	second := atomicFlowRecord("node-two", 41001, at.Add(-4*time.Minute))
	body := bytes.Join([][]byte{first, second, []byte("{malformed")}, []byte("\n"))
	body = append(body, '\n')
	backend := &atomicityBackend{
		object: storeapi.Object{
			Identity: "opaque-retry-id",
			Key:      "flow/2026/07/26/11:55:00.json",
		},
		body: body,
	}
	cp := collector.NewMemoryStore()
	cache := enrich.NewDeviceCache()
	store := &countingFlowStore{}
	resolver := &countingResolver{}
	proc := flowlog.NewProcessor(cache, flowlog.Options{
		Dedup:           dedup.New(16),
		RDNS:            resolver,
		Store:           store,
		FlowMetricsMode: "both",
		NodeDims:        true,
	})
	rec := telemetrytest.New()
	var accepted, ingested int
	opts := atomicityOptions(&at)
	opts.OnAccepted = func(ingest.AcceptedEvent) { accepted++ }
	opts.OnIngest = func(source, signal string, records, _ int) {
		if source != semconv.IngestSourceObjectStore || signal != semconv.IngestSignalFlow || records != 2 {
			t.Errorf("OnIngest = (%q,%q,%d), want (objectstore,flow,2)", source, signal, records)
		}
		ingested++
	}
	failing := &failAfterSignal{delegate: NewFlowSignal(proc), failAt: 3}
	col, err := New(backend, failing, cp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	proc.FlushRollup(rec.Emitter())

	if len(store.observations) != 0 || resolver.calls != 0 || accepted != 0 || ingested != 0 {
		t.Fatalf("pre-retry store/rdns/accepted/ingested = %d/%d/%d/%d, want all zero",
			len(store.observations), resolver.calls, accepted, ingested)
	}
	if name, _ := cache.ResolveNameAny("100.64.0.1:41000"); name != "unknown" {
		t.Fatalf("cache resolved %q before commit, want unknown", name)
	}
	for _, name := range []string{flowlog.MetricIO, flowlog.MetricFlows, flowlog.MetricIORollup} {
		if got := len(rec.MetricPoints(name)); got != 0 {
			t.Fatalf("%s points before retry = %d, want 0", name, got)
		}
	}

	// A fresh collector over the durable checkpoint store models process
	// restart. The pending gap is retried before listing and the malformed row
	// remains a row-local decode rejection.
	at = at.Add(2 * time.Minute)
	restarted, err := New(backend, NewFlowSignal(proc), cp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	proc.FlushRollup(rec.Emitter())

	if len(store.observations) != 2 || resolver.calls != 2 || accepted != 2 || ingested != 1 {
		t.Fatalf("post-retry store/rdns/accepted/ingested = %d/%d/%d/%d, want 2/2/2/1",
			len(store.observations), resolver.calls, accepted, ingested)
	}
	if name, _ := cache.ResolveNameAny("100.64.0.1:41000"); name != "source" {
		t.Fatalf("cache resolved %q after commit, want source", name)
	}
	if got := len(rec.MetricPoints(flowlog.MetricIO)); got == 0 {
		t.Fatal("retry emitted no real flow metrics")
	}
	for _, checkpointKey := range cp.Keys() {
		if strings.Contains(checkpointKey, "/gap/") {
			t.Fatalf("resolved gap remains after retry: %q", checkpointKey)
		}
	}

	// The shared processor's dedup set was populated only by the successful
	// retry. Reprocessing the same record cannot reach rDNS or the store.
	var decoded flowlog.FlowLog
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	proc.Process(decoded, rec.Emitter())
	if len(store.observations) != 2 || resolver.calls != 2 {
		t.Fatalf("duplicate changed store/rdns to %d/%d, want 2/2",
			len(store.observations), resolver.calls)
	}
}
