package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestWarnings_HostnameInstanceSourceWithTailscaleIPsOff(t *testing.T) {
	for _, source := range []string{"hostname", "name", "address"} {
		for _, enabled := range []bool{false, true} {
			c := config.Default()
			c.Collectors.NodeMetrics.Discovery.InstanceSource = source
			c.PIIFilter.TailscaleIPs = enabled
			found := ""
			for _, warning := range c.Warnings() {
				if strings.HasPrefix(warning, "collectors.node_metrics.discovery.instance_source") {
					found = warning
				}
			}
			want := source == "hostname" && !enabled
			if (found != "") != want {
				t.Errorf("source=%s tailscale_ips=%v warning=%q", source, enabled, found)
			}
			if want {
				for _, text := range []string{"tailscale.node", "removed", "instance_source: name", "unique", "address"} {
					if !strings.Contains(found, text) {
						t.Errorf("warning missing %q", text)
					}
				}
			}
		}
	}
}
