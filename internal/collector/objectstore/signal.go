package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

var (
	// ErrRecordDecode marks one malformed record. The engine skips only that
	// record and can still complete the containing object.
	ErrRecordDecode = errors.New("objectstore record decode failed")
	// ErrRecordInvalid marks one decoded record which failed source validation.
	// The engine skips only that record and can still complete the object.
	ErrRecordInvalid = errors.New("objectstore record validation failed")
)

// RecordTimestamps carries the accepted record's event and optional capture
// timestamps without coupling the engine to a signal-specific record type.
type RecordTimestamps struct {
	EventTime   time.Time
	CaptureTime time.Time
}

// SignalProcessor decodes, validates, and hands one source record to the
// signal's existing processor. Implementations must wrap ErrRecordDecode or
// ErrRecordInvalid for row-local failures; any other error fails the object.
type SignalProcessor interface {
	Signal() string
	ProcessRecord(context.Context, []byte, time.Time, telemetry.Emitter) (RecordTimestamps, error)
}

type flowSignal struct {
	proc *flowlog.Processor
}

// NewFlowSignal adapts the existing shared flow-log processor to the
// provider-neutral object-store engine.
func NewFlowSignal(proc *flowlog.Processor) SignalProcessor {
	return &flowSignal{proc: proc}
}

func (*flowSignal) Signal() string { return semconv.IngestSignalFlow }

func (s *flowSignal) ProcessRecord(
	_ context.Context,
	line []byte,
	now time.Time,
	e telemetry.Emitter,
) (RecordTimestamps, error) {
	var record flowlog.FlowLog
	if err := json.Unmarshal(line, &record); err != nil {
		return RecordTimestamps{}, fmt.Errorf("%w: %w", ErrRecordDecode, err)
	}
	violations := flowlog.Validate(record, flowlog.ValidationOptions{Now: func() time.Time { return now }})
	if len(violations) != 0 {
		flowlog.ObserveDataQuality(e, semconv.IngestSourceObjectStore, violations)
		return RecordTimestamps{}, fmt.Errorf("%w: %d violation(s)", ErrRecordInvalid, len(violations))
	}
	s.proc.Process(record, e)
	return RecordTimestamps{
		EventTime:   flowlog.EventTimestamp(record),
		CaptureTime: flowlog.CaptureTimestamp(record),
	}, nil
}
