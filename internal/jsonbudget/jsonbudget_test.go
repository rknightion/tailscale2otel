package jsonbudget_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v5/internal/jsonbudget"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// TestSharedSentinelsReachBothClients is the anti-drift guard that is the whole
// point of #488: internal/tsapi and internal/hsapi must keep resolving to ONE
// implementation, so a fix to either can never again apply to only one. If a
// package re-declares its own sentinel, this fails.
func TestSharedSentinelsReachBothClients(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want error
	}{
		{"tsapi too-large", tsapi.ErrResponseTooLarge, jsonbudget.ErrTooLarge},
		{"tsapi too-complex", tsapi.ErrResponseTooComplex, jsonbudget.ErrTooComplex},
		{"hsapi too-large", hsapi.ErrResponseTooLarge, jsonbudget.ErrTooLarge},
		{"hsapi too-complex", hsapi.ErrResponseTooComplex, jsonbudget.ErrTooComplex},
	} {
		if !errors.Is(tc.got, tc.want) {
			t.Errorf("%s: sentinel is not the shared one — errors.Is across the two clients has diverged", tc.name)
		}
	}
	// The two sentinels stay distinct from each other: a structural violation
	// must never masquerade as a byte-budget one, because the remedies differ.
	if errors.Is(jsonbudget.ErrTooComplex, jsonbudget.ErrTooLarge) {
		t.Error("ErrTooComplex must not match ErrTooLarge")
	}
}

func TestErrorMessageNamesSourceAndConfigKey(t *testing.T) {
	err := jsonbudget.Of("hsapi", 4<<20, "headscale.max_response_bytes").ByteCeilingError()
	msg := err.Error()
	for _, want := range []string{"hsapi:", "bytes", "4194304", "headscale.max_response_bytes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to contain %q", msg, want)
		}
	}
	if !errors.Is(err, jsonbudget.ErrTooLarge) {
		t.Errorf("ByteCeilingError() must unwrap to ErrTooLarge, got %v", err)
	}
}

// A structural budget has no operator knob, so its error must NOT invite the
// operator to raise a config key that would not help.
func TestStructuralErrorNamesNoConfigKey(t *testing.T) {
	var out any
	deep := strings.Repeat("[", jsonbudget.DefaultMaxDepth+2) + strings.Repeat("]", jsonbudget.DefaultMaxDepth+2)
	err := jsonbudget.Decode(strings.NewReader(deep), jsonbudget.Of("tsapi", 1<<20, "tailscale.max_response_bytes"), &out)
	var be *jsonbudget.Error
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want a *jsonbudget.Error", err)
	}
	if be.ConfigKey != "" {
		t.Errorf("ConfigKey = %q, want empty for a structural budget", be.ConfigKey)
	}
	if strings.Contains(be.Error(), "raise") {
		t.Errorf("Error() = %q, must not tell the operator to raise a key that would not help", be.Error())
	}
}

// An empty Source (a hand-built Budget literal) must still produce a usable
// message rather than a stray leading colon.
func TestEmptySourceOmitsPrefix(t *testing.T) {
	err := jsonbudget.Budget{MaxBytes: 10}.ByteCeilingError()
	if strings.HasPrefix(err.Error(), ":") {
		t.Errorf("Error() = %q, want no dangling prefix", err.Error())
	}
}

// TestErrorCarriesNoResponseContent pins the PII/log-hygiene contract: the error
// names the control and the limit, never a byte of the body that tripped it.
func TestErrorCarriesNoResponseContent(t *testing.T) {
	const secret = "SUPERSECRETNODENAME"
	body := `{"nodes":[{"name":"` + secret + strings.Repeat("x", 4096) + `"}]}`
	var out map[string]any
	err := jsonbudget.Decode(strings.NewReader(body), jsonbudget.Budget{
		Source: "hsapi", MaxBytes: 256, MaxDepth: 8, MaxStringBytes: 64, MaxArrayElements: 8,
		ConfigKey: "headscale.max_response_bytes",
	}, &out)
	if err == nil {
		t.Fatal("expected a budget error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "xxx") {
		t.Fatalf("error leaks response content: %q", err.Error())
	}
}
