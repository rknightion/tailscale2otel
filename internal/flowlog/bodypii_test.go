package flowlog_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

func catsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

// Sentinel: the per-connection flow-log body embeds the raw addresses. Disabling
// tailscale_ips must scrub them from the body, not only the attributes (#197).
func TestPerConnectionLogBodyRedactsTailscaleIPs(t *testing.T) {
	rec := telemetrytest.NewWithPII(catsOff(pii.CatTailscaleIPs))
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_connection"})
	p.Process(virtualTCPFlow(), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	body := logs[0].Body
	for _, ip := range []string{"100.64.0.1", "100.64.0.2"} {
		if strings.Contains(body, ip) {
			t.Errorf("tailscale_ips off: body must not contain %s, got %q", ip, body)
		}
	}
	// Non-PII structure survives.
	if !strings.Contains(body, "tx=1000B") {
		t.Errorf("body must keep non-PII byte counts, got %q", body)
	}
}

func TestPerConnectionLogBodyKeepsIPsWhenEnabled(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_connection"})
	p.Process(virtualTCPFlow(), rec.Emitter())
	logs := rec.LogRecords()
	if len(logs) != 1 || !strings.Contains(logs[0].Body, "100.64.0.1") {
		t.Fatalf("all-on: body must retain the source IP, got %+v", logs)
	}
}
