package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

func TestStats_HealthRecoversAfterWriterSuccess(t *testing.T) {
	s := openTestStore(t, testOpts(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.checkpoint(ctx) == nil {
		t.Fatal("canceled checkpoint succeeded")
	}
	if s.Stats().Backend.Healthy {
		t.Fatal("failed checkpoint not reported")
	}
	if err := s.flush([]flowstore.Observation{obs(time.Now())}); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Backend; !got.Healthy || got.Error != "" {
		t.Fatalf("committed flush did not recover: %+v", got)
	}
}

func TestFailRead_OwnDeadlineDoesNotMarkBackendFailed(t *testing.T) {
	s := openTestStore(t, testOpts(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.queryTotals(ctx, window{})
	if !s.Stats().Backend.Healthy {
		t.Fatal("canceled read marked backend failed")
	}
	if _, err := s.db.Exec("DROP TABLE flows"); err != nil {
		t.Fatal(err)
	}
	s.queryTotals(context.Background(), window{})
	if s.Stats().Backend.Healthy {
		t.Fatal("live-context read failure not reported")
	}
}

func TestFlushFailure_CountsLostRows(t *testing.T) {
	s := openTestStore(t, testOpts(t))
	if _, err := s.db.Exec("DROP TABLE flows"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.recorded = 3
	s.mu.Unlock()
	s.flushAndAccount([]flowstore.Observation{obs(time.Now()), obs(time.Now()), obs(time.Now())})
	got := s.Stats()
	if got.Observations != 0 || got.Truncated != 3 || got.Backend.QueueDrops != 3 {
		t.Fatalf("loss stats = %+v", got)
	}
}

func TestSetupFailureSurvivesWriterRecovery(t *testing.T) {
	s := openTestStore(t, testOpts(t))
	s.setupFailure = errors.New("setup conversion failed")
	if err := s.flush([]flowstore.Observation{obs(time.Now())}); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats().Backend; got.Healthy || got.Error != "setup conversion failed" {
		t.Fatalf("setup failure lost: %+v", got)
	}
}
