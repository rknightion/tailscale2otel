package config

import "testing"

func TestPIIFilterDefaultsAllTrue(t *testing.T) {
	c := Default()
	f := c.PIIFilter
	for name, v := range map[string]bool{
		"emails": f.Emails, "user_display_names": f.UserDisplayNames, "user_ids": f.UserIDs,
		"hostnames": f.Hostnames, "node_ids": f.NodeIDs, "tailscale_ips": f.TailscaleIPs,
		"internal_ips": f.InternalIPs, "external_ips": f.ExternalIPs, "service_addrs": f.ServiceAddrs,
		"endpoint_paths": f.EndpointPaths, "network_topology": f.NetworkTopology,
		"tailnet_name": f.TailnetName, "free_text_details": f.FreeTextDetails,
	} {
		if !v {
			t.Errorf("pii_filter.%s default = false, want true", name)
		}
	}
}

func TestPIIFilterEnvOverride(t *testing.T) {
	t.Setenv("TS2OTEL_PII_FILTER__EMAILS", "false")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PIIFilter.Emails {
		t.Error("TS2OTEL_PII_FILTER__EMAILS=false should disable emails")
	}
	if !cfg.PIIFilter.Hostnames {
		t.Error("hostnames should remain default true")
	}
}
