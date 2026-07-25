package oauthapps_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

// fakeLister returns a canned slice of OAuth apps (or an error).
type fakeLister struct {
	apps  []tsapi.OAuthApp
	err   error
	calls int
}

func (f *fakeLister) OAuthApps(context.Context) ([]tsapi.OAuthApp, error) {
	f.calls++
	return f.apps, f.err
}

func findPoint(t *testing.T, pts []telemetrytest.MetricPoint, want map[string]string) telemetrytest.MetricPoint {
	t.Helper()
outer:
	for _, p := range pts {
		for k, v := range want {
			if p.Attrs[k] != v {
				continue outer
			}
		}
		return p
	}
	t.Fatalf("no metric point matching %v in %+v", want, pts)
	return telemetrytest.MetricPoint{}
}

func TestName(t *testing.T) {
	c := oauthapps.New(&fakeLister{}, 0)
	if got, want := c.Name(), "oauth_apps"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestDefaultInterval(t *testing.T) {
	c := oauthapps.New(&fakeLister{}, 0)
	if got, want := c.DefaultInterval(), oauthapps.DefaultInterval; got != want {
		t.Errorf("DefaultInterval() = %v, want %v", got, want)
	}
}

func TestCollect_EmitsCountScopesNodeAttrsAndInfo(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{
			ID:                    "app1",
			Name:                  "provisioner",
			Scopes:                []string{"auth_keys:create", "devices:core:read"},
			AllowedNodeAttributes: []string{"custom:myattribute"},
		},
		{
			ID:   "app2",
			Name: "no-scope-app",
		},
	}}, 0)

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	countPts := rec.MetricPoints(oauthapps.MetricAppsCount)
	if len(countPts) != 1 || countPts[0].Value != 2 {
		t.Fatalf("apps.count = %+v, want a single point with value 2", countPts)
	}

	scopePts := rec.MetricPoints(oauthapps.MetricAppScopes)
	p := findPoint(t, scopePts, map[string]string{"tailscale.oauth_app.id": "app1"})
	if p.Value != 2 {
		t.Errorf("app1 scopes = %v, want 2", p.Value)
	}
	// app2 has no scopes: must NOT emit a scopes point for it.
	for _, pt := range scopePts {
		if pt.Attrs["tailscale.oauth_app.id"] == "app2" {
			t.Errorf("app2 (no scopes) unexpectedly has a scopes point: %+v", pt)
		}
	}

	nodeAttrPts := rec.MetricPoints(oauthapps.MetricAppNodeAttributes)
	p = findPoint(t, nodeAttrPts, map[string]string{"tailscale.oauth_app.id": "app1"})
	if p.Value != 1 {
		t.Errorf("app1 node_attributes = %v, want 1", p.Value)
	}
	for _, pt := range nodeAttrPts {
		if pt.Attrs["tailscale.oauth_app.id"] == "app2" {
			t.Errorf("app2 (no node attrs) unexpectedly has a node_attributes point: %+v", pt)
		}
	}

	logs := rec.LogRecords()
	var sawApp1, sawApp2 bool
	for _, lr := range logs {
		if lr.EventName != oauthapps.EventAppInfo {
			continue
		}
		switch lr.Attrs["tailscale.oauth_app.id"] {
		case "app1":
			sawApp1 = true
			if got, want := lr.Attrs["tailscale.oauth_app.scope_values"], "auth_keys:create,devices:core:read"; got != want {
				t.Errorf("app1 scope_values = %q, want %q", got, want)
			}
			if got, want := lr.Attrs["tailscale.oauth_app.node_attribute_count"], strconv.Itoa(1); got != want {
				t.Errorf("app1 node_attribute_count = %q, want %q", got, want)
			}
			if got, want := lr.Attrs["tailscale.oauth_app.name"], "provisioner"; got != want {
				t.Errorf("app1 name = %q, want %q", got, want)
			}
		case "app2":
			sawApp2 = true
			if got, want := lr.Attrs["tailscale.oauth_app.node_attribute_count"], strconv.Itoa(0); got != want {
				t.Errorf("app2 node_attribute_count = %q, want %q", got, want)
			}
		}
	}
	if !sawApp1 || !sawApp2 {
		t.Fatalf("expected an %s log event for both apps; sawApp1=%v sawApp2=%v", oauthapps.EventAppInfo, sawApp1, sawApp2)
	}
}

// TestCollect_RedirectURICount guards #419: only the COUNT of configured
// redirect URIs is ever emitted, never the URI values.
func TestCollect_RedirectURICount(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app1", Name: "has-redirects", RedirectURIs: []string{"https://example.com/a", "https://example.com/b"}},
		{ID: "app2", Name: "no-redirects"},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints(oauthapps.MetricAppRedirectURIs)
	p := findPoint(t, pts, map[string]string{"tailscale.oauth_app.id": "app1"})
	if p.Value != 2 {
		t.Errorf("app1 redirect_uris = %v, want 2", p.Value)
	}
	for _, pt := range pts {
		if pt.Attrs["tailscale.oauth_app.id"] == "app2" {
			t.Errorf("app2 (no redirect URIs) unexpectedly has a redirect_uris point: %+v", pt)
		}
	}

	// The URI values themselves must never reach telemetry: not on the metric
	// (it carries no such attribute) and not on the info log body/attrs.
	for _, lr := range rec.LogRecords() {
		for k, v := range lr.Attrs {
			if v == "https://example.com/a" || v == "https://example.com/b" {
				t.Errorf("redirect URI value leaked into log attr %q: %q", k, v)
			}
		}
		if got := lr.Body; got != "" {
			for _, banned := range []string{"https://example.com/a", "https://example.com/b"} {
				if strings.Contains(got, banned) {
					t.Errorf("redirect URI value leaked into log body: %q", got)
				}
			}
		}
	}
}

