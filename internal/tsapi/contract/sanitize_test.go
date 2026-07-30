package contract_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/tsapi/contract"
)

func TestSanitizeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		extra   []string
		absent  []string
		present []string
	}{
		{
			name:   "tailscale cgnat address",
			in:     "dial tcp 100.71.134.73:443: connect: refused",
			absent: []string{"100.71.134.73"},
		},
		{
			name:   "any ipv4 literal",
			in:     "upstream 203.0.113.9 returned 500",
			absent: []string{"203.0.113.9"},
		},
		{
			name:   "tailscale ULA ipv6",
			in:     "peer fd7a:115c:a1e0:ab12:4843:cd96:6265:1234 unreachable",
			absent: []string{"fd7a:115c:a1e0"},
		},
		{
			name:   "email address",
			in:     `decode failed: {"email":"someone.real@example.org"}`,
			absent: []string{"someone.real@example.org", "@example.org"},
		},
		{
			name:   "magicdns hostname",
			in:     "GET https://laptop-of-someone.tailfe8c.ts.net/status failed",
			absent: []string{"laptop-of-someone", "tailfe8c"},
		},
		{
			name:   "auth credentials",
			in:     "Authorization: Bearer tskey-api-kABCDEFGH1CNTRL-secretsecret",
			absent: []string{"tskey-api-kABCDEFGH1CNTRL-secretsecret", "secretsecret"},
		},
		{
			name:   "node id",
			in:     "node nabc123XYZCNTRL failed to decode",
			absent: []string{"nabc123XYZCNTRL"},
		},
		{
			name:   "device and service path segments",
			in:     "GET /api/v2/device/12345678901234/attributes -> 404",
			absent: []string{"12345678901234"},
		},
		{
			name:   "caller-supplied literal (the tailnet name)",
			in:     "tailnet acme-corp.example.com has no devices",
			extra:  []string{"acme-corp.example.com"},
			absent: []string{"acme-corp.example.com"},
		},
		{
			name:    "keeps the diagnostic parts",
			in:      "listDeviceInvites live decode failed: json: cannot unmarshal string into field Accepted",
			present: []string{"listDeviceInvites", "cannot unmarshal", "Accepted"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contract.SanitizeEvidence(tc.in, tc.extra...)
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("sanitized output still contains %q:\n%s", a, got)
				}
			}
			for _, p := range tc.present {
				if !strings.Contains(got, p) {
					t.Errorf("sanitized output lost the diagnostic %q:\n%s", p, got)
				}
			}
		})
	}
}

// TestSanitizeEvidence_IsIdempotent: the workflow may pipe a report through more
// than once; a second pass must not mangle the redaction placeholders.
func TestSanitizeEvidence_IsIdempotent(t *testing.T) {
	in := "device 100.64.1.2 user a@b.example decode failed"
	once := contract.SanitizeEvidence(in)
	if twice := contract.SanitizeEvidence(once); twice != once {
		t.Errorf("not idempotent:\n once: %s\ntwice: %s", once, twice)
	}
}

// TestSanitizeEvidence_IgnoresEmptyExtras guards the CI call site, where an
// unset variable expands to an empty string — redacting "" would destroy the
// whole report.
func TestSanitizeEvidence_IgnoresEmptyExtras(t *testing.T) {
	in := "listUsers decode ok"
	if got := contract.SanitizeEvidence(in, "", "   "); got != in {
		t.Errorf("empty extras changed the text: %q", got)
	}
}
