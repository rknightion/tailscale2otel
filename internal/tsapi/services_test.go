package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// servicesFixture mirrors a real /services response, including the
// addrs/comment/annotations the collector must NOT surface.
const servicesFixture = `{"vipServices":[
  {"name":"svc:argocd","addrs":["100.124.43.64","fd7a:115c:a1e0::7501:2b54"],
   "comment":"managed by the operator","annotations":{"tailscale.com/owner-references":"{...}"},
   "ports":["tcp:443"],"tags":["tag:k8s"]},
  {"name":"svc:grpc","addrs":["100.69.161.118"],"ports":["tcp:443","tcp:80"],"tags":["tag:k8s"]}
]}`

func TestServices_DecodesNamePortsTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/services" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(servicesFixture))
	}))
	defer srv.Close()

	svcs, err := newClient(t, srv.URL).Services(context.Background())
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(svcs) != 2 {
		t.Fatalf("len = %d, want 2", len(svcs))
	}
	if svcs[0].Name != "svc:argocd" {
		t.Errorf("name = %q", svcs[0].Name)
	}
	if len(svcs[0].Ports) != 1 || svcs[0].Ports[0] != "tcp:443" {
		t.Errorf("ports = %v", svcs[0].Ports)
	}
	if len(svcs[0].Tags) != 1 || svcs[0].Tags[0] != "tag:k8s" {
		t.Errorf("tags = %v", svcs[0].Tags)
	}
	if len(svcs[1].Ports) != 2 {
		t.Errorf("svc grpc ports = %v, want 2", svcs[1].Ports)
	}
}

func TestServiceHosts_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/services/svc:argocd/devices" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		// The real API wire field is stableNodeID (OAS ServiceHostInfo), not nodeId (#72).
		_, _ = w.Write([]byte(`{"hosts":[
		  {"stableNodeID":"n72PkCQgkF11CNTRL","approvalLevel":"approved:auto","configured":"ready"},
		  {"stableNodeID":"nzesx9PCUy11CNTRL","approvalLevel":"pending","configured":"pending"}
		]}`))
	}))
	defer srv.Close()

	hosts, err := newClient(t, srv.URL).ServiceHosts(context.Background(), "svc:argocd")
	if err != nil {
		t.Fatalf("ServiceHosts: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("len = %d, want 2", len(hosts))
	}
	if hosts[0].NodeID != "n72PkCQgkF11CNTRL" || hosts[0].ApprovalLevel != "approved:auto" || hosts[0].Configured != "ready" {
		t.Errorf("host[0] = %+v", hosts[0])
	}
}
