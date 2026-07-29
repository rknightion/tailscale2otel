package config

import "testing"

// minimalConfig returns a valid baseline config (an authenticated single
// tailnet, everything else at its default), mirroring geoipCfg's pattern in
// geoip_test.go so each test below can change exactly the one thing it is
// about.
func minimalConfig(t *testing.T) *Config {
	t.Helper()
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "tskey-api-test"
	return c
}

func TestK8sAuditObjectStoreIsNeverInheritedFromAudit(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Collectors.Auditlogs.ObjectStore = ObjectStoreConfig{Bucket: "audit-bucket", Region: "eu-west-1", Endpoint: "https://s3.eu-west-1.amazonaws.com"}
	cfg.Collectors.K8sAudit.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("k8s_audit enabled with no destination of its own must fail validation")
	}
}

func TestK8sAuditDefaultsToRecorderLayout(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Collectors.K8sAudit.Enabled = true
	cfg.Collectors.K8sAudit.ObjectStore = ObjectStoreConfig{
		Bucket: "recordings", Region: "eu-west-1", Endpoint: "https://s3.eu-west-1.amazonaws.com",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	os, ok := cfg.K8sAuditObjectStore("")
	if !ok {
		t.Fatal("no destination resolved")
	}
	if os.Layout != ObjectStoreLayoutRecorder {
		t.Fatalf("Layout = %q, want %q", os.Layout, ObjectStoreLayoutRecorder)
	}
}

func TestK8sAuditRejectsPartitionedLayout(t *testing.T) {
	cfg := minimalConfig(t)
	cfg.Collectors.K8sAudit.Enabled = true
	cfg.Collectors.K8sAudit.ObjectStore = ObjectStoreConfig{
		Bucket: "recordings", Region: "eu-west-1",
		Endpoint: "https://s3.eu-west-1.amazonaws.com",
		Layout:   ObjectStoreLayoutPartitioned,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("tsrecorder writes no date partitions; the partitioned layout must be refused")
	}
}
