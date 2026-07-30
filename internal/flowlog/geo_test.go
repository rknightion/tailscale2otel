package flowlog

import (
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/geoip"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// fakeGeo answers from a fixed table, so these tests assert the PROCESSOR's
// behavior rather than MaxMind's data.
type fakeGeo map[string]geoip.Result

func (f fakeGeo) Lookup(addr netip.Addr) (geoip.Result, bool) {
	r, ok := f[addr.String()]
	return r, ok
}

var testGeo = fakeGeo{
	"8.8.8.8":         {CountryISO: "US", ContinentCode: "NA", ASN: 15169, ASOrg: "Google LLC"},
	"185.199.108.153": {CountryISO: "DE", ContinentCode: "EU", ASN: 54113, ASOrg: "Fastly, Inc."},
}

// externalFlow is one virtual connection from a tailnet device to an external
// address, which is the shape geo enrichment exists for.
func externalFlow(dst string) FlowLog {
	return FlowLog{
		NodeID: "nodeA",
		Start:  time.Unix(1700000000, 0),
		End:    time.Unix(1700000060, 0),
		VirtualTraffic: []ConnectionCounts{{
			Proto:   6,
			Src:     "100.64.0.1:5000",
			Dst:     dst,
			TxBytes: 100,
			RxBytes: 200,
			TxPkts:  1,
			RxPkts:  2,
		}},
	}
}

// Flow LOGS always carry the full geo and ASN detail when geoip is on -- that is
// where the ASN lives, since it never reaches a metric.
func TestGeo_LogsCarryCountryAndASN(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor(nil, Options{Geo: testGeo})
	p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) == 0 {
		t.Fatal("no log records emitted")
	}
	attrs := logs[0].Attrs
	want := map[string]string{
		semconv.DestinationGeoCountryISO: "US",
		semconv.DestGeoContinentCode:     "NA",
		semconv.DestinationASOrg:         "Google LLC",
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("log attr %s = %q, want %q", k, attrs[k], v)
		}
	}
	// The AS number is emitted as an Int64 (ECS types as.number as a long, and a
	// consumer filtering on it should not have to parse a string). The
	// telemetrytest recorder stringifies log values via Value.AsString(), which
	// is empty for Int64 -- the same limitation processor_test.go works around
	// for the byte counters -- so assert presence here.
	if _, ok := attrs[semconv.DestinationASNumber]; !ok {
		t.Errorf("log attr %s is missing", semconv.DestinationASNumber)
	}
	// The tailnet source is never geolocated, so it carries no geo attributes at
	// all -- not an "unknown" placeholder.
	for _, k := range []string{semconv.SourceGeoCountryISO, semconv.SourceASNumber, semconv.SourceASOrg} {
		if _, ok := attrs[k]; ok {
			t.Errorf("log attr %s is present for a tailnet source: %q", k, attrs[k])
		}
	}
}

// An address the databases do not know produces no attributes rather than an
// "unknown" label: an absent attribute is queryable as absent, a fabricated one
// is a fact the data never supported.
func TestGeo_UnknownAddressEmitsNothing(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor(nil, Options{Geo: testGeo})
	p.Process(externalFlow("203.0.113.7:443"), rec.Emitter())

	attrs := rec.LogRecords()[0].Attrs
	for _, k := range []string{
		semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
		semconv.DestinationASNumber, semconv.DestinationASOrg,
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("log attr %s is present for an unknown address: %q", k, attrs[k])
		}
	}
}

// With no resolver wired at all, nothing changes -- geoip is opt-in and its
// absence must be indistinguishable from the pre-#461 behavior.
func TestGeo_DisabledEmitsNothing(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor(nil, Options{})
	p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())

	attrs := rec.LogRecords()[0].Attrs
	if _, ok := attrs[semconv.DestinationGeoCountryISO]; ok {
		t.Error("geo attributes emitted with no resolver configured")
	}
}

