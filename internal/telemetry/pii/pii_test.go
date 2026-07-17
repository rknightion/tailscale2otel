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

func TestIdentitySuppressesWhenUserIdentityRedacted(t *testing.T) {
	c := allOn()
	c[CatUserIDs] = false
	c[CatEmails] = false
	r := New(c)
	_, suppress := r.Identity(map[string]any{"user.id": "user-A", "user.name": "a@example.com", "tailscale.user.role": "member"})
	if !suppress {
		t.Error("both user identity keys redacted -> per-user gauge series collapses -> suppress")
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

// TestLogDropsPostureDetailsWhenFreeTextOff is the pii-package half of the
// issue #56 regression: the devices collector's posture log routes its
// dynamic (arbitrary provider-namespaced) posture attribute map through the
// classified "tailscale.device.posture.details" key so it is gated by
// pii_filter.free_text_details, the same category that already covers
// tailscale.audit.details/old/new and the device.attribute.info "value" label.
func TestLogDropsPostureDetailsWhenFreeTextOff(t *testing.T) {
	c := allOn()
	c[CatFreeTextDetails] = false
	r := New(c)
	out := r.Log(map[string]any{
		"tailscale.device.posture.details": `{"custom:foo":"bar","intune:isEncrypted":true}`,
		"host.name":                        "h1",
	})
	if _, ok := out["tailscale.device.posture.details"]; ok {
		t.Error("free_text_details off: tailscale.device.posture.details should be dropped from the posture log")
	}
	if _, ok := out["host.name"]; !ok {
		t.Error("host.name (not free-text) should remain")
	}
}

// TestHostPortRedactedByCorrectCategory is the pii-package half of #198: the
// default node-metrics identity value for tailscale.node is "host:port" (see
// internal/collector/nodemetrics), so it must be classified by its address
// portion and redacted under tailscale_ips/internal_ips/external_ips — NOT
// under hostnames (the ipValueKeys fallback category), and disabling any ONE
// of those categories must not touch the others.
func TestHostPortRedactedByCorrectCategory(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  Category
	}{
		{"ipv4 tailscale", "100.64.0.1:5252", CatTailscaleIPs},
		{"ipv4 internal", "10.0.0.5:5252", CatInternalIPs},
		{"ipv4 external", "8.8.8.8:5252", CatExternalIPs},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Disabling the correct category redacts it.
			on := allOn()
			on[c.want] = false
			out := New(on).Log(map[string]any{"tailscale.node": c.value})
			if _, ok := out["tailscale.node"]; ok {
				t.Errorf("category %v off should redact %q", c.want, c.value)
			}

			// Disabling hostnames (the ipValueKeys fallback) must NOT redact
			// an IP:port value — this is the bug #198 fixes.
			hostnamesOff := allOn()
			hostnamesOff[CatHostnames] = false
			out2 := New(hostnamesOff).Log(map[string]any{"tailscale.node": c.value})
			if _, ok := out2["tailscale.node"]; !ok {
				t.Errorf("hostnames off must NOT redact IP:port value %q (category independence)", c.value)
			}

			// Disabling every OTHER IP category must not redact this one.
			for _, other := range []Category{CatTailscaleIPs, CatInternalIPs, CatExternalIPs} {
				if other == c.want {
					continue
				}
				otherOff := allOn()
				otherOff[other] = false
				out3 := New(otherOff).Log(map[string]any{"tailscale.node": c.value})
				if _, ok := out3["tailscale.node"]; !ok {
					t.Errorf("category %v off must not redact %q (belongs to %v)", other, c.value, c.want)
				}
			}
		})
	}
}

// TestHostPortIPv6BracketedRedactedByCorrectCategory is the bracketed-IPv6
// analog of TestHostPortRedactedByCorrectCategory.
func TestHostPortIPv6BracketedRedactedByCorrectCategory(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  Category
	}{
		{"ipv6 tailscale bracketed", "[fd7a:115c:a1e0::1]:5252", CatTailscaleIPs},
		{"ipv6 internal bracketed (ULA)", "[fc00::1]:5252", CatInternalIPs},
		{"ipv6 external bracketed", "[2606:4700::1111]:5252", CatExternalIPs},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			on := allOn()
			on[c.want] = false
			out := New(on).Log(map[string]any{"tailscale.node": c.value})
			if _, ok := out["tailscale.node"]; ok {
				t.Errorf("category %v off should redact %q", c.want, c.value)
			}

			hostnamesOff := allOn()
			hostnamesOff[CatHostnames] = false
			out2 := New(hostnamesOff).Log(map[string]any{"tailscale.node": c.value})
			if _, ok := out2["tailscale.node"]; !ok {
				t.Errorf("hostnames off must NOT redact bracketed IPv6:port value %q", c.value)
			}
		})
	}
}

// TestHostPortFallsBackToHostnameForGenuineHostname proves the fallback path
// (ipKeyFallback -> CatHostnames) still fires for a value that merely looks
// like host:port but whose host segment is not an IP address at all, and that
// it stays independent of the IP categories.
func TestHostPortFallsBackToHostnameForGenuineHostname(t *testing.T) {
	value := "laptop-1:5252"

	hostnamesOff := allOn()
	hostnamesOff[CatHostnames] = false
	out := New(hostnamesOff).Log(map[string]any{"tailscale.node": value})
	if _, ok := out["tailscale.node"]; ok {
		t.Errorf("hostnames off should redact genuine hostname:port %q", value)
	}

	tailscaleIPsOff := allOn()
	tailscaleIPsOff[CatTailscaleIPs] = false
	out2 := New(tailscaleIPsOff).Log(map[string]any{"tailscale.node": value})
	if _, ok := out2["tailscale.node"]; !ok {
		t.Errorf("tailscale_ips off must not redact genuine hostname:port %q", value)
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
