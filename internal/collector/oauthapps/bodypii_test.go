package oauthapps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

func oauthCatsOff(off ...pii.Category) pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	for _, o := range off {
		c[o] = false
	}
	return c
}

func appInfoBody(t *testing.T, rec *telemetrytest.Recorder) string {
	t.Helper()
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.oauth_app.info" {
			return lr.Body
		}
	}
	t.Fatal("no tailscale.oauth_app.info log emitted")
	return ""
}

// Sentinel: the app-info body names the app. Disabling free_text_details must scrub
// the operator-chosen app name from the body, not only the attribute (#197).
func TestAppInfoBodyRedactsName(t *testing.T) {
	rec := telemetrytest.NewWithPII(oauthCatsOff(pii.CatFreeTextDetails))
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app-1", Name: "provisioner", Scopes: []string{"all:read"}},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := appInfoBody(t, rec); strings.Contains(b, "provisioner") {
		t.Errorf("free_text off: app name must be scrubbed from body, got %q", b)
	}
}

func TestAppInfoBodyKeepsNameWhenEnabled(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app-1", Name: "provisioner", Scopes: []string{"all:read"}},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if b := appInfoBody(t, rec); !strings.Contains(b, "provisioner") {
		t.Errorf("free_text on: body must name the app, got %q", b)
	}
}
