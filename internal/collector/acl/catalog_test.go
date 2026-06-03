package acl_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeAPI implements the narrow acl api interface for the catalog test.
type fakeAPI struct {
	acl *tsclient.RawACL
}

func (f *fakeAPI) PolicyFileRaw(_ context.Context) (*tsclient.RawACL, error) {
	return f.acl, nil
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric the collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog(). The HuJSON policy below includes several
// recognized sections (acls/grants/ssh/tagOwners) so tailscale.acl.rules is
// emitted.
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()

	huJSON := `{
		"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
		"grants": [{"src": ["*"], "dst": ["*"]}],
		"ssh": [{"action": "accept", "src": ["*"], "dst": ["*"], "users": ["root"]}],
		"tagOwners": {"tag:server": ["group:admins"]}
	}`
	fixedNow := time.Unix(1_000_000, 0).UTC()
	c := acl.New(&fakeAPI{acl: &tsclient.RawACL{HuJSON: huJSON, ETag: "etag-1"}}, 0, func() time.Time { return fixedNow })

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range acl.Catalog() {
		declared[m.Name] = m
	}

	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in acl.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}

	logDeclared := map[string]bool{}
	for _, le := range acl.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in acl.LogCatalog()", lr.EventName)
		}
	}
}
