package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDiagnostics_CollectsIndependentErrors is the core #307 acceptance
// check: two unrelated invalid fields must BOTH show up as error Diagnostics
// in one call, even though Validate() only ever reports the first.
func TestDiagnostics_CollectsIndependentErrors(t *testing.T) {
	cfg := Default()
	cfg.LogFormat = "bogus"                    // fails independently...
	cfg.Cardinality.Flow.MetricsMode = "bogus" // ...of this.

	diags := cfg.Diagnostics()
	var sawLogFormat, sawMetricsMode bool
	for _, d := range diags {
		if d.Severity != SeverityError {
			continue
		}
		if strings.Contains(d.Message, "log_format") {
			sawLogFormat = true
		}
		if strings.Contains(d.Message, "metrics_mode") {
			sawMetricsMode = true
		}
	}
	if !sawLogFormat || !sawMetricsMode {
		t.Fatalf("expected diagnostics for BOTH independent failures, got: %+v", diags)
	}

	// Validate() must keep its historical first-error contract.
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "log_format") {
		t.Fatalf("Validate() semantics changed: want first error to mention log_format, got %v", err)
	}
}

// TestDiagnostics_SkipsDependentChecks: an invalid provider value makes every
// provider-specific rule (headscale.* required fields) meaningless, since
// neither the "== tailscale" nor "== headscale" branch matches. Diagnostics()
// must report the provider error alone, not a cascade of headscale.* errors
// derived from a config that was never meant to be read as headscale.
func TestDiagnostics_SkipsDependentChecks(t *testing.T) {
	cfg := Default()
	cfg.Provider = "bogus"
	cfg.Headscale.URL = ""    // would fail "headscale.url: required" if evaluated
	cfg.Headscale.APIKey = "" // same

	diags := cfg.Diagnostics()
	for _, d := range diags {
		if d.Severity != SeverityError {
			continue
		}
		if strings.HasPrefix(d.Path, "headscale.") {
			t.Fatalf("expected headscale.* checks to be skipped when provider is invalid, got: %+v", d)
		}
	}
	var sawProvider bool
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "provider") {
			sawProvider = true
		}
	}
	if !sawProvider {
		t.Fatalf("expected a provider diagnostic, got: %+v", diags)
	}
}

// TestDiagnostics_IncludesWarnings folds Warnings() into the same stream as
// severity=warning, without altering Warnings() itself.
func TestDiagnostics_IncludesWarnings(t *testing.T) {
	cfg := Default()
	cfg.Tailscale.Auth.Method = "apikey"

	want := cfg.Warnings()
	if len(want) == 0 {
		t.Fatal("expected at least one warning from this fixture")
	}

	diags := cfg.Diagnostics()
	var got []string
	for _, d := range diags {
		if d.Severity == SeverityWarning {
			got = append(got, d.Message)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("warning count mismatch: Warnings()=%d Diagnostics()=%d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("warning %d mismatch:\n Warnings():    %q\n Diagnostics(): %q", i, want[i], got[i])
		}
	}
}

// TestDiagnostics_NoSecretValues is the sentinel required by #307: no
// diagnostic (message or remediation) may ever contain a raw secret value,
// only the key it concerns. This config sets several credential-bearing
// fields to a distinctive marker and asserts the marker never appears in the
// message or remediation text of ANY diagnostic — even ones unrelated to
// that field, since a shared formatting helper leaking one value would leak
// them all.
func TestDiagnostics_NoSecretValues(t *testing.T) {
	const marker = "sekrit-value-must-never-be-echoed"

	cfg := Default()
	// Trigger validateNoURLCredentials, the one check that historically
	// handles a credential embedded in a URL.
	cfg.OTLP.Protocol = "http"
	cfg.OTLP.Endpoint = "https://user:" + marker + "@example.com/otlp"
	// Also set unrelated plain secrets so a shared helper leaking any
	// Config field indiscriminately would be caught too.
	cfg.Webhook.Secret = marker
	cfg.Streaming.Token = marker

	diags := cfg.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic from the malformed OTLP endpoint")
	}
	for _, d := range diags {
		if strings.Contains(d.Message, marker) {
			t.Fatalf("secret value leaked into Message: %+v", d)
		}
		if strings.Contains(d.Remediation, marker) {
			t.Fatalf("secret value leaked into Remediation: %+v", d)
		}
	}
}

// TestDiagnostic_JSONShape locks the JSON field names/casing so CI/editor
// tooling has a stable contract to parse.
func TestDiagnostic_JSONShape(t *testing.T) {
	d := Diagnostic{
		Severity:    SeverityError,
		Path:        "log_format",
		Message:     "log_format \"bogus\" invalid: must be text or json",
		Remediation: "Set log_format to one of: text, json.",
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"severity":"error","path":"log_format","message":"log_format \"bogus\" invalid: must be text or json","remediation":"Set log_format to one of: text, json."}`
	if string(b) != want {
		t.Fatalf("JSON shape drifted:\n got:  %s\n want: %s", b, want)
	}

	// Path and Remediation are omitempty — a diagnostic with neither must not
	// print empty-string keys, since CI tooling may treat their PRESENCE as
	// meaningful.
	bare := Diagnostic{Severity: SeverityWarning, Message: "advisory text"}
	b2, err := json.Marshal(bare)
	if err != nil {
		t.Fatal(err)
	}
	wantBare := `{"severity":"warning","message":"advisory text"}`
	if string(b2) != wantBare {
		t.Fatalf("omitempty shape drifted:\n got:  %s\n want: %s", b2, wantBare)
	}
}

func TestHasErrorsAndHasWarnings(t *testing.T) {
	none := []Diagnostic{}
	if HasErrors(none) || HasWarnings(none) {
		t.Fatal("empty slice must report neither")
	}
	onlyWarn := []Diagnostic{{Severity: SeverityWarning, Message: "w"}}
	if HasErrors(onlyWarn) {
		t.Fatal("warning-only slice must not report HasErrors")
	}
	if !HasWarnings(onlyWarn) {
		t.Fatal("expected HasWarnings true")
	}
	withErr := []Diagnostic{{Severity: SeverityWarning, Message: "w"}, {Severity: SeverityError, Message: "e"}}
	if !HasErrors(withErr) {
		t.Fatal("expected HasErrors true")
	}
}
