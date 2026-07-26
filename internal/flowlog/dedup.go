package flowlog

import (
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/dedup"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// DuplicateResult describes whether a connection was new, an exact retry, or
// a retry whose counters conflict with the first observation.
type DuplicateResult uint8

const (
	DuplicateNew DuplicateResult = iota
	DuplicateExact
	DuplicateConflict
)

// Deduplication scopes identify the boundary at which a duplicate was found.
const (
	DedupScopePollBoundary = "poll_boundary"
	DedupScopeCrossSource  = "cross_source"
)

// ConnectionKey returns the bounded identity of one connection. A connection
// is scoped by the reporting node, UTC window, traffic class, protocol and
// endpoints, so identical tuples in separate traffic classes remain distinct.
func ConnectionKey(fl FlowLog, trafficType string, cc ConnectionCounts) string {
	var b strings.Builder
	b.WriteString(fl.NodeID)
	b.WriteByte('|')
	b.WriteString(fl.Start.UTC().Format(time.RFC3339Nano))
	b.WriteByte('|')
	b.WriteString(fl.End.UTC().Format(time.RFC3339Nano))
	b.WriteByte('|')
	b.WriteString(boundedTrafficType(trafficType))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(cc.Proto))
	b.WriteByte('|')
	b.WriteString(cc.Src)
	b.WriteByte('|')
	b.WriteString(cc.Dst)
	return b.String()
}

// CompareConnection records a connection's first counter values and reports
// whether a later observation is exact or conflicts with them.
func CompareConnection(seen *dedup.Set, fl FlowLog, trafficType string, cc ConnectionCounts) DuplicateResult {
	switch seen.CompareAndAdd(ConnectionKey(fl, trafficType, cc), connectionFingerprint(cc)) {
	case dedup.ResultNew:
		return DuplicateNew
	case dedup.ResultExact:
		return DuplicateExact
	default:
		return DuplicateConflict
	}
}

// ObserveDedupConflict records one bounded conflict point for a duplicate whose
// counter values differ from the first observation.
func ObserveDedupConflict(e telemetry.Emitter, scope, trafficType string) {
	e.Counter(docDedupConflicts.Name, docDedupConflicts.Unit, docDedupConflicts.Description,
		1, telemetry.Attrs{
			"scope":                 scope,
			semconv.AttrTrafficType: boundedTrafficType(trafficType),
		})
}

func boundedTrafficType(trafficType string) string {
	switch trafficType {
	case "virtual", "subnet", "exit", "physical":
		return trafficType
	default:
		return "unknown"
	}
}

func connectionFingerprint(cc ConnectionCounts) string {
	return strconv.FormatInt(cc.TxBytes, 10) + "|" +
		strconv.FormatInt(cc.RxBytes, 10) + "|" +
		strconv.FormatInt(cc.TxPkts, 10) + "|" +
		strconv.FormatInt(cc.RxPkts, 10)
}
