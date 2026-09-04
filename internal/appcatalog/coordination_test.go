package appcatalog

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
)

func TestCoordinationHandoverDescriptor(t *testing.T) {
	var got *metricdoc.Metric
	for _, descriptor := range Catalog() {
		if descriptor.Name == MetricCoordinationHandovers {
			copy := descriptor
			got = &copy
			break
		}
	}
	if got == nil {
		t.Fatalf("Catalog() does not contain %q", MetricCoordinationHandovers)
	}
	if got.Unit != "{handover}" {
		t.Errorf("unit = %q, want {handover}", got.Unit)
	}
	if got.Instrument != metricdoc.Counter {
		t.Errorf("instrument = %q, want counter", got.Instrument)
	}
	wantAttrs := []string{
		"coordination.mode",
		"coordination.lease_name",
		"coordination.namespace",
		"coordination.identity",
	}
	if len(got.Attributes) != len(wantAttrs) {
		t.Fatalf("attributes = %v, want %v", got.Attributes, wantAttrs)
	}
	for i := range wantAttrs {
		if got.Attributes[i] != wantAttrs[i] {
			t.Errorf("attribute %d = %q, want %q", i, got.Attributes[i], wantAttrs[i])
		}
	}
	if got.Group != GroupSelfObs {
		t.Errorf("group = %q, want %q", got.Group, GroupSelfObs)
	}
	for _, phrase := range []string{"completed", "initial", "restart", "deleted"} {
		if !strings.Contains(strings.ToLower(got.Description), phrase) {
			t.Errorf("description %q does not explain %q", got.Description, phrase)
		}
	}
}
