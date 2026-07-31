package config

import (
	"strings"
	"testing"
)

// #525. Two flow-cardinality knobs are read by nothing under the wrong
// metrics_mode. Both are the same bug class as flows.capacity_profile under
// flows.store.directory: the operator asks for MORE data, the config validates,
// the docs describe the key, and nothing is ever emitted.
//
// The firing cases matter, but so do the silent ones — an advisory that fires
// when the mode DOES consume the key, or when the key is at its default, is
// noise in a startup path whose value depends on every line being worth reading.

// warningFor returns the single warning mentioning key, or "" if none does.
// It fails if more than one matches, so a broadened message cannot silently
// start matching two assertions.
func warningFor(t *testing.T, c *Config, key string) string {
	t.Helper()
	var found []string
	for _, w := range c.Warnings() {
		if strings.HasPrefix(w, key) {
			found = append(found, w)
		}
	}
	if len(found) > 1 {
		t.Fatalf("%d warnings start with %q, want at most 1: %v", len(found), key, found)
	}
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

func TestWarnings_FlowPortsInertUnderRollupMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		src, dst bool
		want     bool
	}{
		{"source_port under the default rollup mode", "rollup", true, false, true},
		{"destination_port under the default rollup mode", "rollup", false, true, true},
		{"both ports under rollup", "rollup", true, true, true},
		// `all` and `both` construct the raw per-connection family, which is the
		// only place p.srcPort/p.dstPort are read.
		{"source_port under all", "all", true, false, false},
		{"destination_port under all", "all", false, true, false},
		{"both ports under both", "both", true, true, false},
		// Neither port requested: nothing was asked for, so nothing was lost.
		{"no ports under rollup", "rollup", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Cardinality.Flow.MetricsMode = tc.mode
			c.Cardinality.Flow.SourcePort = tc.src
			c.Cardinality.Flow.DestinationPort = tc.dst

			// One advisory PER KEY, not one combined message: advisoryKey takes the
			// first token, so a combined message could only ever be attributed to
			// one of the two on the status page.
			srcWarn := warningFor(t, c, "cardinality.flow.source_port")
			dstWarn := warningFor(t, c, "cardinality.flow.destination_port")

			wantSrc := tc.want && tc.src
			wantDst := tc.want && tc.dst
			if (srcWarn != "") != wantSrc {
				t.Errorf("source_port warning present = %v, want %v (mode=%q)\ngot: %s",
					srcWarn != "", wantSrc, tc.mode, srcWarn)
			}
			if (dstWarn != "") != wantDst {
				t.Errorf("destination_port warning present = %v, want %v (mode=%q)\ngot: %s",
					dstWarn != "", wantDst, tc.mode, dstWarn)
			}
		})
	}
}

func TestWarnings_RollupTopNInertUnderAllMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		topN int
		want bool
	}{
		{"tuned top_n under all", "all", 1200, true},
		// rollup/both build the accumulator, which is the only consumer.
		{"tuned top_n under rollup", "rollup", 1200, false},
		{"tuned top_n under both", "both", 1200, false},
		// At its default the operator chose nothing; saying so is noise.
		{"default top_n under all", "all", defaultFlowRollupTopN, false},
		// 0 means "use the default" per the field doc, so it is not a tuning
		// either — warning about it would be wrong, not merely noisy.
		{"zero top_n under all", "all", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Cardinality.Flow.MetricsMode = tc.mode
			c.Cardinality.Flow.RollupTopN = tc.topN

			got := warningFor(t, c, "cardinality.flow.rollup_top_n")
			if (got != "") != tc.want {
				t.Fatalf("warning present = %v, want %v (mode=%q top_n=%d)\ngot: %s",
					got != "", tc.want, tc.mode, tc.topN, got)
			}
		})
	}
}

// TestAdvisories_InertFlowCardinalityKeys pins the message FORMAT. advisoryKey
// takes the first whitespace-delimited token and cuts at "=", so a message that
// does not open with the dotted key yields an empty key column on the status
// page — the advisory would still fire and still be unattributable.
func TestAdvisories_InertFlowCardinalityKeys(t *testing.T) {
	tests := []struct {
		name string
		tune func(*Config)
		want string
	}{
		{"source_port", func(c *Config) {
			c.Cardinality.Flow.MetricsMode = "rollup"
			c.Cardinality.Flow.SourcePort = true
		}, "cardinality.flow.source_port"},
		{"destination_port", func(c *Config) {
			c.Cardinality.Flow.MetricsMode = "rollup"
			c.Cardinality.Flow.DestinationPort = true
		}, "cardinality.flow.destination_port"},
		{"rollup_top_n", func(c *Config) {
			c.Cardinality.Flow.MetricsMode = "all"
			c.Cardinality.Flow.RollupTopN = 1200
		}, "cardinality.flow.rollup_top_n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.tune(c)

			var keys []string
			for _, a := range c.Advisories() {
				keys = append(keys, a.Key)
			}
			var found bool
			for _, k := range keys {
				if k == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("Advisories() keys = %v, want one equal to %q; the message must open "+
					"with the dotted config key or the status page shows an empty key column",
					keys, tc.want)
			}
		})
	}
}