// GeoDims=false (the default) keeps country off METRICS while logs keep it.
func TestGeo_MetricsGatedOnGeoDims(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		rec := telemetrytest.New()
		p := NewProcessor(nil, Options{Geo: testGeo, FlowMetricsMode: flowModeAll, NodeDims: true})
		p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())

		for _, pt := range rec.MetricPoints(docIO.Name) {
			if _, ok := pt.Attrs[semconv.DestinationGeoCountryISO]; ok {
				t.Fatalf("metric carries a country label with geo_dims off: %v", pt.Attrs)
			}
		}
		// ...but the log for the same connection does.
		if got := rec.LogRecords()[0].Attrs[semconv.DestinationGeoCountryISO]; got != "US" {
			t.Errorf("log country = %q, want US even with geo_dims off", got)
		}
	})

	t.Run("on when enabled", func(t *testing.T) {
		rec := telemetrytest.New()
		p := NewProcessor(nil, Options{Geo: testGeo, GeoDims: true, FlowMetricsMode: flowModeAll, NodeDims: true})
		p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())

		var seen bool
		for _, pt := range rec.MetricPoints(docIO.Name) {
			if pt.Attrs[semconv.DestinationGeoCountryISO] == "US" {
				seen = true
				if pt.Attrs[semconv.DestGeoContinentCode] != "NA" {
					t.Errorf("metric continent = %q, want NA", pt.Attrs[semconv.DestGeoContinentCode])
				}
			}
		}
		if !seen {
			t.Error("no metric point carries the country label with geo_dims on")
		}
	})
}

// The ASN never reaches a metric, in any mode, with any toggle. It is bounded by
// nothing useful, and the logs answer that breakdown exactly. This is the
// cardinality contract of #461 and the test that keeps it honest.
func TestGeo_ASNNeverOnMetrics(t *testing.T) {
	for _, mode := range []string{flowModeAll, flowModeRollup, flowModeBoth} {
		t.Run(mode, func(t *testing.T) {
			rec := telemetrytest.New()
			p := NewProcessor(nil, Options{Geo: testGeo, GeoDims: true, FlowMetricsMode: mode, NodeDims: true})
			p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())
			p.FlushRollup(rec.Emitter())

			for _, name := range rec.MetricNames() {
				for _, pt := range rec.MetricPoints(name) {
					for _, k := range []string{
						semconv.SourceASNumber, semconv.SourceASOrg,
						semconv.DestinationASNumber, semconv.DestinationASOrg,
					} {
						if _, ok := pt.Attrs[k]; ok {
							t.Errorf("metric %s carries the AS attribute %s: %v", name, k, pt.Attrs)
						}
					}
				}
			}
		})
	}
}

// Geo on the rollup family is the cheap path -- it widens the bounded key rather
// than multiplying series -- so it has to actually reach the rollup, not just the
// raw families.
func TestGeo_RollupCarriesCountry(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor(nil, Options{Geo: testGeo, GeoDims: true, FlowMetricsMode: flowModeRollup, NodeDims: true})
	p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	var seen bool
	for _, pt := range rec.MetricPoints(docIORollup.Name) {
		if pt.Attrs[semconv.DestinationGeoCountryISO] == "US" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no rollup point carries the country label; got %+v", rec.MetricPoints(docIORollup.Name))
	}
}

// Two connections to different countries must remain distinct rollup series, or
// the dimension is being dropped somewhere in the key.
func TestGeo_RollupSeparatesCountries(t *testing.T) {
	rec := telemetrytest.New()
	p := NewProcessor(nil, Options{Geo: testGeo, GeoDims: true, FlowMetricsMode: flowModeRollup, NodeDims: true})
	p.Process(externalFlow("8.8.8.8:443"), rec.Emitter())
	p.Process(externalFlow("185.199.108.153:443"), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	countries := map[string]bool{}
	for _, pt := range rec.MetricPoints(docIORollup.Name) {
		if c := pt.Attrs[semconv.DestinationGeoCountryISO]; c != "" {
			countries[c] = true
		}
	}
	if !countries["US"] || !countries["DE"] {
		t.Fatalf("rollup countries = %v, want both US and DE", countries)
	}
}
