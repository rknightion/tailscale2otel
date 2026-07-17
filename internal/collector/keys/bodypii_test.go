package keys_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

func keysCatsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

// Sentinel: the expiring-key warn body names the key via its description. Disabling
// free_text_details must scrub the description from the body, not only the attr (#197).
func TestExpiringKeyBodyRedactsDescription(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.NewWithPII(keysCatsOff(pii.CatFreeTextDetails))
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", now.Add(time.Hour)),
	}}, 0, 24*time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	r := findLog(t, rec.LogRecords(), "tailscale.key.expiring")
	if strings.Contains(r.Body, "ci runner") {
		t.Errorf("free_text off: key description must be scrubbed from body, got %q", r.Body)
	}
}

func TestExpiringKeyBodyKeepsDescriptionWhenEnabled(t *testing.T) {
	now := time.Date(2024, 6, 6, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := keys.New(&fakeLister{keys: []tsapi.Key{
		reusableKey("k1", now.Add(time.Hour)),
	}}, 0, 24*time.Hour, func() time.Time { return now })
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	r := findLog(t, rec.LogRecords(), "tailscale.key.expiring")
	if !strings.Contains(r.Body, "ci runner") {
		t.Errorf("free_text on: body must name the key, got %q", r.Body)
	}
}
