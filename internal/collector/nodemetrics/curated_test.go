package nodemetrics_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// curatedFixtureScrape1 and curatedFixtureScrape2 are two consecutive tailscaled
// client-metric scrapes (client v1.94+ format) of the same node, the second
// incremented, so counter families produce a delta on scrape 2. The label names
// (`path`, `reason`, `type`) and the raw `path` value set
// (direct_ipv4|direct_ipv6|derp|peer_relay_ipv4|peer_relay_ipv6) mirror the real
// tailscaled exposition documented in docs/node-metrics.md. The `reason` value
// set now mirrors the seven documented upstream values (see
// tailscale.com/docs/reference/tailscale-client-metrics) — see
// TestCurated_DropReasonAllSevenValues for individual coverage of all seven plus
// the unknown-value fold.
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

// TestCurated_DropReasonAllSevenValues verifies foldDropReason admits every one
// of the seven documented tailscaled reason values individually (each preserved
// as-is, not folded to "other"), while a genuinely unknown/future value still
// folds to "other". Regression guard for #429: before the fix, only "acl" and
// "error" were admitted and the other five documented values (multicast,
// link_local_unicast, too_short, fragment, unknown_protocol) were silently
// collapsed into "other" alongside truly unknown values.
func TestCurated_DropReasonAllSevenValues(t *testing.T) {
	body1 := `# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="acl"} 1
tailscaled_inbound_dropped_packets_total{reason="multicast"} 1
tailscaled_inbound_dropped_packets_total{reason="link_local_unicast"} 1
tailscaled_inbound_dropped_packets_total{reason="too_short"} 1
tailscaled_inbound_dropped_packets_total{reason="fragment"} 1
tailscaled_inbound_dropped_packets_total{reason="unknown_protocol"} 1
tailscaled_inbound_dropped_packets_total{reason="error"} 1
tailscaled_inbound_dropped_packets_total{reason="totally_novel_future_reason"} 1
`
	body2 := `# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="acl"} 3
tailscaled_inbound_dropped_packets_total{reason="multicast"} 4
tailscaled_inbound_dropped_packets_total{reason="link_local_unicast"} 5
tailscaled_inbound_dropped_packets_total{reason="too_short"} 6
tailscaled_inbound_dropped_packets_total{reason="fragment"} 7
tailscaled_inbound_dropped_packets_total{reason="unknown_protocol"} 8
tailscaled_inbound_dropped_packets_total{reason="error"} 9
tailscaled_inbound_dropped_packets_total{reason="totally_novel_future_reason"} 10
`
	rec := scrapeTwice(t, nodemetrics.Options{}, body1, body2)

	wantDeltas := map[string]float64{
		"acl":                2,
		"multicast":          3,
		"link_local_unicast": 4,
		"too_short":          5,
		"fragment":           6,
		"unknown_protocol":   7,
		"error":              8,
	}
	for reason, want := range wantDeltas {
		wantCounter(t, rec, "tailscale.node.packets.dropped",
			map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.drop.reason": reason}, want)
	}
	// A genuinely unknown/future reason still folds to "other" (delta 10-1=9).
	wantCounter(t, rec, "tailscale.node.packets.dropped",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.drop.reason": "other"}, 9)
}

// TestCurated_PeerRelayTransportAttrs verifies the peer-relay forwarded
// bytes/packets curated counters carry the bounded transport_in/transport_out
// attributes: udp4 and udp6 pass through as-is, and an unrecognized transport
// value folds to "other" independently on each of the two label positions.
func TestCurated_PeerRelayTransportAttrs(t *testing.T) {
	body1 := `# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="udp4"} 1000
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp6",transport_out="udp6"} 2000
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="tcp"} 3000
# TYPE tailscaled_peer_relay_forwarded_packets_total counter
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp4",transport_out="udp4"} 10
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp6",transport_out="udp6"} 20
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp4",transport_out="tcp"} 30
`
	body2 := `# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="udp4"} 1100
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp6",transport_out="udp6"} 2300
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="tcp"} 3600
# TYPE tailscaled_peer_relay_forwarded_packets_total counter
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp4",transport_out="udp4"} 11
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp6",transport_out="udp6"} 25
tailscaled_peer_relay_forwarded_packets_total{transport_in="udp4",transport_out="tcp"} 39
`
	rec := scrapeTwice(t, nodemetrics.Options{}, body1, body2)

	wantCounter(t, rec, "tailscale.node.peer_relay.io",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp4", "tailscale.peer_relay.transport_out": "udp4"}, 100)
	wantCounter(t, rec, "tailscale.node.peer_relay.io",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp6", "tailscale.peer_relay.transport_out": "udp6"}, 300)
	wantCounter(t, rec, "tailscale.node.peer_relay.io",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp4", "tailscale.peer_relay.transport_out": "other"}, 600)

	wantCounter(t, rec, "tailscale.node.peer_relay.packets",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp4", "tailscale.peer_relay.transport_out": "udp4"}, 1)
	wantCounter(t, rec, "tailscale.node.peer_relay.packets",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp6", "tailscale.peer_relay.transport_out": "udp6"}, 5)
	wantCounter(t, rec, "tailscale.node.peer_relay.packets",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp4", "tailscale.peer_relay.transport_out": "other"}, 9)
}

// TestCurated_PeerRelayEndpointStateAttrs verifies the peer-relay endpoints
// curated gauge carries the bounded state attribute: connecting and open pass
// through as-is, and an unrecognized state folds to "other".
func TestCurated_PeerRelayEndpointStateAttrs(t *testing.T) {
	body := `# TYPE tailscaled_peer_relay_endpoints gauge
tailscaled_peer_relay_endpoints{state="connecting"} 2
tailscaled_peer_relay_endpoints{state="open"} 5
tailscaled_peer_relay_endpoints{state="draining"} 1
`
	srv := serveText(&body)
	defer srv.Close()
	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wantGauge(t, rec, "tailscale.node.peer_relay.endpoints",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.state": "connecting"}, 2)
	wantGauge(t, rec, "tailscale.node.peer_relay.endpoints",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.state": "open"}, 5)
	wantGauge(t, rec, "tailscale.node.peer_relay.endpoints",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.state": "other"}, 1)
}

// TestCurated_PeerRelayAndDropReasonBypassFiltersAndDropLabels is the regression
// guard proving the newly-widened curated attribute derivers stay on the
// "curated metrics bypass passthrough filters" contract (see curated.go's
// package doc and TestCurated_BypassesMetricFilters): metric_deny suppresses the
// raw forward as before, and DropLabels configured for the exact raw label keys
// the new derivers read (transport_in/transport_out/state/reason) strips them
// from the RAW forward's attrs but never reaches the curated attribute deriver,
// which reads s.labels directly. drop_labels never touches curated series.
func TestCurated_PeerRelayAndDropReasonBypassFiltersAndDropLabels(t *testing.T) {
	body1 := `# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="udp6"} 1000
# TYPE tailscaled_peer_relay_endpoints gauge
tailscaled_peer_relay_endpoints{state="open"} 2
# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="fragment"} 1
`
	body2 := `# TYPE tailscaled_peer_relay_forwarded_bytes_total counter
tailscaled_peer_relay_forwarded_bytes_total{transport_in="udp4",transport_out="udp6"} 1300
# TYPE tailscaled_peer_relay_endpoints gauge
tailscaled_peer_relay_endpoints{state="open"} 4
# TYPE tailscaled_inbound_dropped_packets_total counter
tailscaled_inbound_dropped_packets_total{reason="fragment"} 6
`
	opts := nodemetrics.Options{
		MetricDeny: []string{"tailscaled_.*"},
		DropLabels: []string{"transport_in", "transport_out", "state", "reason"},
	}
	rec := scrapeTwice(t, opts, body1, body2)

	// Raw forward suppressed entirely by the deny filter.
	if pts := rec.MetricPoints("tailscaled_peer_relay_forwarded_bytes_total"); len(pts) != 0 {
		t.Fatalf("raw tailscaled_peer_relay_forwarded_bytes_total = %+v, want none (denied)", pts)
	}
	if pts := rec.MetricPoints("tailscaled_inbound_dropped_packets_total"); len(pts) != 0 {
		t.Fatalf("raw tailscaled_inbound_dropped_packets_total = %+v, want none (denied)", pts)
	}

	// Curated series still carry their full folded attributes despite
	// DropLabels naming the exact raw label keys the derivers read.
	wantCounter(t, rec, "tailscale.node.peer_relay.io",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.transport_in": "udp4", "tailscale.peer_relay.transport_out": "udp6"}, 300)
	wantGauge(t, rec, "tailscale.node.peer_relay.endpoints",
		map[string]string{"tailscale.node": "node-a", "tailscale.peer_relay.state": "open"}, 4)
	wantCounter(t, rec, "tailscale.node.packets.dropped",
		map[string]string{"tailscale.node": "node-a", "network.io.direction": "receive", "tailscale.drop.reason": "fragment"}, 5)
}
