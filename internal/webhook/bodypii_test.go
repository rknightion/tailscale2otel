package webhook

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func webhookCatsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

// Sentinel: the webhook body is the upstream-supplied message. Disabling
// free_text_details must replace the whole body, not only the attributes (#197).
func TestWebhookBodyRedactedWhenFreeTextOff(t *testing.T) {
	rec := telemetrytest.NewWithPII(webhookCatsOff(pii.CatFreeTextDetails))
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret, Tolerance: 0},
		rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)
	_ = resp.Body.Close()

	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook.nodeCreated" {
			if strings.Contains(lr.Body, "Node foo created") || strings.Contains(lr.Body, "foo") {
				t.Errorf("free_text off: webhook body must be replaced, got %q", lr.Body)
			}
			return
		}
	}
	t.Fatal("no tailscale.webhook.nodeCreated log emitted")
}

func TestWebhookBodyKeptWhenFreeTextOn(t *testing.T) {
	rec := telemetrytest.New()
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret, Tolerance: 0},
		rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	sig := signBody(testSecret, ts, twoEventBody)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, sig)
	_ = resp.Body.Close()

	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.webhook.nodeCreated" {
			if lr.Body != "Node foo created" {
				t.Errorf("free_text on: body must be the raw message, got %q", lr.Body)
			}
			return
		}
	}
	t.Fatal("no tailscale.webhook.nodeCreated log emitted")
}

func TestWebhookTypedAttributesRespectPIIPolicy(t *testing.T) {
	rec := telemetrytest.NewWithPII(webhookCatsOff(
		pii.CatEmails,
		pii.CatHostnames,
		pii.CatNodeIDs,
		pii.CatEndpointPaths,
	))
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret},
		rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := `[{"timestamp":"2026-06-02T10:00:00Z","version":1,"type":"nodeKeyExpired","tailnet":"e.com","message":"node","data":{"nodeID":"node-secret","deviceName":"host-secret","managedBy":"owner@example.com","actor":"actor@example.com","url":"https://login.tailscale.com/admin/machines/node-secret","expiration":"2026-07-01T00:00:00Z"}}]`
	ts := time.Date(2026, 6, 2, 10, 1, 0, 0, time.UTC)

	resp := doPost(t, s.Handler(), "/webhook", body, signBody(testSecret, ts, body))
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	for _, key := range []string{AttrNodeID, AttrDeviceName, AttrManagedBy, AttrActor, AttrURL} {
		if _, ok := logs[0].Attrs[key]; ok {
			t.Errorf("disabled PII attribute %q was emitted", key)
		}
	}
	if got := logs[0].Attrs[AttrKeyExpiration]; got != "2026-07-01T00:00:00Z" {
		t.Errorf("non-identifier expiration = %#v, want preserved", got)
	}
}
