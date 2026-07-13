package telemetry_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

func TestProviderSetPerTailnetResource(t *testing.T) {
	base := telemetry.Options{ServiceName: "tailscale2otel", Protocol: "stdout"}
	ps, err := telemetry.NewProviderSet(context.Background(), base, []telemetry.PerTailnetOptions{
		{Name: "acme.example.com", InstanceID: "host/acme.example.com"},
		{Name: "beta.example.com", InstanceID: "host/beta.example.com"},
	})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(context.Background()) })

	if ps.Process() == nil {
		t.Fatal("Process() provider is nil")
	}
	got := ps.TailnetNames()
	if len(got) != 2 || got[0] != "acme.example.com" || got[1] != "beta.example.com" {
		t.Fatalf("TailnetNames = %v", got)
	}
	if ps.Tailnet("acme.example.com") == nil {
		t.Fatal("Tailnet(acme) is nil")
	}
	if ps.Tailnet("missing") != nil {
		t.Fatal("Tailnet(missing) should be nil")
	}
	if ps.Process().Emitter() == nil {
		t.Fatal("process Emitter is nil")
	}
	if ps.Tailnet("acme.example.com").Emitter() == nil {
		t.Fatal("tailnet Emitter is nil")
	}
}

func TestNewProviderSetNoTailnets(t *testing.T) {
	base := telemetry.Options{ServiceName: "tailscale2otel", Protocol: "stdout"}
	ps, err := telemetry.NewProviderSet(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(context.Background()) })
	if ps.Process() == nil {
		t.Fatal("Process() provider is nil")
	}
	if len(ps.TailnetNames()) != 0 {
		t.Fatalf("TailnetNames = %v, want empty", ps.TailnetNames())
	}
}
