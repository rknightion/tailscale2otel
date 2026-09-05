package telemetry_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

// PAM classifies addresses using its runtime AddrSet before emission. The final
// emitter must preserve that classification, including custom Headscale ranges.
func TestEmitterPAMSessionPII(t *testing.T) {
	for _, tc := range []struct {
		name    string
		off     pii.Category
		removed string
	}{
		{"external", pii.CatExternalIPs, "tailscale.pam.session.client.external_ip"},
		{"tailnet", pii.CatTailscaleIPs, "tailscale.pam.session.client.tailnet_ip"},
		{"unrelated internal", pii.CatInternalIPs, ""},
		{"bounded socket", pii.CatFreeTextDetails, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cats := allOnCats()
			cats[tc.off] = false
			e, _, exp := newPIITestEmitter(t, cats)
			body := "PAM session authorization for test-socket"
			input := telemetry.Attrs{
				"tailscale.pam.session.socket_name":        "test-socket",
				"tailscale.pam.session.client.external_ip": "192.168.1.2",
				"tailscale.pam.session.client.tailnet_ip":  "10.1.2.3",
			}
			e.LogEvent(telemetry.Event{Name: "tailscale.pam.session", Body: body, Attrs: input})
			recs := exp.all()
			if len(recs) != 1 {
				t.Fatalf("records=%d, want 1", len(recs))
			}
			attrs := logAttrs(recs[0])
			for key := range input {
				_, present := attrs[key]
				if present != (key != tc.removed) {
					t.Errorf("%s present=%v", key, present)
				}
			}
			if got := recs[0].Body().AsString(); got != body {
				t.Errorf("body=%q, want %q", got, body)
			}
		})
	}
}
