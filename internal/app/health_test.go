package app

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
)

func TestSuccessRatePct(t *testing.T) {
	cases := []struct {
		runs, failures int64
		want           float64
	}{
		{1, 0, 100},
		{4, 1, 75},
		{2, 2, 0},
		{0, 0, 0}, // no runs yet: undefined, reported as 0
	}
	for _, c := range cases {
		if got := successRatePct(c.runs, c.failures); got != c.want {
			t.Errorf("successRatePct(%d,%d) = %v, want %v", c.runs, c.failures, got, c.want)
		}
	}
}

func TestNextRunIn(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := time.Minute
	if got := nextRunIn(t0, interval, t0.Add(10*time.Second)); got != 50*time.Second {
		t.Errorf("nextRunIn 10s in = %v, want 50s", got)
	}
	if got := nextRunIn(t0, interval, t0.Add(90*time.Second)); got != 0 {
		t.Errorf("nextRunIn overdue = %v, want 0 (clamped)", got)
	}
}

func TestIsOverdue(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := time.Minute
	if isOverdue(t0, interval, t0.Add(90*time.Second)) {
		t.Errorf("90s < 2 intervals should not be overdue")
	}
	if !isOverdue(t0, interval, t0.Add(130*time.Second)) {
		t.Errorf("130s > 2 intervals should be overdue")
	}
}

func TestDeriveHealth(t *testing.T) {
	ran := func(name string, ok bool, consec int64) statusdata.CollectorStatus {
		return statusdata.CollectorStatus{Name: name, HasRun: true, LastSuccess: ok, ConsecutiveFailures: consec}
	}

	t.Run("all ran ok -> healthy", func(t *testing.T) {
		state, reasons := deriveHealth([]statusdata.CollectorStatus{ran("a", true, 0), ran("b", true, 0)})
		if state != healthHealthy || len(reasons) != 0 {
			t.Fatalf("got %q %v, want healthy/none", state, reasons)
		}
	})

	t.Run("no collectors -> healthy", func(t *testing.T) {
		if state, _ := deriveHealth(nil); state != healthHealthy {
			t.Fatalf("got %q, want healthy", state)
		}
	})

	t.Run("unrun collector -> starting", func(t *testing.T) {
		state, reasons := deriveHealth([]statusdata.CollectorStatus{
			ran("a", true, 0),
			{Name: "devices", HasRun: false},
		})
		if state != healthStarting {
			t.Fatalf("got %q, want starting", state)
		}
		if len(reasons) != 1 || !strings.Contains(reasons[0], "devices") {
			t.Fatalf("reasons = %v, want one mentioning devices", reasons)
		}
	})

	t.Run("3 consecutive failures -> degraded", func(t *testing.T) {
		state, reasons := deriveHealth([]statusdata.CollectorStatus{ran("flowlogs", false, 3)})
		if state != healthDegraded {
			t.Fatalf("got %q, want degraded", state)
		}
		if len(reasons) != 1 || !strings.Contains(reasons[0], "consecutive") {
			t.Fatalf("reasons = %v, want one about consecutive failures", reasons)
		}
	})

	t.Run("single last-run failure -> degraded", func(t *testing.T) {
		state, reasons := deriveHealth([]statusdata.CollectorStatus{ran("x", false, 1)})
		if state != healthDegraded || len(reasons) != 1 || !strings.Contains(reasons[0], "last run failed") {
			t.Fatalf("got %q %v, want degraded/last-run-failed", state, reasons)
		}
	})

	t.Run("stuck checkpoint -> degraded", func(t *testing.T) {
		c := ran("auditlogs", true, 0)
		c.Checkpoint = &statusdata.CheckpointStatus{Stuck: true, Lag: "26m40s"}
		state, reasons := deriveHealth([]statusdata.CollectorStatus{c})
		if state != healthDegraded || len(reasons) != 1 || !strings.Contains(reasons[0], "stuck") {
			t.Fatalf("got %q %v, want degraded/stuck", state, reasons)
		}
	})

	t.Run("overdue -> degraded", func(t *testing.T) {
		c := ran("users", true, 0)
		c.Overdue = true
		state, reasons := deriveHealth([]statusdata.CollectorStatus{c})
		if state != healthDegraded || len(reasons) != 1 || !strings.Contains(reasons[0], "overdue") {
			t.Fatalf("got %q %v, want degraded/overdue", state, reasons)
		}
	})

	t.Run("degraded takes precedence over starting", func(t *testing.T) {
		state, _ := deriveHealth([]statusdata.CollectorStatus{
			ran("a", false, 5),
			{Name: "b", HasRun: false},
		})
		if state != healthDegraded {
			t.Fatalf("got %q, want degraded (a failing outweighs b starting)", state)
		}
	})
}
