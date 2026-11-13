package acl_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	tsclient "tailscale.com/client/tailscale/v2"
)

func TestACLSectionGaugesDropWhenSectionRemoved(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[{"src":["*"],"dst":["*"]}],"grants":[],"ssh":[{"src":["*"],"dst":["tag:example-web"]}],"autoApprovers":{"exitNode":["tag:example-web"]}}`}}
	c := acl.New(api, 0, nil)
	rec := telemetrytest.New()
	collect := func() {
		t.Helper()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	names := []string{"tailscale.acl.rules", "tailscale.acl.wildcard_rules", "tailscale.acl.unrestricted_rules", "tailscale.acl.posture_gated_rules", "tailscale.acl.ssh_wildcard", "tailscale.acl.autoapprovers"}
	before := make(map[string][]telemetrytest.MetricPoint)
	for _, name := range names {
		before[name] = rec.MetricPoints(name)
		if len(before[name]) == 0 {
			t.Fatalf("missing initial %s", name)
		}
	}
	api.err = errors.New("synthetic fetch failure")
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("expected fetch error")
	}
	api.err = nil
	api.acl.HuJSON = "{invalid"
	collect()
	for _, name := range names {
		if got := rec.MetricPoints(name); !sameMetricPoints(got, before[name]) {
			t.Errorf("%s changed after failed fetch or invalid policy", name)
		}
	}
	api.acl.HuJSON = `{"grants":[]}`
	collect()
	for _, name := range names {
		for _, point := range rec.MetricPoints(name) {
			if point.Attrs["tailscale.acl.section"] != "grants" {
				t.Errorf("%s retains removed section: %v", name, point)
			}
			if point.Value != 0 {
				t.Errorf("%s clean section value = %v, want 0", name, point.Value)
			}
		}
	}
	for _, name := range names[:4] {
		if len(rec.MetricPoints(name)) == 0 {
			t.Errorf("%s missing explicit clean grant zero", name)
		}
	}
}

// sameMetricPoints compares snapshots without depending on SDK iteration order.
func sameMetricPoints(a, b []telemetrytest.MetricPoint) bool {
	if len(a) != len(b) {
		return false
	}
	for _, point := range a {
		found := false
		for _, other := range b {
			if reflect.DeepEqual(point, other) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
