package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type flowStoreObsStub struct{ backend flowstore.Backend }

func (s *flowStoreObsStub) Record(flowstore.Observation) {}
func (s *flowStoreObsStub) RecordResult(flowstore.Observation) flowstore.Admission {
	return flowstore.AdmissionAccepted
}
func (s *flowStoreObsStub) Query(flowstore.Query) flowstore.Result { return flowstore.Result{} }
func (s *flowStoreObsStub) RecentPage(flowstore.RecentQuery) flowstore.RecentPage {
	return flowstore.RecentPage{}
}
func (s *flowStoreObsStub) Stats() flowstore.Stats   { return flowstore.Stats{Backend: s.backend} }
func (s *flowStoreObsStub) Limits() flowstore.Limits { return flowstore.Limits{} }
func (s *flowStoreObsStub) Close() error             { return nil }

func TestEmitFlowStoreObservability(t *testing.T) {
	rec := telemetrytest.New()
	checkpoint := time.Unix(1_700_000_000, 500_000_000).UTC()
	rt := &tailnetRuntime{
		emitter: rec.Emitter(),
		flowStore: &flowStoreObsStub{backend: flowstore.Backend{
			Kind: flowstore.BackendSQLite, JournalSizeBytes: 8192, LastCheckpointAt: checkpoint,
		}},
	}

	emitFlowStoreObservability(rt)
	journal := rec.MetricPoints(appcatalog.MetricFlowStoreJournalSize)
	if len(journal) != 1 || journal[0].Value != 8192 {
		t.Fatalf("journal points = %+v, want one 8192-byte gauge", journal)
	}
	checkpoints := rec.MetricPoints(appcatalog.MetricFlowStoreLastCheckpointTimestamp)
	if len(checkpoints) != 1 || checkpoints[0].Value != 1_700_000_000.5 {
		t.Fatalf("checkpoint points = %+v, want one Unix timestamp gauge", checkpoints)
	}
}

func TestEmitFlowStoreObservabilitySkipsMemoryBackend(t *testing.T) {
	rec := telemetrytest.New()
	rt := &tailnetRuntime{emitter: rec.Emitter(), flowStore: &flowStoreObsStub{backend: flowstore.Backend{Kind: flowstore.BackendMemory}}}
	emitFlowStoreObservability(rt)
	if got := rec.MetricPoints(appcatalog.MetricFlowStoreJournalSize); len(got) != 0 {
		t.Fatalf("memory backend journal points = %+v, want none", got)
	}
}
