package collector_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

func TestEmitCheckpointStats_ExistingFile(t *testing.T) {
	content := []byte(`{"checkpoint":"data","key":"value"}`)
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := telemetrytest.New()
	collector.EmitCheckpointStats(rec.Emitter(), path)

	// disk.size must equal len(content)
	sizePoints := rec.MetricPoints(collector.MetricCheckpointDiskSize)
	if len(sizePoints) != 1 {
		t.Fatalf("checkpoint.disk.size: got %d points, want 1", len(sizePoints))
	}
	p := sizePoints[0]
	if p.Kind != "gauge" {
		t.Fatalf("checkpoint.disk.size kind = %q, want gauge", p.Kind)
	}
	if p.Unit != semconv.UnitBytes {
		t.Fatalf("checkpoint.disk.size unit = %q, want %q", p.Unit, semconv.UnitBytes)
	}
	if want := float64(len(content)); p.Value != want {
		t.Fatalf("checkpoint.disk.size value = %v, want %v", p.Value, want)
	}
	if len(p.Attrs) != 0 {
		t.Fatalf("checkpoint.disk.size attrs = %v, want none", p.Attrs)
	}

	// persist.age must be non-negative (mtime ≤ now)
	agePoints := rec.MetricPoints(collector.MetricCheckpointPersistAge)
	if len(agePoints) != 1 {
		t.Fatalf("checkpoint.persist.age: got %d points, want 1", len(agePoints))
	}
	a := agePoints[0]
	if a.Kind != "gauge" {
		t.Fatalf("checkpoint.persist.age kind = %q, want gauge", a.Kind)
	}
	if a.Unit != semconv.UnitSeconds {
		t.Fatalf("checkpoint.persist.age unit = %q, want %q", a.Unit, semconv.UnitSeconds)
	}
	if a.Value < 0 {
		t.Fatalf("checkpoint.persist.age value = %v, want >= 0", a.Value)
	}
	if len(a.Attrs) != 0 {
		t.Fatalf("checkpoint.persist.age attrs = %v, want none", a.Attrs)
	}
}

func TestEmitCheckpointStats_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	rec := telemetrytest.New()
	collector.EmitCheckpointStats(rec.Emitter(), path)

	// No metrics should be emitted when the file does not exist.
	if pts := rec.MetricPoints(collector.MetricCheckpointDiskSize); len(pts) != 0 {
		t.Fatalf("checkpoint.disk.size: got %d points for missing file, want 0", len(pts))
	}
	if pts := rec.MetricPoints(collector.MetricCheckpointPersistAge); len(pts) != 0 {
		t.Fatalf("checkpoint.persist.age: got %d points for missing file, want 0", len(pts))
	}
}
