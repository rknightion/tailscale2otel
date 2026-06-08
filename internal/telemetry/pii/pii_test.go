package pii

import "testing"

func allOn() Categories {
	c := Categories{}
	for _, cat := range AllCategories {
		c[cat] = true
	}
	return c
}

func TestRedactorFastPathAllOn(t *testing.T) {
	r := New(allOn())
	in := map[string]any{"tailscale.user": "a@b.com", "host.name": "h1"}
	if len(r.Log(in)) != 2 {
		t.Fatalf("all-on Log dropped attrs")
	}
	if _, suppress := r.Identity(in); suppress {
		t.Fatal("all-on Identity should not suppress")
	}
}

func TestLogDropsDisabledCategory(t *testing.T) {
	c := allOn()
	c[CatEmails] = false
	r := New(c)
	out := r.Log(map[string]any{"tailscale.user": "a@b.com", "host.name": "h1"})
	if _, ok := out["tailscale.user"]; ok {
		t.Error("emails off: tailscale.user should be dropped from log")
	}
	if _, ok := out["host.name"]; !ok {
		t.Error("hostnames on: host.name should remain")
	}
}

func TestMergeDropsLabelKeepsOthers(t *testing.T) {
	c := allOn()
	c[CatExternalIPs] = false
	r := New(c)
	out := r.Merge(map[string]any{"source.address": "8.8.8.8", "network.transport": "tcp"})
	if _, ok := out["source.address"]; ok {
		t.Error("external_ips off: external source.address should be dropped")
	}
	if _, ok := out["network.transport"]; !ok {
		t.Error("non-identifier should remain")
	}
}

func TestMergeKeepsTailscaleIPWhenOnlyExternalOff(t *testing.T) {
	c := allOn()
	c[CatExternalIPs] = false
	r := New(c)
	out := r.Merge(map[string]any{"source.address": "100.64.0.9"})
	if _, ok := out["source.address"]; !ok {
		t.Error("tailscale IP must survive when only external_ips is off")
	}
}

func TestIdentitySuppressesWhenSoleIdentityRedacted(t *testing.T) {
	c := allOn()
	c[CatHostnames] = false
	r := New(c)
	_, suppress := r.Identity(map[string]any{"host.name": "h1", "tailscale.authorized": true})
	if !suppress {
		t.Error("gauge whose only identity key is redacted must suppress")
	}
}

func TestIdentityKeepsGaugeWhenNonIdentityRedacted(t *testing.T) {
	c := allOn()
	c[CatEmails] = false // tailscale.user is NOT an identity key
	r := New(c)
	out, suppress := r.Identity(map[string]any{
		"host.name": "h1", "host.id": "n1", "tailscale.user": "a@b.com",
	})
	if suppress {
		t.Fatal("device gauge must NOT suppress when only a non-identity label (email) is redacted")
	}
	if _, ok := out["tailscale.user"]; ok {
		t.Error("redacted email must be dropped from the gauge")
	}
	if _, ok := out["host.name"]; !ok {
		t.Error("identity labels must remain")
	}
}

func TestIdentityKeepsGaugeWhenOneOfTwoIdentityRedacted(t *testing.T) {
	c := allOn()
	c[CatHostnames] = false // host.name redacted, host.id (node_ids) survives
	r := New(c)
	out, suppress := r.Identity(map[string]any{"host.name": "h1", "host.id": "n1"})
	if suppress {
		t.Fatal("must NOT suppress: host.id still identifies the series")
	}
	if _, ok := out["host.name"]; ok {
		t.Error("host.name should be dropped")
	}
	if _, ok := out["host.id"]; !ok {
		t.Error("host.id should remain")
	}
}

func TestIdentitySuppressesWhenAllIdentityRedacted(t *testing.T) {
	c := allOn()
	c[CatHostnames] = false
	c[CatNodeIDs] = false
	r := New(c)
	_, suppress := r.Identity(map[string]any{"host.name": "h1", "host.id": "n1", "os.type": "linux"})
	if !suppress {
		t.Error("both join keys redacted -> series collapses -> suppress")
	}
}

func TestIPKeyFallbackToHostname(t *testing.T) {
	c := allOn()
	c[CatHostnames] = false
	r := New(c)
	out := r.Log(map[string]any{"tailscale.src.node": "laptop-1"})
	if _, ok := out["tailscale.src.node"]; ok {
		t.Error("src.node name should drop under hostnames-off")
	}
}

func TestUnknownKeyIPValueClassified(t *testing.T) {
	c := allOn()
	c[CatExternalIPs] = false
	r := New(c)
	out := r.Merge(map[string]any{"some_node_label": "203.0.113.7", "region": "lhr"})
	if _, ok := out["some_node_label"]; ok {
		t.Error("unknown key with external-IP value should be redacted")
	}
	if _, ok := out["region"]; !ok {
		t.Error("unknown key with non-IP value should be left alone")
	}
	out2 := r.Merge(map[string]any{"some_node_label": "100.64.0.1"})
	if _, ok := out2["some_node_label"]; !ok {
		t.Error("unknown key with tailscale-IP value must survive when only external_ips is off")
	}
}
