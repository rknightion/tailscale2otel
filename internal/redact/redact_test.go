package redact_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/redact"
)

func TestURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"plain", "https://otlp.example.com/otlp", "https://otlp.example.com/otlp"},
		{"userinfo user and password", "https://123456:glc_secrettoken@otlp.example.com/otlp", "https://redacted@otlp.example.com/otlp"},
		{"userinfo user only", "https://glc_secrettoken@otlp.example.com/otlp", "https://redacted@otlp.example.com/otlp"},
		{"userinfo empty password", "https://user:@otlp.example.com/otlp", "https://redacted@otlp.example.com/otlp"},
		{"query value", "https://push.example.com/ingest?api_key=SUPERSECRET", "https://push.example.com/ingest?api_key=redacted"},
		{"multiple query values keep keys and order", "https://h/x?b=2&a=1&c=3", "https://h/x?b=redacted&a=redacted&c=redacted"},
		{"repeated key", "https://h/x?k=1&k=2", "https://h/x?k=redacted&k=redacted"},
		{"bare query flag has no value to strip", "https://h/x?debug", "https://h/x?debug"},
		{"empty query value", "https://h/x?k=", "https://h/x?k=redacted"},
		{"fragment dropped", "https://h/x#token=abc", "https://h/x"},
		{"everything at once", "https://u:p@h:8443/path?sig=abc&exp=1#frag", "https://redacted@h:8443/path?sig=redacted&exp=redacted"},
		{"host port no scheme", "otlp-gateway.example.com:443", "otlp-gateway.example.com:443"},
		{"unparseable", "https://%zz", "(unparseable url)"},
		{"control characters are unparseable", "http://exa\x7fmple.com", "(unparseable url)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact.URL(tc.raw); got != tc.want {
				t.Errorf("URL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestURLNeverLeaksSecretMaterial is the property the advisories care about: no
// substring of a credential placed in userinfo, a query value or the fragment
// may survive into the redacted form.
func TestURLNeverLeaksSecretMaterial(t *testing.T) {
	const secret = "sUpErSeCrEtCrEdEnTiAl"
	raws := []string{
		"https://user:" + secret + "@host.example/path",
		"https://" + secret + "@host.example/path",
		"https://host.example/path?token=" + secret,
		"https://host.example/path?a=1&token=" + secret + "&b=2",
		"https://host.example/path#" + secret,
		"https://user:" + secret + "@host.example/path?sig=" + secret + "#" + secret,
	}
	for _, raw := range raws {
		got := redact.URL(raw)
		if strings.Contains(got, secret) {
			t.Errorf("URL(%q) = %q: leaks the secret", raw, got)
		}
	}
}

// TestURLKeepsDiagnosticValue proves the redaction is still useful for support:
// scheme, host, port, path and the query KEY names survive.
func TestURLKeepsDiagnosticValue(t *testing.T) {
	got := redact.URL("https://user:pw@gateway.example.com:8443/otlp/v1/metrics?region=eu&api_key=abc")
	for _, want := range []string{"https://", "gateway.example.com:8443", "/otlp/v1/metrics", "region=", "api_key="} {
		if !strings.Contains(got, want) {
			t.Errorf("URL(...) = %q: missing diagnostic part %q", got, want)
		}
	}
}

func TestURLOriginDropsEveryCredentialBearingComponent(t *testing.T) {
	const secret = "path-query-fragment-secret"
	raw := "https://user:password@gateway.example.com:8443/" + secret + "?token=" + secret + "#" + secret
	got := redact.URLOrigin(raw)
	if got != "https://gateway.example.com:8443" {
		t.Fatalf("URLOrigin = %q", got)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("URLOrigin leaked secret: %q", got)
	}
	if got := redact.URLOrigin("https://[malformed-secret"); strings.Contains(got, "malformed-secret") {
		t.Fatalf("malformed URL leaked raw input: %q", got)
	}
}

func TestURLOriginPreservesBareHostPortEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"otlp-gateway.example.com:443",
		"[2001:db8::1]:4317",
	} {
		if got := redact.URLOrigin(endpoint); got != endpoint {
			t.Errorf("URLOrigin(%q) = %q, want unchanged host:port", endpoint, got)
		}
	}
}

func TestHasUserinfo(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"https://host/path", false},
		{"host:443", false},
		{"https://user@host/path", true},
		{"https://user:pw@host/path", true},
		{"https://%zz@host", false}, // unparseable: not classified as userinfo-bearing
		{"http://host/path?u=a@b", false},
	}
	for _, tc := range cases {
		if got := redact.HasUserinfo(tc.raw); got != tc.want {
			t.Errorf("HasUserinfo(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
