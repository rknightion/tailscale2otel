package flowlog

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// benchLiveRecordJSON mirrors the shared liveRecordJSON fixture in
// nodemeta_test.go (package flowlog_test, so not reachable from here — this
// internal test package needs its own copy of the same record shape). It is
// deliberately identical to what BenchmarkProcessWithPolicy in policy_test.go
// decodes, so the raw hot-path numbers are directly comparable.
const benchLiveRecordJSON = `{
  "logged": "2026-07-24T07:00:09.000000000Z",
  "nodeId": "nSrcCNTRL",
  "start":  "2026-07-24T07:00:00.000000000Z",
  "end":    "2026-07-24T07:00:05.000000000Z",
  "srcNode": {
    "nodeId": "nSrcCNTRL",
    "name": "camden.example.ts.net",
    "addresses": ["100.64.0.1", "fd7a:115c:a1e0::1"],
    "tags": ["tag:servers", "tag:sshrecorder"]
  },
  "dstNodes": [
    {
      "nodeId": "nDstCNTRL",
      "name": "mbp16.example.ts.net",
      "addresses": ["100.64.0.2"],
      "os": "macOS",
      "user": "rob@example.com"
    },
    {
      "nodeId": "nBareCNTRL",
      "name": "jules.example.ts.net",
      "addresses": ["100.64.0.3"],
      "tags": ["tag:servers"]
    }
  ],
  "virtualTraffic": [
    {"proto": 6, "src": "100.64.0.1:443", "dst": "100.64.0.2:51820",
     "txPkts": 10, "txBytes": 1000, "rxPkts": 8, "rxBytes": 800}
  ]
}`

// benchDiscardStore is the cheapest possible Recorder, so a benchmark measures
// the processor rather than the store (mirrors discardStore in policy_test.go,
// which lives in the external flowlog_test package and so isn't reachable
// here).
type benchDiscardStore struct{}

func (benchDiscardStore) Record(flowstore.Observation) {}

// benchDecodeLiveRecord decodes benchLiveRecordJSON once; callers must do this
// during benchmark setup, never inside the timed loop.
// It takes testing.TB rather than *testing.B so the allocation-budget gate in
// benchbudget_internal_test.go decodes the same fixture as the benchmarks; a
// gate measuring a different record than the benchmark it guards would be
// asserting against a number nobody ever reads.
func benchDecodeLiveRecord(tb testing.TB) FlowLog {
	tb.Helper()
	var fl FlowLog
	if err := json.Unmarshal([]byte(benchLiveRecordJSON), &fl); err != nil {
		tb.Fatalf("decode: %v", err)
	}
	return fl
}

// BenchmarkProcess_RawMode drives Process in "all" mode (per-connection raw
// io/packets, no rollup accumulation, no policy) — the library default.
func BenchmarkProcess_RawMode(b *testing.B) {
	fl := benchDecodeLiveRecord(b)
	p := NewProcessor(enrich.NewDeviceCache(), Options{
		FlowMetricsMode: flowModeAll,
		Store:           benchDiscardStore{},
		LogMode:         "off",
	})
	e := telemetrytest.New().Emitter()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p.Process(fl, e)
	}
}

// BenchmarkProcess_RollupMode drives Process in "rollup" mode: every
// connection folds into the bounded rollupAccumulator instead of emitting raw
// per-connection points.
func BenchmarkProcess_RollupMode(b *testing.B) {
	fl := benchDecodeLiveRecord(b)
	p := NewProcessor(enrich.NewDeviceCache(), Options{
		FlowMetricsMode: flowModeRollup,
		Store:           benchDiscardStore{},
		LogMode:         "off",
	})
	e := telemetrytest.New().Emitter()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p.Process(fl, e)
	}
}

// BenchmarkProcess_BothMode drives Process in "both" mode: raw per-connection
// emission AND rollup accumulation happen on every call.
func BenchmarkProcess_BothMode(b *testing.B) {
	fl := benchDecodeLiveRecord(b)
	p := NewProcessor(enrich.NewDeviceCache(), Options{
		FlowMetricsMode: flowModeBoth,
		Store:           benchDiscardStore{},
		LogMode:         "off",
	})
	e := telemetrytest.New().Emitter()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p.Process(fl, e)
	}
}

// BenchmarkProcess_IdentityDims isolates the marginal cost of turning on the
// per-flow identity dimensions (user/tags/os) on top of node dimensions, in
// "all" mode — see the Options.IdentityDims doc: identity is meant to be
// nearly free once node dimensions are already on.
func BenchmarkProcess_IdentityDims(b *testing.B) {
	variants := []struct {
		name     string
		identity bool
	}{
		{"off", false},
		{"on", true},
	}
	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			fl := benchDecodeLiveRecord(b)
			p := NewProcessor(enrich.NewDeviceCache(), Options{
				FlowMetricsMode: flowModeAll,
				NodeDims:        true,
				IdentityDims:    v.identity,
				Store:           benchDiscardStore{},
				LogMode:         "off",
			})
			e := telemetrytest.New().Emitter()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.Process(fl, e)
			}
		})
	}
}

// BenchmarkRollupAccumulator_Record drives record/observeUnique with enough
// distinct keys to blow past maxRollupKeys, so the bounded __other__-folding
// path is exercised on every iteration rather than only the happy path — the
// same key-flood idiom as TestRollupAccumulatorBoundsEntriesUnderKeyFlood in
// rollup_internal_test.go, but run inside the timed loop instead of a fixed
// count so it reports steady-state per-record cost under permanent pressure.
func BenchmarkRollupAccumulator_Record(b *testing.B) {
	a := newRollupAccumulator(500, true, false)
	// Pre-flood the accumulator once so the timed loop starts already past
	// maxRollupKeys and every iteration take the fold branch, not the
	// map-growth branch.
	for i := 0; i < maxRollupKeys+5000; i++ {
		node := strconv.Itoa(i)
		a.record(rollupDims{
			transport: "tcp", trafficType: "virtual",
			srcNode: node, dstNode: node, dstService: "https",
		}, 1, 1, 1, 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		node := strconv.Itoa(maxRollupKeys + 5000 + i)
		a.record(rollupDims{
			transport: "tcp", trafficType: "virtual",
			srcNode: node, dstNode: node, dstService: "https",
		}, 1, 1, 1, 1)
		a.observeUnique(node, "peer", "443")
	}
}
