package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/k8saudit"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
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

// PreparedRecord is one decoded record whose row-local outcome can be committed
// after every row in the containing object has prepared successfully. Commit is
// infallible and is the first point at which a signal adapter may touch shared
// processors, emitters, caches, de-duplication, stores, rDNS, rollups, or
// freshness observers.
type PreparedRecord interface {
	Commit(telemetry.Emitter) RecordTimestamps
}

// SignalProcessor decodes and validates one source record without observable
// side effects. Implementations return:
//   - a non-nil prepared record and nil for an accepted row;
//   - nil and an error wrapping ErrRecordDecode for malformed source data;
//   - a non-nil deferred-observation record and an error wrapping
//     ErrRecordInvalid for semantically invalid source data.
//
// Any other error fails the object, and the engine ignores any prepared record
// returned with it.
type SignalProcessor interface {
	Signal() string
	PrepareRecord(context.Context, []byte, time.Time) (PreparedRecord, error)
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

func (s *flowSignal) PrepareRecord(
	_ context.Context,
	line []byte,
	now time.Time,
) (PreparedRecord, error) {
	var record flowlog.FlowLog
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecordDecode, err)
	}
	violations := flowlog.Validate(record, flowlog.ValidationOptions{Now: func() time.Time { return now }})
	if len(violations) != 0 {
		return invalidFlowRecord{violations: violations},
			fmt.Errorf("%w: %d violation(s)", ErrRecordInvalid, len(violations))
	}
	return acceptedFlowRecord{proc: s.proc, record: record}, nil
}

type acceptedFlowRecord struct {
	proc   *flowlog.Processor
	record flowlog.FlowLog
}

func (r acceptedFlowRecord) Commit(e telemetry.Emitter) RecordTimestamps {
	r.proc.Process(r.record, e)
	return RecordTimestamps{
		EventTime:   flowlog.EventTimestamp(r.record),
		CaptureTime: flowlog.CaptureTimestamp(r.record),
	}
}

type auditSignal struct {
	proc *audit.Processor
}

// NewAuditSignal adapts the existing shared configuration-audit processor to the
// provider-neutral object-store engine, so an export object reaches exactly the
// emission path the poller, the log-stream receiver and the webhook receiver use
// (#288).
func NewAuditSignal(proc *audit.Processor) SignalProcessor {
	return &auditSignal{proc: proc}
}

func (*auditSignal) Signal() string { return semconv.IngestSignalAudit }

func (s *auditSignal) PrepareRecord(
	_ context.Context,
	line []byte,
	_ time.Time,
) (PreparedRecord, error) {
	var event audit.Event
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecordDecode, err)
	}
	// "null", "{}" and "[]"-shaped padding all unmarshal into a zero Event
	// WITHOUT an error, and a zero Event emits a log record with no action, no
	// target and the zero timestamp. Requiring one load-bearing field keeps such
	// a line from minting a phantom audit event; the object-level guard (#497)
	// then fails an object in which every row is like this.
	if event.Action == "" && event.EventTime.IsZero() && event.Logged.IsZero() {
		return nil, fmt.Errorf("%w: record carries no action and no timestamp", ErrRecordDecode)
	}
	return acceptedAuditRecord{proc: s.proc, event: event}, nil
}

type acceptedAuditRecord struct {
	proc  *audit.Processor
	event audit.Event
}

func (r acceptedAuditRecord) Commit(e telemetry.Emitter) RecordTimestamps {
	r.proc.Process(r.event, e)
	return RecordTimestamps{
		EventTime:   audit.EventTimestamp(r.event),
		CaptureTime: audit.CaptureTimestamp(r.event),
	}
}

type invalidFlowRecord struct {
	violations []flowlog.Violation
}

func (r invalidFlowRecord) Commit(e telemetry.Emitter) RecordTimestamps {
	flowlog.ObserveDataQuality(e, semconv.IngestSourceObjectStore, r.violations)
	return RecordTimestamps{}
}

type k8sAuditSignal struct {
	proc *k8saudit.Processor
}

// NewK8sAuditSignal adapts the shared Kubernetes-audit processor to the
// object-store engine.
//
// Unlike the flow and audit signals, this one is handed two DIFFERENT record
// shapes on the same feed. tsrecorder writes .event objects (one API request
// each) and .cast objects (one terminal session each) into the same bucket, and
// <stableID> varies per recorder replica so no prefix separates them. Crucially,
// PrepareRecord receives only the record BYTES — never the object key — so the
// two cannot be told apart by filename and are discriminated by content instead.
func NewK8sAuditSignal(proc *k8saudit.Processor) SignalProcessor {
	return &k8sAuditSignal{proc: proc}
}

func (*k8sAuditSignal) Signal() string { return semconv.IngestSignalK8sAudit }

func (s *k8sAuditSignal) PrepareRecord(
	_ context.Context,
	line []byte,
	_ time.Time,
) (PreparedRecord, error) {
	// An asciinema frame is accepted and does nothing. Returning ErrRecordDecode
	// here instead would turn every line of terminal output in a long session
	// into a decode-failure observation, burying real corruption under thousands
	// of expected ones — and the object-level guard would fail an otherwise
	// healthy recording whose header is its only non-frame line.
	if k8saudit.IsCastFrame(line) {
		return castFrameRecord{}, nil
	}
	if obj, err := k8saudit.DecodeObject(line); err == nil {
		return acceptedK8sAuditRecord{proc: s.proc, obj: obj}, nil
	}
	if hdr, err := k8saudit.DecodeCastHeader(line); err == nil {
		return acceptedK8sSessionRecord{proc: s.proc, hdr: hdr}, nil
	}
	return nil, fmt.Errorf("%w: neither an audit event nor a cast header", ErrRecordDecode)
}

type acceptedK8sAuditRecord struct {
	proc *k8saudit.Processor
	obj  k8saudit.Object
}

func (r acceptedK8sAuditRecord) Commit(e telemetry.Emitter) RecordTimestamps {
	r.proc.Process(r.obj, e)
	return RecordTimestamps{
		EventTime:   k8saudit.EventTimestamp(r.obj),
		CaptureTime: k8saudit.CaptureTimestamp(r.obj),
	}
}

type acceptedK8sSessionRecord struct {
	proc *k8saudit.Processor
	hdr  k8saudit.CastHeader
}

func (r acceptedK8sSessionRecord) Commit(e telemetry.Emitter) RecordTimestamps {
	r.proc.ProcessSession(r.hdr, e)
	if r.hdr.Timestamp == 0 {
		return RecordTimestamps{}
	}
	at := time.Unix(r.hdr.Timestamp, 0).UTC()
	return RecordTimestamps{EventTime: at, CaptureTime: at}
}

// castFrameRecord is one line of terminal output: recognized, deliberately
// ignored, never inspected for meaning and never emitted. It contributes no
// timestamps, so it cannot drag the freshness observers backwards either.
type castFrameRecord struct{}

func (castFrameRecord) Commit(telemetry.Emitter) RecordTimestamps {
	return RecordTimestamps{}
}
