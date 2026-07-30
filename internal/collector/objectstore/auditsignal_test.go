package objectstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/ingest"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// auditKeyAt builds a configuration-log export key for a given instant, in the
// layout Tailscale's own S3 publisher writes (verified live 2026-07-27, #288):
// <prefix>/YYYY/MM/DD/YYYY-MM-DD-HH-MM-SS.ndjson.
func auditKeyAt(at time.Time, ext string) string {
	return "audit/" + at.UTC().Format("2006/01/02") + "/" + at.UTC().Format("2006-01-02-15-04-05") + ext
}

// newAuditHarness wires an object-store collector to the SHARED audit processor,
// so a test proves objects reach the same emission path poll, stream and webhook
// use rather than a parallel one.
func newAuditHarness(t *testing.T, tune func(*objectstore.Options)) *harness {
	t.Helper()
	h := &harness{store: newFakeStore(), cp: collector.NewMemoryStore(), rec: telemetrytest.New()}
	opts := objectstore.Options{
		Prefix: "audit",
		Now:    func() time.Time { return now },
		Logger: discardLogger(),
		Scope: objectstore.CheckpointScope{
			Tailnet:  "test.example",
			Provider: "s3",
			Signal:   semconv.IngestSignalAudit,
			Feed:     objectstore.FeedID("test", "audit"),
		},
	}
	if tune != nil {
		tune(&opts)
	}
	col, err := objectstore.New(h.store, objectstore.NewAuditSignal(audit.NewProcessor()), h.cp, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.col = col
	return h
}

// The fixture is a REAL Tailscale configuration-log export object, captured from
// a live tailnet on 2026-07-27 and sanitized of lab identifiers only (#288). It
// is the evidence the audit object-store path was blocked on: every record in
// the export carries its own eventTime, so no delivery envelope is needed to
// place an event in time.
func TestAuditSignal_RealExportObjectReachesTheSharedProcessor(t *testing.T) {
	fixture, err := os.ReadFile("testdata/audit_export_real.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var accepted []ingest.AcceptedEvent
	h := newAuditHarness(t, func(o *objectstore.Options) {
		o.OnAccepted = func(ev ingest.AcceptedEvent) { accepted = append(accepted, ev) }
	})
	h.store.put(auditKeyAt(now.Add(-10*time.Minute), ".ndjson"), fixture)

	h.collect(t)

	// One log record per event in the fixture: nothing was dropped as
	// undecodable, and nothing was silently skipped for want of a timestamp.
	const wantRecords = 20
	if got := len(h.rec.LogRecords()); got != wantRecords {
		t.Errorf("audit log records = %d, want %d — the real export did not decode cleanly "+
			"through the shared audit processor", got, wantRecords)
	}
	if len(h.store.fetched) != 1 {
		t.Errorf("fetched = %v, want the one object", h.store.fetched)
	}
	// The record is timestamped with the export's eventTime (when the change
	// happened), NOT its logged value (when the publisher batched it). The two
	// differ by ~2.4s in the fixture, so this pins the right one.
	wantAt := time.Date(2026, 7, 27, 12, 10, 27, 129480593, time.UTC)
	notAt := time.Date(2026, 7, 27, 12, 10, 29, 541584437, time.UTC)
	got := h.rec.LogRecords()[0].Timestamp.UTC()
	switch {
	case got.Equal(notAt):
		t.Errorf("first record timestamp = %v, the export's `logged` value; want its `eventTime` %v", got, wantAt)
	case !got.Equal(wantAt):
		t.Errorf("first record timestamp = %v, want the export's eventTime %v", got, wantAt)
	}
	for _, r := range h.rec.LogRecords() {
		if r.Timestamp.IsZero() {
			t.Fatalf("log record %q has a zero timestamp: the export's own eventTime was not used", r.Body)
		}
	}

	// The engine's freshness view of the record is separate from the log record's
	// own timestamp: it comes from the signal adapter's RecordTimestamps and feeds
	// the cursor and the lag/age telemetry. Event time must be the export's
	// eventTime and capture time its logged value — not the other way round, and
	// not both the same field.
	if len(accepted) != wantRecords {
		t.Fatalf("accepted observations = %d, want %d", len(accepted), wantRecords)
	}
	if got := accepted[0].EventTime.UTC(); !got.Equal(wantAt) {
		t.Errorf("accepted EventTime = %v, want the export's eventTime %v", got, wantAt)
	}
	if got := accepted[0].CaptureTime.UTC(); !got.Equal(notAt) {
		t.Errorf("accepted CaptureTime = %v, want the export's logged value %v", got, notAt)
	}
	if accepted[0].Signal != semconv.IngestSignalAudit {
		t.Errorf("accepted Signal = %q, want %q", accepted[0].Signal, semconv.IngestSignalAudit)
	}
}

// Tailscale writes a ZERO-BYTE object for an upload period with nothing to
// report (observed live: audit/2026/07/27/2026-07-27-12-13-00.ndjson, 0 bytes).
// That is an ordinary empty object, not a fault: it must be consumed, emit
// nothing, and still let the cursor move past it.
func TestAuditSignal_EmptyExportObjectIsNotAFailure(t *testing.T) {
	h := newAuditHarness(t, nil)
	h.store.put(auditKeyAt(now.Add(-10*time.Minute), ".ndjson"), nil)

	h.collect(t)

	if got := len(h.rec.LogRecords()); got != 0 {
		t.Errorf("log records = %d, want 0 from an empty object", got)
	}
}

// The signal name is what keys the checkpoint namespace and the ingest
// self-observability attributes, so it must be the shared audit constant and not
// a new spelling.
func TestAuditSignal_UsesTheSharedAuditSignalName(t *testing.T) {
	if got := objectstore.NewAuditSignal(audit.NewProcessor()).Signal(); got != semconv.IngestSignalAudit {
		t.Errorf("Signal() = %q, want %q", got, semconv.IngestSignalAudit)
	}
}

// "null" and "{}" are valid JSON that unmarshal into a ZERO audit.Event without
// error, so a decoder that only checks json.Unmarshal would emit a log record
// with no action, no target and the zero timestamp — a phantom configuration
// change. Such a line must be rejected as undecodable instead.
func TestAuditSignal_ZeroValuedRecordIsNotEmittedAsAPhantomEvent(t *testing.T) {
	for _, line := range []string{"null", "{}", `{"actor":{}}`} {
		t.Run(line, func(t *testing.T) {
			h := newAuditHarness(t, nil)
			good := `{"eventTime":"2026-07-24T11:50:00Z","action":"CREATE","origin":"CONFIG_API",` +
				`"actor":{"id":"uA","type":"USER"},"target":{"id":"tA","type":"TAILNET"}}`
			h.store.put(auditKeyAt(now.Add(-10*time.Minute), ".ndjson"), []byte(line+"\n"+good+"\n"))

			if err := h.col.Collect(context.Background(), h.rec.Emitter()); err != nil {
				t.Fatalf("Collect: %v", err)
			}

			if got := len(h.rec.LogRecords()); got != 1 {
				t.Errorf("log records = %d, want 1 — %q must not become an audit event", got, line)
			}
		})
	}
}

// A malformed line must cost only its own record. The engine's object-level
// guard (#497) fails an object where EVERY row was undecodable, so this fixture
// keeps a good record alongside the bad one.
func TestAuditSignal_MalformedRecordSkipsOnlyThatRecord(t *testing.T) {
	h := newAuditHarness(t, nil)
	good := `{"eventTime":"2026-07-24T11:50:00Z","action":"CREATE","origin":"CONFIG_API",` +
		`"actor":{"id":"uA","type":"USER"},"target":{"id":"tA","type":"TAILNET"}}`
	h.store.put(auditKeyAt(now.Add(-10*time.Minute), ".ndjson"), []byte("{not json\n"+good+"\n"))

	if err := h.col.Collect(context.Background(), h.rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := len(h.rec.LogRecords()); got != 1 {
		t.Errorf("log records = %d, want 1 — the good record must survive its malformed neighbor", got)
	}
}

// Both signals can run on one runtime, and the collector NAME keys the framework's
// tailscale.collector scrape attribute and the admin status page row. One shared
// name would merge two collectors' durations, failures and last-error into one
// indistinguishable series. The flow name stays the bare "objectstore" on purpose
// — it is what every existing deployment's series already carries.
func TestAuditSignal_CollectorNameIsDistinctFromFlow(t *testing.T) {
	audits := newAuditHarness(t, nil)
	flows := newHarness(t, nil)
	if got := audits.col.Name(); got != "objectstore-audit" {
		t.Errorf("audit collector Name() = %q, want objectstore-audit", got)
	}
	if got := flows.col.Name(); got != "objectstore" {
		t.Errorf("flow collector Name() = %q, want the unchanged objectstore", got)
	}
}
