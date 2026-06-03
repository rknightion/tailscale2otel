package webhook

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// nodeCreatedEvent is a webhook event that maps onto the same canonical
// (create, node, <id>) change as an audit CREATE/NODE event at the same second.
func nodeCreatedEvent(nodeID string) event {
	return event{
		Timestamp: "2024-06-06T15:25:26Z",
		Version:   1,
		Type:      "nodeCreated",
		Tailnet:   "example.com",
		Message:   "node created",
		Data:      map[string]string{"nodeID": nodeID},
	}
}

// TestEmit_CrossDedupSuppressesAfterAudit confirms that when an audit event for
// a change has already been recorded in the shared cross-source set, the webhook
// event describing the SAME change is suppressed, while a webhook for a
// different node still emits.
func TestEmit_CrossDedupSuppressesAfterAudit(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)

	// Audit records the change first (NODE/CREATE/n1 at 2024-06-06T15:25:26Z).
	auditRec := telemetrytest.New()
	ap := audit.NewProcessor(audit.WithCrossDedup(set))
	ap.Process(audit.Event{
		EventTime: time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		Action:    "CREATE",
		Target:    audit.Target{ID: "n1", Type: "NODE"},
		Actor:     audit.Actor{LoginName: "a@example.com"},
	}, auditRec.Emitter())

	s := New(Options{}, rec.Emitter(), discard(), WithDedup(set))
	s.emit(nodeCreatedEvent("n1")) // same change -> suppressed
	s.emit(nodeCreatedEvent("n2")) // different node -> emitted

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("webhook log records = %d, want 1 (n1 suppressed, n2 emitted)", len(logs))
	}
	var total float64
	for _, p := range rec.MetricPoints(MetricEvents) {
		total += p.Value
	}
	if total != 1 {
		t.Fatalf("webhook events counter = %v, want 1", total)
	}
}

// TestEmit_WebhookSelfDedup confirms two identical mapped webhook events share
// the set and only the first emits.
func TestEmit_WebhookSelfDedup(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	s := New(Options{}, rec.Emitter(), discard(), WithDedup(set))

	s.emit(nodeCreatedEvent("n1"))
	s.emit(nodeCreatedEvent("n1"))

	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("log records = %d, want 1 (self cross-dedup)", got)
	}
}

// TestEmit_UnmappedTypeNeverSuppressed confirms a webhook type with no canonical
// audit mapping is never cross-deduped (ok=false -> always emit), so distinct
// real events are never lost to an over-eager key.
func TestEmit_UnmappedTypeNeverSuppressed(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	s := New(Options{}, rec.Emitter(), discard(), WithDedup(set))

	ev := nodeCreatedEvent("n1")
	ev.Type = "nodeNeedsApproval" // not in the cross-source map
	s.emit(ev)
	s.emit(ev)

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2 (unmapped type not deduped)", got)
	}
}

// TestEmit_NoDedupSetEmitsAll confirms back-compat: with no shared set, every
// event emits regardless of cross-key.
func TestEmit_NoDedupSetEmitsAll(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{}, rec.Emitter(), discard())

	s.emit(nodeCreatedEvent("n1"))
	s.emit(nodeCreatedEvent("n1"))

	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2 (no dedup set)", got)
	}
}
