package config

import (
	"strings"
	"testing"
)

// The headline built-in UI was unusable under the application's own defaults:
// admin.enabled and admin.landing_page both default true, admin.listen defaulted
// to the wildcard ":9091", and the status page refuses every request on a
// network-reachable bind with no token. So a first run served 403 to the one
// surface a new operator is most likely to open (#314).
//
// The fix is a loopback default, not a weaker gate. Deployment assets that need a
// wildcard bind set one explicitly.

func TestAdminDefaultBindIsLoopback(t *testing.T) {
	got := Default().Admin.Listen
	if !strings.HasPrefix(got, "127.0.0.1:") {
		t.Errorf("default admin.listen = %q, want a loopback bind. On a wildcard default the "+
			"landing page — enabled by default — answers 403 to every request until a token is "+
			"set, so the exporter's own UI does not work out of the box.", got)
	}
}

func TestAdminDefaultsStillEnableTheLandingPage(t *testing.T) {
	d := Default()
	if !d.Admin.Enabled || !d.Admin.LandingPage {
		t.Fatal("the point of #314 is a landing page that WORKS by default, not one that is off")
	}
}

// The loopback default must not become a way to reach the page unauthenticated
// from elsewhere: an operator who moves the bind to a network address is back
// under the credential requirement.
func TestNetworkBindStillWarnsWithoutAToken(t *testing.T) {
	c := Default()
	c.Admin.Listen = "0.0.0.0:9091"
	var found bool
	for _, w := range c.Warnings() {
		if strings.Contains(w, "admin.auth.token") {
			found = true
		}
	}
	if !found {
		t.Error("moving admin.listen to a wildcard bind with no token produced no advisory")
	}
}
