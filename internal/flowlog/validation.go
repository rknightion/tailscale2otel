package flowlog

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

const defaultMaxFutureSkew = 5 * time.Minute

// MetricDataQuality counts semantically invalid flow records by a closed
// ingestion source and violation reason.
const MetricDataQuality = "tailscale.network.data_quality"

// ViolationKind classifies a flow-log validation failure. Its values are
// closed and bounded, so callers can use them safely as telemetry attributes.
type ViolationKind string

const (
	ViolationNegativeCounters ViolationKind = "negative_counters"
	ViolationInvertedWindow   ViolationKind = "inverted_window"
	ViolationInvalidEndpoint  ViolationKind = "invalid_endpoint"
	ViolationFutureTimestamp  ViolationKind = "future_timestamp"
	ViolationInvalidProtocol  ViolationKind = "invalid_protocol"
)

// Violation is a safe, bounded classification. It deliberately carries no
// source record values, endpoints, identifiers, or free-form message.
type Violation struct {
	Kind ViolationKind
}

// ValidationOptions controls timestamp validation. A nil Now and a zero
// MaxFutureSkew select time.Now and five minutes respectively.
type ValidationOptions struct {
	Now           func() time.Time
	MaxFutureSkew time.Duration
}

// Validate reports the bounded, deterministic set of structural violations in
// a flow-log record. It allows omitted optional fields and unknown-but-in-range
// IANA protocol numbers so new upstream record variants remain processable.
func Validate(log FlowLog, opts ValidationOptions) []Violation {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	maxFutureSkew := opts.MaxFutureSkew
	if maxFutureSkew == 0 {
		maxFutureSkew = defaultMaxFutureSkew
	}

	seen := make(map[ViolationKind]bool, 5)
	for _, connection := range allConnections(log) {
		if connection.TxPkts < 0 || connection.TxBytes < 0 || connection.RxPkts < 0 || connection.RxBytes < 0 {
			seen[ViolationNegativeCounters] = true
		}
		if (connection.Src != "" && !validEndpoint(connection.Src)) ||
			(connection.Dst != "" && !validEndpoint(connection.Dst)) {
			seen[ViolationInvalidEndpoint] = true
		}
		if connection.Proto < 0 || connection.Proto > 255 {
			seen[ViolationInvalidProtocol] = true
		}
	}

	if !log.Start.IsZero() && !log.End.IsZero() && log.Start.After(log.End) {
		seen[ViolationInvertedWindow] = true
	}
	limit := now().Add(maxFutureSkew)
	if timestampAfter(log.Start, limit) || timestampAfter(log.End, limit) || timestampAfter(log.Logged, limit) {
		seen[ViolationFutureTimestamp] = true
	}

	order := []ViolationKind{
		ViolationNegativeCounters,
		ViolationInvertedWindow,
		ViolationInvalidEndpoint,
		ViolationFutureTimestamp,
		ViolationInvalidProtocol,
	}
	violations := make([]Violation, 0, len(seen))
	for _, kind := range order {
		if seen[kind] {
			violations = append(violations, Violation{Kind: kind})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return violations
}

// ObserveDataQuality emits one bounded point per violation classification.
// Both dimensions fail closed to "other" so wire data can never create series.
func ObserveDataQuality(e telemetry.Emitter, source string, violations []Violation) {
	source = boundedValidationSource(source)
	for _, violation := range violations {
		e.Counter(docDataQuality.Name, docDataQuality.Unit, docDataQuality.Description, 1, telemetry.Attrs{
			"source": source,
			"reason": boundedViolationKind(violation.Kind),
		})
	}
}

func boundedValidationSource(source string) string {
	switch source {
	case "poll", "stream", "objectstore":
		return source
	default:
		return "other"
	}
}

func boundedViolationKind(kind ViolationKind) string {
	switch kind {
	case ViolationNegativeCounters,
		ViolationInvertedWindow,
		ViolationInvalidEndpoint,
		ViolationFutureTimestamp,
		ViolationInvalidProtocol:
		return string(kind)
	default:
		return "other"
	}
}

func allConnections(log FlowLog) []ConnectionCounts {
	connections := make([]ConnectionCounts, 0,
		len(log.VirtualTraffic)+len(log.SubnetTraffic)+len(log.ExitTraffic)+len(log.PhysicalTraffic))
	connections = append(connections, log.VirtualTraffic...)
	connections = append(connections, log.SubnetTraffic...)
	connections = append(connections, log.ExitTraffic...)
	connections = append(connections, log.PhysicalTraffic...)
	return connections
}

func timestampAfter(timestamp, limit time.Time) bool {
	return !timestamp.IsZero() && timestamp.After(limit)
}

func validEndpoint(endpoint string) bool {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || !validHost(host) || !validPort(port) {
		return false
	}
	return true
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	return err == nil && parsed <= 65535
}

func validHost(host string) bool {
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if strings.Contains(host, ":") {
		return false
	}
	if len(host) > 253 {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !validHostnameLabel(label) {
			return false
		}
	}
	return true
}

func validHostnameLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
