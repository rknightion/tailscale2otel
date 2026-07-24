package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

func TestStatus_ReportsFlowStore(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)

	s := a.buildStatus()
	if !s.Flows.Enabled {
		t.Fatal("status reports the flow view as disabled")
	}
	if s.Flows.Observations != 1 {
		t.Errorf("observations = %d, want 1", s.Flows.Observations)
	}
	if s.Flows.Buckets != 1 {
		t.Errorf("buckets = %d, want 1", s.Flows.Buckets)
	}
	if s.Flows.Capacity != 360 {
		t.Errorf("capacity = %d, want 360 (6h of minutes)", s.Flows.Capacity)
	}
	if s.Flows.Retention != "6h0m0s" {
		t.Errorf("retention = %q", s.Flows.Retention)
	}
	if s.Flows.Covered == "" {
		t.Error("covered window is empty despite retained data")
	}
}

// Multi-tailnet sums the per-tailnet stores, so the operator sees one number for
// the process's memory rather than having to add them up.
func TestStatus_FlowStoreCombinesTailnets(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedFlows(t, a)
	seedFlows(t, a)

	if got := a.buildStatus().Flows.Observations; got != 2 {
		t.Errorf("observations = %d, want 2", got)
	}
}

func TestStatus_FlowStoreDisabled(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) { c.Flows.Enabled = false })

	s := a.buildStatus()
	if s.Flows.Enabled {
		t.Error("status reports the flow view as enabled when it is off")
	}
	if s.Flows.Observations != 0 || s.Flows.Buckets != 0 {
		t.Errorf("a disabled view reported activity: %+v", s.Flows)
	}
}

// The two admin surfaces have to be navigable from each other, or /flows is
// undiscoverable.
func TestStatusPage_LinksToFlows(t *testing.T) {
	a := flowsTestApp(t, nil)
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `href="/flows"`) {
		t.Error("the status page does not link to /flows")
	}
}

// With the view off the link must not be offered — it would 404.
func TestStatusPage_NoFlowsLinkWhenDisabled(t *testing.T) {
	a := flowsTestApp(t, func(c *config.Config) { c.Flows.Enabled = false })
	w := httptest.NewRecorder()
	a.buildAdminServer().Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Contains(w.Body.String(), `href="/flows"`) {
		t.Error("the status page links to /flows even though the route is not registered")
	}
}
