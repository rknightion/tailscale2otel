package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNewTraceExporter_Protocols(t *testing.T) {
	for _, proto := range []string{"", "http", "grpc", "stdout"} {
		exp, err := newTraceExporter(context.Background(), Options{
			Protocol:     proto,
			Endpoint:     "http://localhost:4318",
			StdoutWriter: &bytes.Buffer{},
		})
		if err != nil {
			t.Fatalf("protocol %q: %v", proto, err)
		}
		if exp == nil {
			t.Fatalf("protocol %q: got nil exporter", proto)
		}
		_ = exp.Shutdown(context.Background())
	}
	if _, err := newTraceExporter(context.Background(), Options{Protocol: "bogus"}); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestBuildSampler(t *testing.T) {
	cases := []struct {
		name       string
		sampler    string
		samplerArg float64
		wantDesc   string
	}{
		{name: "default empty", sampler: "", samplerArg: 1, wantDesc: "ParentBased{root:AlwaysOnSampler,..."},
		{name: "always_on", sampler: "always_on", wantDesc: "AlwaysOnSampler"},
		{name: "always_off", sampler: "always_off", wantDesc: "AlwaysOffSampler"},
		{name: "traceidratio", sampler: "traceidratio", samplerArg: 0.25, wantDesc: "TraceIDRatioBased{0.25}"},
		{name: "parentbased_always_on", sampler: "parentbased_always_on", wantDesc: "ParentBased{root:AlwaysOnSampler,..."},
		{name: "parentbased_ratio", sampler: "parentbased_traceidratio", samplerArg: 0.5, wantDesc: "ParentBased{root:TraceIDRatioBased{0.5},..."},
		{name: "unknown falls back", sampler: "nonexistent", wantDesc: "ParentBased{root:AlwaysOnSampler"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSampler(c.sampler, c.samplerArg).Description()
			// Prefix match keeps the test robust to the ParentBased sub-sampler suffix.
			// wantDesc may end with ",..." as a sentinel — strip it for HasPrefix.
			want := strings.TrimSuffix(c.wantDesc, ",...")
			if len(want) > 0 && !strings.HasPrefix(got, want) {
				t.Errorf("buildSampler(%q,%v).Description() = %q, want prefix %q", c.sampler, c.samplerArg, got, want)
			}
		})
	}
}
