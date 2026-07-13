package nodemetrics_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// curatedFixtureScrape1 and curatedFixtureScrape2 are two consecutive tailscaled
// client-metric scrapes (client v1.94+ format) of the same node, the second
// incremented, so counter families produce a delta on scrape 2. The label names
// (`path`, `reason`, `type`) and the raw `path` value set
// (direct_ipv4|direct_ipv6|derp|peer_relay_ipv4|peer_relay_ipv6) mirror the real
// tailscaled exposition documented in docs/node-metrics.md. NOTE: the exact
// `reason` value set is constructed from the tailscaled client-metrics format,
// not a captured live scrape — live-scrape validation of the reason admit-set is
// recommended (see the lane brief).
const (
	curatedFixtureScrape1 = `# TYPE tailscaled_inbound_bytes_total counter
tailscaled_inbound_bytes_total{path="direct_ipv4"} 1000
tailscaled_inbound_bytes_total{path="derp"} 500
# TYPE tailscaled_outbound_bytes_total counter
tailscaled_outbound_bytes_total{path="peer_relay_ipv6"} 200
# TYPE tailscaled_inbound_packets_total counter
tailscaled_inbound_packets_total{path="direct_ipv4"} 10
# TYPE tailscaled_outbound_packets_total counter
tailscaled_outbound_packets_total{path="direct_ipv6"} 8
# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="acl"} 3
tailscaled_inbound_dropped_packets_total{reason="error"} 1
# TYPE tailscaled_outbound_dropped_packets_total counter
tailscaled_outbound_dropped_packets_total{reason="somethingnew"} 2
# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total 4096
# TYPE tailscaled_peer_relay_forwarded_packets_total counter
tailscaled_peer_relay_forwarded_packets_total 40
# TYPE tailscaled_peer_relay_endpoints gauge
tailscaled_peer_relay_endpoints 2
# TYPE tailscaled_health_messages gauge
tailscaled_health_messages{type="warming-up"} 1
# TYPE tailscaled_home_derp_region_id gauge
tailscaled_home_derp_region_id 5
`

	curatedFixtureScrape2 = `# TYPE tailscaled_inbound_bytes_total counter
tailscaled_inbound_bytes_total{path="direct_ipv4"} 1200
tailscaled_inbound_bytes_total{path="derp"} 650
# TYPE tailscaled_outbound_bytes_total counter
tailscaled_outbound_bytes_total{path="peer_relay_ipv6"} 260
# TYPE tailscaled_inbound_packets_total counter
tailscaled_inbound_packets_total{path="direct_ipv4"} 15
# TYPE tailscaled_outbound_packets_total counter
tailscaled_outbound_packets_total{path="direct_ipv6"} 12
# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="acl"} 7
tailscaled_inbound_dropped_packets_total{reason="error"} 4
# TYPE tailscaled_outbound_dropped_packets_total counter
tailscaled_outbound_dropped_packets_total{reason="somethingnew"} 5
# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total 5120
# TYPE tailscaled_peer_relay_forwarded_packets_total counter
tailscaled_peer_relay_forwarded_packets_total 55
# TYPE tailscaled_peer_relay_endpoints gauge
tailscaled_peer_relay_endpoints 3
# TYPE tailscaled_health_messages gauge
tailscaled_health_messages{type="warming-up"} 1
# TYPE tailscaled_home_derp_region_id gauge
tailscaled_home_derp_region_id 5
`
)

// scrapeTwice scrapes the given collector against body1 then body2 (mutating the
// served payload between scrapes), returning a fresh recorder capturing ONLY the
// second scrape's emissions — so counters show their delta and gauges their
// current value.
func scrapeTwice(t *testing.T, opts nodemetrics.Options, body1, body2 string) *telemetrytest.Recorder {
	t.Helper()
	body := body1
	srv := serveText(&body)
	defer srv.Close()
	opts.Targets = []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}}
	c := nodemetrics.New(opts)

	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}
	body = body2
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	return rec2
}

// wantCounter asserts a single curated counter point exists with the given attrs
// and delta value, and is emitted as a monotonic sum.
func wantCounter(t *testing.T, rec *telemetrytest.Recorder, name string, attrs map[string]string, want float64) {
	t.Helper()
	p, ok := pointByAttr(rec.MetricPoints(name), attrs)
	if !ok {
		t.Fatalf("%s: no point matching %v; pts=%+v", name, attrs, rec.MetricPoints(name))
	}
	if p.Kind != "sum" || !p.Monotonic {
		t.Errorf("%s%v: kind=%q monotonic=%v, want sum/monotonic", name, attrs, p.Kind, p.Monotonic)
	}
	if p.Value != want {
		t.Errorf("%s%v: value = %v, want %v", name, attrs, p.Value, want)
	}
}

