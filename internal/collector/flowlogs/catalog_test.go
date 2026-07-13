package flowlogs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// fakeAPI is a canned NetworkFlowLogs source so the test can drive a full
// CollectWindow without a live Tailscale API.
type fakeAPI struct {
	resp flowlog.NetworkResponse
}

func (f *fakeAPI) NetworkFlowLogs(_ context.Context, _, _ time.Time) (flowlog.NetworkResponse, error) {
	return f.resp, nil
}

// newProcessor mirrors flowlogs_test.go: a real Processor over an empty cache.
func newProcessor() *flowlog.Processor {
	return flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{NodeDims: true})
}

// oneTCPResponse is a NetworkResponse with a single TCP virtual connection,
// mirroring flowlogs_test.go.
func oneTCPResponse() flowlog.NetworkResponse {
	return flowlog.NetworkResponse{
		Logs: []flowlog.FlowLog{
			{
				NodeID: "n-laptop",
				Start:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC),
				Logged: time.Date(2026, 6, 2, 12, 1, 5, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{
						Proto:   6,
						Src:     "100.64.0.1:12345",
						Dst:     "100.64.0.2:443",
						TxPkts:  10,
						TxBytes: 1000,
						RxPkts:  8,
						RxBytes: 800,
					},
				},
			},
		},
	}
}

// TestCatalogMatchesEmitted is the declaration<->emission drift guard for this
// collector's OWN metric (tailscale.feature.enabled). docs/metrics.md is
// generated from Catalog(), so this keeps the generated docs honest: the one
// feature gauge this package emits must be declared with a matching unit,
// instrument, and description.
//
// CollectWindow also drives the shared flowlog.Processor, which emits
// tailscale.network.* metrics and flow log records that are NOT this package's
// own (they are cataloged in internal/flowlog). So we only treat an emitted
// metric named "tailscale.feature." as a potential drift error, and we do not
// assert anything about emitted log records (the flow logs are downstream).
func TestCatalogMatchesEmitted(t *testing.T) {
	rec := telemetrytest.New()
	c := flowlogs.New(
		&fakeAPI{resp: oneTCPResponse()},
		newProcessor(),
		0, 0,
		func(context.Context) (bool, error) { return true, nil },
		nil,
	)

	from := time.Date(2026, 6, 2, 11, 58, 0, 0, time.UTC)
	to := time.Date(2026, 6, 2, 11, 59, 0, 0, time.UTC)
	if _, err := c.CollectWindow(context.Background(), from, to, rec.Emitter()); err != nil {
		t.Fatalf("CollectWindow() error = %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range flowlogs.Catalog() {
		declared[m.Name] = m
	}

	// (b) tailscale.feature.enabled must actually be emitted and declared.
	const featureEnabled = "tailscale.feature.enabled"
	featPts := rec.MetricPoints(featureEnabled)
	if len(featPts) == 0 {
		t.Fatalf("expected %q to be emitted by CollectWindow with feature enabled", featureEnabled)
	}
	if _, ok := declared[featureEnabled]; !ok {
		t.Fatalf("%q is emitted but not declared in flowlogs.Catalog()", featureEnabled)
	}

	for _, name := range rec.MetricNames() {
		// (a) Only this package's own feature.* metrics are in scope; the
		// network.* metrics come from the shared processor and are cataloged
		// in internal/flowlog.
		if !strings.HasPrefix(name, "tailscale.feature.") {
			continue
		}
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in flowlogs.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}
}