// TestCollect_ScopeClassGauge guards #419's scope_class slice, mirroring the
// keys collector's #415 treatment: tsscope.Classify(a.Scopes), zero-seeded
// across every class for EVERY app (including one with no scopes at all,
// unlike the count-based tailscale.oauth_app.scopes gauge, since ClassNone is
// itself a meaningful posture value here).
func TestCollect_ScopeClassGauge(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app1", Name: "unrestricted", Scopes: []string{"all"}},
		{ID: "app2", Name: "no-scopes"},
	}}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints(oauthapps.MetricAppScopeClass)
	byKey := func(id string) map[string]float64 {
		out := map[string]float64{}
		for _, p := range pts {
			if p.Attrs["tailscale.oauth_app.id"] != id {
				continue
			}
			out[p.Attrs["tailscale.oauth_app.scope_class"]] = p.Value
		}
		return out
	}

	app1 := byKey("app1")
	if len(app1) != 5 {
		t.Fatalf("app1: got %d classes, want 5 (zero-seeded): %v", len(app1), app1)
	}
	if app1["all"] != 1 {
		t.Errorf("app1 class 'all' = %v, want 1", app1["all"])
	}

	app2 := byKey("app2")
	if len(app2) != 5 {
		t.Fatalf("app2 (no scopes): got %d classes, want 5 (zero-seeded, including for an unscoped app): %v", len(app2), app2)
	}
	if app2["none"] != 1 {
		t.Errorf("app2 class 'none' = %v, want 1", app2["none"])
	}
}

// TestCollect_AppsAgeHistogram guards the oauth_apps slice of #426.
func TestCollect_AppsAgeHistogram(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: []tsapi.OAuthApp{
		{ID: "app1", Name: "week-old", Created: now.Add(-7 * 24 * time.Hour)},
		{ID: "app2", Name: "no-created"}, // zero Created -> must be skipped
	}}, 0, oauthapps.WithClock(func() time.Time { return now }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints(oauthapps.MetricAppsAge)
	if len(pts) != 1 {
		t.Fatalf("oauth_apps.age points = %d, want 1 (no-created must be skipped) (%+v)", len(pts), pts)
	}
	if pts[0].Kind != "histogram" {
		t.Fatalf("oauth_apps.age kind = %q, want histogram", pts[0].Kind)
	}
	if pts[0].Unit != "s" {
		t.Fatalf("oauth_apps.age unit = %q, want s", pts[0].Unit)
	}
	wantSecs := float64(7 * 24 * time.Hour / time.Second)
	if pts[0].Value != wantSecs {
		t.Fatalf("oauth_apps.age sum = %v, want %v", pts[0].Value, wantSecs)
	}
}

func TestCollect_EmptyTailnet(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{apps: nil}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints(oauthapps.MetricAppsCount)
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("apps.count = %+v, want a single zero-value point", pts)
	}
}

// isFeatureOffErr simulates the alpha endpoint being unavailable.
func statusErr(code int) error {
	return &tsapi.StatusError{Method: "GET", URL: "https://api.tailscale.com/api/v2/tailnet/example.com/oauth-apps", Code: code, Body: "not found"}
}

func TestCollect_403IsIdleNotError(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{err: statusErr(403)}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect on 403 must be idle (nil error), got: %v", err)
	}
	if len(rec.MetricNames()) != 0 {
		t.Fatalf("Collect on 403 must emit nothing, got metrics: %v", rec.MetricNames())
	}
}

func TestCollect_404IsIdleNotError(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{err: statusErr(404)}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect on 404 must be idle (nil error), got: %v", err)
	}
	if len(rec.MetricNames()) != 0 {
		t.Fatalf("Collect on 404 must emit nothing, got metrics: %v", rec.MetricNames())
	}
}

func TestCollect_OtherErrorPropagates(t *testing.T) {
	rec := telemetrytest.New()
	wantErr := errors.New("boom")
	c := oauthapps.New(&fakeLister{err: wantErr}, 0)
	err := c.Collect(context.Background(), rec.Emitter())
	if err == nil {
		t.Fatal("Collect: expected a non-nil error for a non-403/404 failure")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Collect error = %v, want wrapping %v", err, wantErr)
	}
}

func TestCollect_5xxPropagates(t *testing.T) {
	rec := telemetrytest.New()
	c := oauthapps.New(&fakeLister{err: statusErr(500)}, 0)
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect on 5xx: expected a non-nil error (transient failure, not idle)")
	}
}