// wantGauge asserts a single curated gauge point exists with the given attrs and
// value.
func wantGauge(t *testing.T, rec *telemetrytest.Recorder, name string, attrs map[string]string, want float64) {
	t.Helper()
	p, ok := pointByAttr(rec.MetricPoints(name), attrs)
	if !ok {
		t.Fatalf("%s: no point matching %v; pts=%+v", name, attrs, rec.MetricPoints(name))
	}
	if p.Kind != "gauge" {
		t.Errorf("%s%v: kind=%q, want gauge", name, attrs, p.Kind)
	}
	if p.Value != want {
		t.Errorf("%s%v: value = %v, want %v", name, attrs, p.Value, want)
	}
}

// TestCurated_CountersMappedWithDeltas verifies every curated counter family is
// emitted with the shared delta, the mapped direction/path/reason attributes,
// and the node identity — folding the raw path/reason values to the bounded
// curated set.
func TestCurated_CountersMappedWithDeltas(t *testing.T) {
	rec := scrapeTwice(t, nodemetrics.Options{}, curatedFixtureScrape1, curatedFixtureScrape2)

	// tailscale.node.io: bytes, by direction + folded path.
	wantCounter(t, rec, "tailscale.node.io",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.path": "direct"}, 200)
	wantCounter(t, rec, "tailscale.node.io",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.path": "derp"}, 150)
	wantCounter(t, rec, "tailscale.node.io",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "transmit", "tailscale.path": "peer_relay"}, 60)

	// tailscale.node.packets: packets, by direction + folded path.
	wantCounter(t, rec, "tailscale.node.packets",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.path": "direct"}, 5)
	wantCounter(t, rec, "tailscale.node.packets",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "transmit", "tailscale.path": "direct"}, 4)

	// tailscale.node.packets.dropped: by direction + bounded reason (unknown -> other).
	wantCounter(t, rec, "tailscale.node.packets.dropped",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.drop.reason": "acl"}, 4)
	wantCounter(t, rec, "tailscale.node.packets.dropped",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.drop.reason": "error"}, 3)
	wantCounter(t, rec, "tailscale.node.packets.dropped",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "transmit", "tailscale.drop.reason": "other"}, 3)

	// tailscale.node.peer_relay.io / .packets: node identity only.
	wantCounter(t, rec, "tailscale.node.peer_relay.io",
		map[string]string{"tailscale.node": "node-a"}, 1024)
	wantCounter(t, rec, "tailscale.node.peer_relay.packets",
		map[string]string{"tailscale.node": "node-a"}, 15)
}

// TestCurated_GaugesMapped verifies the curated gauge families carry the mapped
// attributes and current value.
func TestCurated_GaugesMapped(t *testing.T) {
	rec := scrapeTwice(t, nodemetrics.Options{}, curatedFixtureScrape1, curatedFixtureScrape2)

	wantGauge(t, rec, "tailscale.node.health_messages",
		map[string]string{"tailscale.node": "node-a", "tailscale.health.type": "warming-up"}, 1)
	wantGauge(t, rec, "tailscale.node.derp.home_region",
		map[string]string{"tailscale.node": "node-a"}, 5)
	wantGauge(t, rec, "tailscale.node.peer_relay.endpoints",
		map[string]string{"tailscale.node": "node-a"}, 3)
}

// TestCurated_PathFoldSumsIPVersions verifies direct_ipv4 and direct_ipv6 both
// fold to path=direct and their deltas SUM into one curated series (the fold is a
// deliberate cardinality reduction).
func TestCurated_PathFoldSumsIPVersions(t *testing.T) {
	body1 := `# TYPE tailscaled_inbound_bytes_total counter
tailscaled_inbound_bytes_total{path="direct_ipv4"} 100
tailscaled_inbound_bytes_total{path="direct_ipv6"} 100
`
	body2 := `# TYPE tailscaled_inbound_bytes_total counter
tailscaled_inbound_bytes_total{path="direct_ipv4"} 130
tailscaled_inbound_bytes_total{path="direct_ipv6"} 170
`
	rec := scrapeTwice(t, nodemetrics.Options{}, body1, body2)
	// ipv4 delta 30 + ipv6 delta 70 = 100 in the single folded path=direct bucket.
	wantCounter(t, rec, "tailscale.node.io",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.path": "direct"}, 100)
}

// TestCurated_GaugeChurnDropsDepartedSeries verifies the curated gauges use
// snapshot semantics like tailscale.node.up: a health type present in one scrape
// but gone the next drops OUT of the export instead of ghosting at its last
// value. Reuses one recorder across both scrapes (a synchronous gauge under
// cumulative temporality would keep re-exporting the departed series).
func TestCurated_GaugeChurnDropsDepartedSeries(t *testing.T) {
	body := "# TYPE tailscaled_health_messages gauge\ntailscaled_health_messages{type=\"warming-up\"} 1\n"
	srv := serveText(&body)
	defer srv.Close()
	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("collect 1: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.node.health_messages"); len(pts) != 1 {
		t.Fatalf("tick 1: health_messages series = %d, want 1", len(pts))
	}

	// The warming-up warning clears and a different one appears.
	body = "# TYPE tailscaled_health_messages gauge\ntailscaled_health_messages{type=\"update-available\"} 1\n"
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("collect 2: %v", err)
	}
	pts := rec.MetricPoints("tailscale.node.health_messages")
	if len(pts) != 1 {
		t.Fatalf("tick 2: health_messages series = %d, want 1 (warming-up must drop, not ghost); pts=%+v", len(pts), pts)
	}
	if pts[0].Attrs["tailscale.health.type"] != "update-available" {
		t.Errorf("tick 2 surviving type = %q, want update-available", pts[0].Attrs["tailscale.health.type"])
	}
}

// TestCurated_BypassesMetricFilters verifies curation is emitted even when the
// raw forward is fully denied by metric_deny (curated metrics are catalog
// metrics, not passthrough) — and that the raw series is indeed suppressed.
func TestCurated_BypassesMetricFilters(t *testing.T) {
	rec := scrapeTwice(t, nodemetrics.Options{MetricDeny: []string{"tailscaled_.*"}},
		curatedFixtureScrape1, curatedFixtureScrape2)

	// Raw forward suppressed by the deny filter.
	if pts := rec.MetricPoints("tailscaled_inbound_bytes_total"); len(pts) != 0 {
		t.Fatalf("raw tailscaled_inbound_bytes_total = %+v, want none (denied)", pts)
	}
	// Curated counter still emitted (filter bypassed) with the shared delta.
	wantCounter(t, rec, "tailscale.node.io",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.path": "direct"}, 200)
	// Curated gauge still emitted.
	wantGauge(t, rec, "tailscale.node.derp.home_region",
		map[string]string{"tailscale.node": "node-a"}, 5)
}

// TestCurated_RawForwardByteIdentical is the guard test: with curation active,
// every raw tailscaled_* series is still forwarded VERBATIM — original name,
// original (unfolded) labels, and the same delta value the curated series
// consumes. Curation adds tailscale.node.* series ON TOP; it never mutates or
// suppresses the raw forward.
func TestCurated_RawForwardByteIdentical(t *testing.T) {
	rec := scrapeTwice(t, nodemetrics.Options{}, curatedFixtureScrape1, curatedFixtureScrape2)

	// Raw counter kept its ORIGINAL name and UNFOLDED path label, empty unit, and
	// the same delta the curated tailscale.node.io{receive,direct} series carries.
	raw, ok := pointByAttr(rec.MetricPoints("tailscaled_inbound_bytes_total"),
		map[string]string{"tailscale.node": "node-a", "path": "direct_ipv4"})
	if !ok {
		t.Fatalf("raw tailscaled_inbound_bytes_total{path=direct_ipv4} not forwarded; pts=%+v",
			rec.MetricPoints("tailscaled_inbound_bytes_total"))
	}
	if raw.Kind != "sum" || !raw.Monotonic {
		t.Errorf("raw kind=%q monotonic=%v, want sum/monotonic", raw.Kind, raw.Monotonic)
	}
	if raw.Value != 200 {
		t.Errorf("raw delta = %v, want 200 (byte-identical to pre-curation)", raw.Value)
	}
	if raw.Unit != "" {
		t.Errorf("raw unit = %q, want empty (verbatim forward)", raw.Unit)
	}
	// The raw series must NOT carry the curated folded attribute keys.
	if _, bad := raw.Attrs["tailscale.path"]; bad {
		t.Errorf("raw series leaked curated attr tailscale.path = %v", raw.Attrs["tailscale.path"])
	}

	// The raw drop counter kept its ORIGINAL unfolded reason value.
	if _, ok := pointByAttr(rec.MetricPoints("tailscaled_outbound_dropped_packets_total"),
		map[string]string{"tailscale.node": "node-a", "reason": "somethingnew"}); !ok {
		t.Fatalf("raw tailscaled_outbound_dropped_packets_total{reason=somethingnew} not forwarded verbatim; pts=%+v",
			rec.MetricPoints("tailscaled_outbound_dropped_packets_total"))
	}

	// The raw gauge is still forwarded verbatim alongside the curated gauge.
	if _, ok := pointByAttr(rec.MetricPoints("tailscaled_home_derp_region_id"),
		map[string]string{"tailscale.node": "node-a"}); !ok {
		t.Fatalf("raw tailscaled_home_derp_region_id not forwarded; pts=%+v",
			rec.MetricPoints("tailscaled_home_derp_region_id"))
	}
}
