package logstream

import (
	"context"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

func lsCatsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

func errorLogBody(t *testing.T, rec *telemetrytest.Recorder) string {
	t.Helper()
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.logstream.error" {
			return lr.Body
		}
	}
	t.Fatal("no tailscale.logstream.error log emitted")
	return ""
}

// Sentinel: the logstream error body is raw upstream free text. Disabling
// free_text_details must replace the whole body (#197).
func TestLogstreamErrorBodyRedactedWhenFreeTextOff(t *testing.T) {
	api := networkOnly(func(st *tsapi.LogStreamStatus) { st.LastError = "splunk: node 100.64.0.9 connection refused" })
	rec := telemetrytest.NewWithPII(lsCatsOff(pii.CatFreeTextDetails))
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := errorLogBody(t, rec); strings.Contains(b, "100.64.0.9") || strings.Contains(b, "connection refused") {
		t.Errorf("free_text off: error body must be replaced, got %q", b)
	}
}

func TestLogstreamErrorBodyKeptWhenFreeTextOn(t *testing.T) {
	api := networkOnly(func(st *tsapi.LogStreamStatus) { st.LastError = "splunk: connection refused" })
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := errorLogBody(t, rec); b != "splunk: connection refused" {
		t.Errorf("free_text on: body must be the raw error, got %q", b)
	}
}
