package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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
		Data:      map[string]json.RawMessage{"nodeID": jsonString(nodeID)},
	}
}

// jsonString renders s as a JSON string value (e.g. n1 -> "n1") for event.Data.
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
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

// TestHandler_ArrayValuedDataDoesNotDropBatch pins S4-11(e): a webhook event
// whose data carries a non-flat value (userRoleUpdated's oldRoles/newRoles are
// arrays per kb/1213) must NOT cause the whole POST to be rejected. The old
// map[string]string Data type failed json.Unmarshal on the array, dropping every
// event in the batch.
func TestHandler_ArrayValuedDataDoesNotDropBatch(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Path: "/webhook"}, rec.Emitter(), discard()) // empty secret -> verification skipped
	body := `[` +
		`{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeCreated","tailnet":"e.com","message":"m","data":{"nodeID":"n1"}},` +
		`{"timestamp":"2026-06-02T10:00:01Z","version":1,"type":"userRoleUpdated","tailnet":"e.com","message":"m","data":{"user":"a@e.com","oldRoles":["member"],"newRoles":["admin"],"url":"https://x"}}` +
		`]`
	resp := doPost(t, s.Handler(), "/webhook", body, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (array-valued data must not drop the batch)", resp.StatusCode)
	}
	if got := len(rec.LogRecords()); got != 2 {
		t.Fatalf("log records = %d, want 2 (both events emitted despite array-valued data)", got)
	}
}

// TestHandler_DistinctUserUpdatesNotSuppressed guards D11 (never over-suppress).
// userApproved, userSuspended and userRoleUpdated are THREE INDEPENDENT changes
// that would all share the same (update, user) cross-key — so once subjectID
// resolves the "user" field, the same user in the same one-second bucket would
// collapse three real events into one. They must all emit. (Cross-SOURCE user
// dedup is non-viable anyway: the audit user Target.ID is an internal id, not the
// login/email the webhook "user" field carries — so these types are intentionally
// NOT in webhookActionMap.)
func TestHandler_DistinctUserUpdatesNotSuppressed(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	s := New(Options{Path: "/webhook"}, rec.Emitter(), discard(), WithDedup(set))
	mk := func(typ string) string {
		return `{"timestamp":"2024-06-06T15:25:26Z","version":1,"type":"` + typ + `","tailnet":"e.com","message":"m","data":{"user":"u1@e.com"}}`
	}
	body := `[` + mk("userApproved") + `,` + mk("userRoleUpdated") + `,` + mk("userSuspended") + `]`
	doPost(t, s.Handler(), "/webhook", body, "")
	if got := len(rec.LogRecords()); got != 3 {
		t.Fatalf("log records = %d, want 3 (distinct user-update events must not collapse)", got)
	}
}

// TestHandler_NodeAuthorizedAliasDedup pins S4-11(c): nodeAuthorized is the
// deprecated alias of nodeApproved and must map to the same (update, node)
// cross-key. Two identical nodeAuthorized events collapse to one.
func TestHandler_NodeAuthorizedAliasDedup(t *testing.T) {
	rec := telemetrytest.New()
	set := dedup.New(0)
	s := New(Options{Path: "/webhook"}, rec.Emitter(), discard(), WithDedup(set))
	one := `{"timestamp":"2024-06-06T15:25:26Z","version":1,"type":"nodeAuthorized","tailnet":"e.com","message":"m","data":{"nodeID":"n1"}}`
	body := `[` + one + `,` + one + `]`
	doPost(t, s.Handler(), "/webhook", body, "")
	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("log records = %d, want 1 (nodeAuthorized alias of nodeApproved)", got)
	}
}
