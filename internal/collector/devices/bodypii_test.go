package devices_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

func devCatsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

func lockErrorBody(t *testing.T, rec *telemetrytest.Recorder) string {
	t.Helper()
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.tailnet_lock_error" {
			return lr.Body
		}
	}
	t.Fatal("no tailnet_lock_error log emitted")
	return ""
}

// Sentinel: the tailnet-lock error body is raw upstream free text. Disabling
// free_text_details must replace the whole body (#197).
func TestTailnetLockErrorBodyRedactedWhenFreeTextOff(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "2", Hostname: "h2", TailnetLockError: "node 100.64.0.9 is not signed by key abc"},
	}}
	rec := telemetrytest.NewWithPII(devCatsOff(pii.CatFreeTextDetails))
	if err := devices.New(api, enrich.NewDeviceCache(), 0, false, false).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := lockErrorBody(t, rec); strings.Contains(b, "100.64.0.9") || strings.Contains(b, "not signed") {
		t.Errorf("free_text off: lock-error body must be replaced, got %q", b)
	}
}

func TestTailnetLockErrorBodyKeptWhenFreeTextOn(t *testing.T) {
	api := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "2", Hostname: "h2", TailnetLockError: "node is not signed"},
	}}
	rec := telemetrytest.New()
	if err := devices.New(api, enrich.NewDeviceCache(), 0, false, false).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := lockErrorBody(t, rec); b != "node is not signed" {
		t.Errorf("free_text on: body must be the raw error, got %q", b)
	}
}
