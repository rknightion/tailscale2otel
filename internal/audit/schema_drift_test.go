package audit_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/audit"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func TestProcessEmitsBoundedSchemaDriftObservations(t *testing.T) {
	var logs bytes.Buffer
	processor := audit.NewProcessor(audit.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	recorder := telemetrytest.New()

	event := sampleEvent()
	event.Action = "FUTURE_ACTION"
	event.Origin = "FUTURE_ORIGIN"
	event.Actor.Type = "FUTURE_ACTOR"
	event.Target.Property = "FUTURE_PROPERTY"
	processor.Process(event, recorder.Emitter())

	points := recorder.MetricPoints(audit.MetricAuditSchemaDrift)
	if len(points) != 4 {
		t.Fatalf("schema-drift points = %d, want 4", len(points))
	}
	for _, field := range []string{"action", "origin", "actor_type", "target_property"} {
		if !hasSchemaDriftPoint(points, field, "unknown") {
			t.Errorf("missing unknown schema-drift point for field %q: %#v", field, points)
		}
	}
	for _, point := range points {
		if point.Unit != "{observation}" || point.Kind != "sum" || !point.Monotonic || point.Value != 1 {
			t.Errorf("point = %#v, want monotonic {observation} counter increment", point)
		}
		if len(point.Attrs) != 2 {
			t.Errorf("point attrs = %#v, want only field and status", point.Attrs)
		}
	}

	output := logs.String()
	for _, raw := range []string{"FUTURE_ACTION", "FUTURE_ORIGIN", "FUTURE_ACTOR", "FUTURE_PROPERTY"} {
		if strings.Contains(output, raw) {
			t.Errorf("schema-drift warning leaked raw value %q: %s", raw, output)
		}
	}
	for _, field := range []string{"action", "origin", "actor_type", "target_property"} {
		if !strings.Contains(output, "field="+field) || !strings.Contains(output, "digest=") {
			t.Errorf("schema-drift warning missing bounded diagnostic for %q: %s", field, output)
		}
	}
	sum := sha256.Sum256([]byte("FUTURE_ACTION"))
	if want := hex.EncodeToString(sum[:])[:12]; !strings.Contains(output, "digest="+want) {
		t.Errorf("schema-drift warning missing 12-character SHA-256 prefix %q: %s", want, output)
	}
}

func TestProcessSchemaDriftObservesKnownValuesAndWarnsOncePerUnknown(t *testing.T) {
	var logs bytes.Buffer
	processor := audit.NewProcessor(audit.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	recorder := telemetrytest.New()

	known := sampleEvent()
	processor.Process(known, recorder.Emitter())
	unknown := sampleEvent()
	unknown.EventGroupID = "unknown"
	unknown.Action = "FUTURE_ACTION"
	processor.Process(unknown, recorder.Emitter())
	unknown.EventGroupID = "unknown-again"
	processor.Process(unknown, recorder.Emitter())

	points := recorder.MetricPoints(audit.MetricAuditSchemaDrift)
	for _, field := range []string{"action", "origin", "actor_type", "target_property"} {
		if !hasSchemaDriftPoint(points, field, "known") {
			t.Errorf("missing known schema-drift point for %q", field)
		}
	}
	if got := sumSchemaDriftPoints(points, "action", "unknown"); got != 2 {
		t.Errorf("unknown action observations = %v, want 2", got)
	}
	if got := strings.Count(logs.String(), "field=action"); got != 1 {
		t.Errorf("unknown action warnings = %d, want 1: %s", got, logs.String())
	}
}

func TestProcessSchemaDriftWarningCap(t *testing.T) {
	var logs bytes.Buffer
	processor := audit.NewProcessor(audit.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	recorder := telemetrytest.New()

	for i := range 129 {
		event := sampleEvent()
		event.EventGroupID = "unique-" + strconv.Itoa(i)
		event.Action = "FUTURE_ACTION_" + strconv.Itoa(i)
		processor.Process(event, recorder.Emitter())
	}
	if got := strings.Count(logs.String(), "msg="); got != 128 {
		t.Errorf("schema-drift warnings = %d, want hard cap 128", got)
	}
}

func hasSchemaDriftPoint(points []telemetrytest.MetricPoint, field, status string) bool {
	return countSchemaDriftPoints(points, field, status) > 0
}

func countSchemaDriftPoints(points []telemetrytest.MetricPoint, field, status string) int {
	count := 0
	for _, point := range points {
		if point.Attrs["field"] == field && point.Attrs["status"] == status {
			count++
		}
	}
	return count
}

func sumSchemaDriftPoints(points []telemetrytest.MetricPoint, field, status string) float64 {
	var sum float64
	for _, point := range points {
		if point.Attrs["field"] == field && point.Attrs["status"] == status {
			sum += point.Value
		}
	}
	return sum
}
