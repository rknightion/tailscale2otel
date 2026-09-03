package telemetry_test

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// BenchmarkCardinalityTracker_Observe drives Observe under two distinct-
// fingerprint regimes: "low" repeats a handful of attribute sets (the steady-
// state case — most connections share a small set of dimension values), while
// "high" hands every call a fresh, never-before-seen attribute set, pushing
// toward defaultSeriesCap so the capped/pinned bookkeeping path is exercised
// too, not just the happy-path map insert.
func BenchmarkCardinalityTracker_Observe(b *testing.B) {
	b.Run("low", func(b *testing.B) {
		tr := telemetry.NewCardinalityTracker()
		const distinct = 8
		attrs := make([]telemetry.Attrs, distinct)
		for i := range attrs {
			attrs[i] = telemetry.Attrs{"a": "x", "n": i}
		}
		b.ReportAllocs()
		b.ResetTimer()
		i := 0
		for b.Loop() {
			tr.Observe("tailscale.metric.bench", attrs[i%distinct])
			i++
		}
	})

	b.Run("high", func(b *testing.B) {
		tr := telemetry.NewCardinalityTracker()
		b.ReportAllocs()
		b.ResetTimer()
		i := 0
		for b.Loop() {
			tr.Observe("tailscale.metric.bench", telemetry.Attrs{
				"a": "x",
				"n": strconv.Itoa(i),
			})
			i++
		}
	})
}

// BenchmarkProviderSet_NewAndShutdown measures the cost of standing up and
// tearing down a ProviderSet (one process provider + N tailnet providers) at
// tailnet counts 1, 10, and 50. Protocol "stdout" with StdoutWriter set to
// io.Discard so this never dials out — no OTLP endpoint, no network egress,
// matching the pattern used by providerset_test.go and provider_test.go.
func BenchmarkProviderSet_NewAndShutdown(b *testing.B) {
	for _, n := range []int{1, 10, 50} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			tailnets := make([]telemetry.PerTailnetOptions, n)
			for i := range tailnets {
				tailnets[i] = telemetry.PerTailnetOptions{
					Name:       fmt.Sprintf("tailnet-%d.example.com", i),
					InstanceID: fmt.Sprintf("host/tailnet-%d", i),
				}
			}
			base := telemetry.Options{
				ServiceName:  "tailscale2otel",
				Protocol:     "stdout",
				StdoutWriter: io.Discard,
			}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				ps, err := telemetry.NewProviderSet(ctx, base, tailnets)
				if err != nil {
					b.Fatalf("NewProviderSet: %v", err)
				}
				if err := ps.Shutdown(ctx); err != nil {
					b.Fatalf("Shutdown: %v", err)
				}
			}
		})
	}
}
