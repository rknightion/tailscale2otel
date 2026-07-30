package flowlog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

type rejectingStore struct {
	result flowstore.Admission
}

func (s *rejectingStore) Record(flowstore.Observation) {}

func (s *rejectingStore) RecordResult(flowstore.Observation) flowstore.Admission {
	return s.result
}

func TestProcess_StoreAdmissionDropIsObservableWithoutAffectingOTLP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result flowstore.Admission
		reason string
	}{
		{name: "expired", result: flowstore.AdmissionExpired, reason: "expired"},
		{name: "future", result: flowstore.AdmissionFuture, reason: "future"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
				Store: &rejectingStore{result: tc.result},
			})

			p.Process(decodeLiveRecord(t), rec.Emitter())

			if len(rec.MetricPoints(flowlog.MetricIO)) == 0 {
				t.Fatal("OTLP flow metrics were suppressed by a local-store rejection")
			}
			points := rec.MetricPoints(flowlog.MetricStoreDropped)
			if len(points) == 0 {
				t.Fatal("no local-store drop metric emitted")
			}
			for _, point := range points {
				if got := point.Attrs["reason"]; got != tc.reason {
					t.Errorf("reason = %q, want %q", got, tc.reason)
				}
				if len(point.Attrs) != 1 {
					t.Errorf("attrs = %#v, want reason only", point.Attrs)
				}
			}
		})
	}
}
