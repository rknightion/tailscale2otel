package appcatalog

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
)

// TestProfilingCatalogShape pins the Pyroscope upload-health descriptors (#374).
// They are declared here rather than in internal/app so internal/catalog can
// document them without importing the app layer (see the package doc).
func TestProfilingCatalogShape(t *testing.T) {
	got := ProfilingCatalog()
	if len(got) != 5 {
		t.Fatalf("ProfilingCatalog() has %d descriptors, want 5: %+v", len(got), got)
	}
	want := map[string]struct {
		unit       string
		instrument metricdoc.Instrument
		attrs      int
	}{
		MetricProfilingUploadAttempts:            {"1", metricdoc.Counter, 0},
		MetricProfilingUploadFailures:            {"1", metricdoc.Counter, 1},
		MetricProfilingUploadDuration:            {"s", metricdoc.Histogram, 0},
		MetricProfilingUploadLastSuccess:         {"s", metricdoc.Gauge, 0},
		MetricProfilingUploadConsecutiveFailures: {"1", metricdoc.Gauge, 0},
	}
	for _, d := range got {
		w, ok := want[d.Name]
		if !ok {
			t.Errorf("undeclared descriptor %q in ProfilingCatalog()", d.Name)
			continue
		}
		if d.Unit != w.unit {
			t.Errorf("%s unit = %q, want %q", d.Name, d.Unit, w.unit)
		}
		if d.Instrument != w.instrument {
			t.Errorf("%s instrument = %v, want %v", d.Name, d.Instrument, w.instrument)
		}
		if len(d.Attributes) != w.attrs {
			t.Errorf("%s has %d attributes, want %d: %v", d.Name, len(d.Attributes), w.attrs, d.Attributes)
		}
		if d.Group != GroupSelfObs {
			t.Errorf("%s group = %q, want %q", d.Name, d.Group, GroupSelfObs)
		}
		if d.Description == "" {
			t.Errorf("%s has no description", d.Name)
		}
		delete(want, d.Name)
	}
	for name := range want {
		t.Errorf("missing descriptor %q from ProfilingCatalog()", name)
	}
}

// TestProfilingCatalogIsRegistered pins the descriptors into Catalog(), which is
// what puts them in the generated docs/metrics.md table. Declaring a descriptor
// without registering it is invisible: the metric emits fine and is simply
// undocumented, so only a test catches the omission.
func TestProfilingCatalogIsRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, d := range Catalog() {
		names[d.Name] = true
	}
	for _, d := range ProfilingCatalog() {
		if !names[d.Name] {
			t.Errorf("descriptor %q is in ProfilingCatalog() but missing from Catalog(): "+
				"it will emit but never appear in docs/metrics.md", d.Name)
		}
	}
}

// TestProfilingUploadErrorClassesClosed proves the error-class attribute's value
// set is CLOSED, which is what bounds the failures counter's cardinality.
func TestProfilingUploadErrorClassesClosed(t *testing.T) {
	got := ProfilingUploadErrorClasses()
	if len(got) == 0 {
		t.Fatal("ProfilingUploadErrorClasses() is empty")
	}
	seen := map[string]bool{}
	for _, c := range got {
		if c == "" {
			t.Error("empty error class in the closed set")
		}
		if seen[c] {
			t.Errorf("duplicate error class %q", c)
		}
		seen[c] = true
	}
}
