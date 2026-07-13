package oauthapps_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
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
