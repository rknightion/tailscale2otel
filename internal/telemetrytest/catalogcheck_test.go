package telemetrytest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeTB captures Errorf so a test can assert the guard fired (or stayed quiet)
// without failing the enclosing test.
type fakeTB struct {
	testing.TB
	errs []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

// TestAssertCatalogAttrs_CatchesUndeclaredAttr reproduces the tailscale.key.scopes
// drift class: an attribute emitted at runtime but absent from the catalog. The
// older Name/Unit/Instrument-only guards missed it; AssertCatalogAttrs must flag it.
func TestAssertCatalogAttrs_CatchesUndeclaredAttr(t *testing.T) {
	rec := telemetrytest.New()
	rec.Emitter().Gauge("tailscale.key.info", "1", "d", 1, telemetry.Attrs{
		"tailscale.key.id":     "k1",
		"tailscale.key.scopes": "all", // emitted but NOT declared below
	})
	metrics := []metricdoc.Metric{{
		Name: "tailscale.key.info", Unit: "1", Instrument: metricdoc.Gauge,
		Attributes: []string{"tailscale.key.id"},
	}}

	ft := &fakeTB{}
	telemetrytest.AssertCatalogAttrs(ft, rec, metrics, nil)

	if len(ft.errs) == 0 {
		t.Fatal("guard did not flag the undeclared tailscale.key.scopes attribute")
	}
	joined := strings.Join(ft.errs, "\n")
	if !strings.Contains(joined, "tailscale.key.scopes") {
		t.Fatalf("expected an error naming tailscale.key.scopes, got: %s", joined)
	}
}

// TestAssertCatalogAttrs_PassesWhenDeclared confirms no false positive when every
// emitted attribute is declared.
func TestAssertCatalogAttrs_PassesWhenDeclared(t *testing.T) {
	rec := telemetrytest.New()
	rec.Emitter().Gauge("tailscale.key.info", "1", "d", 1, telemetry.Attrs{
		"tailscale.key.id":     "k1",
		"tailscale.key.scopes": "all",
	})
	metrics := []metricdoc.Metric{{
		Name: "tailscale.key.info", Unit: "1", Instrument: metricdoc.Gauge,
		Attributes: []string{"tailscale.key.id", "tailscale.key.scopes"},
	}}

	ft := &fakeTB{}
	telemetrytest.AssertCatalogAttrs(ft, rec, metrics, nil)
	if len(ft.errs) != 0 {
		t.Fatalf("guard produced false positives on fully-declared attrs: %v", ft.errs)
	}
}
