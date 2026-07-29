package k8saudit

import (
	"strings"
	"testing"
)

func TestCatalogIsWellFormed(t *testing.T) {
	if len(Catalog()) == 0 {
		t.Fatal("Catalog() empty")
	}
	for _, m := range Catalog() {
		if !strings.HasPrefix(m.Name, "tailscale.k8s.") {
			t.Fatalf("metric %q outside the tailscale.k8s. namespace", m.Name)
		}
		if m.Description == "" || m.Unit == "" || m.Group == "" {
			t.Fatalf("metric %q missing description/unit/group", m.Name)
		}
	}
	if len(LogCatalog()) == 0 {
		t.Fatal("LogCatalog() empty")
	}
}

// High-cardinality fields must never reach a metric.
func TestNoHighCardinalityAttributeOnAnyMetric(t *testing.T) {
	banned := map[string]bool{
		attrObjectName: true, attrPath: true, attrLabelSelector: true,
		attrFieldSelector: true, attrCommand: true, attrPod: true,
		attrContainer: true, attrSrcNodeID: true,
	}
	for _, m := range Catalog() {
		for _, a := range m.Attributes {
			if banned[a] {
				t.Fatalf("metric %q carries high-cardinality attribute %q", m.Name, a)
			}
		}
	}
}
