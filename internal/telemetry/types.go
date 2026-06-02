// Package telemetry is the OTEL-agnostic facade that collectors use to record
// metrics and emit log events. The concrete implementation wraps the
// OpenTelemetry Go SDK; collectors depend only on the Emitter interface.
package telemetry

import "time"

// Attrs is a set of attributes attached to a metric data point or log record.
// Supported value types: string, bool, int, int64, float64, []string.
type Attrs map[string]any

// Severity is the log severity level for an Event.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityError
)

// String returns the canonical severity text (INFO/WARN/ERROR).
func (s Severity) String() string {
	switch s {
	case SeverityWarn:
		return "WARN"
	case SeverityError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Event is a single log record to emit.
type Event struct {
	Name      string // OTEL LogRecord EventName, e.g. "tailscale.network.flow"
	Body      string // human-readable summary
	Severity  Severity
	Timestamp time.Time // event time; zero means "now"
	Attrs     Attrs
}

// Emitter records metrics and log events. Implementations must be safe for
// concurrent use. Instruments are created lazily and cached by name.
type Emitter interface {
	// Counter adds to a monotonic Float64 counter (e.g. bytes, packets).
	Counter(name, unit, desc string, add float64, attrs Attrs)
	// Gauge records the current value of a synchronous Float64 gauge.
	Gauge(name, unit, desc string, value float64, attrs Attrs)
	// UpDownCounter adds (or subtracts) to a non-monotonic counter.
	UpDownCounter(name, unit, desc string, value float64, attrs Attrs)
	// LogEvent emits a single OTEL log record.
	LogEvent(ev Event)
}
