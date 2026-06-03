package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/catalog"
)

// TestDocsMetricsInSync is a drift guard inside the normal test suite (not just
// CI): it asserts that the generated tables in docs/metrics.md match what the
// in-code catalog renders, so editing a metric/log descriptor without
// regenerating the doc fails `go test ./...`. The authoritative CI check is
// tools/metricscatalog -check; this mirrors it so the failure shows up locally.
func TestDocsMetricsInSync(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "metrics.md")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s (run from the repo tree to exercise this guard): %v", path, err)
	}
	out, err := catalog.Render(string(src))
	if err != nil {
		t.Fatalf("rendering %s failed: %v", path, err)
	}
	if out != string(src) {
		t.Errorf("%s is out of date with the in-code telemetry catalog; regenerate it with "+
			"`go run ./tools/metricscatalog -write` (from the repo root) and commit the result", path)
	}
}
