// Package flowlogs implements the "flowlogs" polling collector: the POLL path
// for Tailscale network flow logs. On each tick the scheduler hands it a
// [from, to] window; the collector fetches that window via the Tailscale API and
// delegates record-to-OTEL conversion to the shared flowlog.Processor (the same
// processor used by the streaming receiver).
//
// Because the API window is inclusive of both ends, a connection straddling a
// window edge can be returned in two adjacent ticks. The collector keeps a
// bounded de-duplication set keyed by connection identity and drops repeats
// before handing the response to the processor, so a boundary connection's
// metrics are emitted exactly once. The set is bounded (FIFO eviction), so its
// memory stays small even under a long stream of unique connections.
package flowlogs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

const (
	// defaultInterval is the poll cadence used when none is configured.
	defaultInterval = 60 * time.Second
	// defaultLag is the trailing safety margin used when none is configured.
	// Flow logs can arrive late, so the scheduler queries up to now-Lag.
	defaultLag = 120 * time.Second
	// dedupCapacity bounds how many recently-seen connections are remembered for
	// boundary de-duplication. A window holds at most a couple of ticks' worth of
	// connections, so a few thousand keys covers the overlap with margin.
	dedupCapacity = 16384
)

// metricFeatureEnabled is the gauge reporting whether the network-flow-logging
// feature is enabled (1) or disabled (0) for the tailnet.
const metricFeatureEnabled = "tailscale.feature.enabled"

// featureName is the value of the tailscale.feature attribute on the
// feature.enabled gauge this collector emits.
const featureName = "network_flow_logging"

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	NetworkFlowLogs(ctx context.Context, start, end time.Time) (flowlog.NetworkResponse, error)
}

// FeatureCheck reports whether the network-flow-logging feature is currently
// enabled for the tailnet. A nil FeatureCheck means "always enabled". An error
// is treated as fail-open (proceed as enabled) by the collector.
type FeatureCheck func(ctx context.Context) (bool, error)

// Collector implements collector.WindowCollector for Tailscale network flow
// logs, fetching each window and delegating conversion to a shared
// flowlog.Processor.
type Collector struct {
	api          api
	proc         *flowlog.Processor
	interval     time.Duration
	lag          time.Duration
	seen         *dedup.Set
	featureCheck FeatureCheck
	// onIngest, when non-nil, is called once per successful poll window with
	// ("poll","flow", <records accepted>, 0). The app supplies it (gated on
	// self-observability); the collector stays agnostic to how it's emitted.
	onIngest func(source, signal string, records, bytes int)
}

// New returns a flowlogs Collector that fetches windows via a, converts them
// with proc, and uses interval/lag as its poll cadence and trailing safety
// margin (non-positive values fall back to 60s and 120s respectively).
//
// featureCheck, when non-nil, gates collection: if it reports the feature
// disabled the collector stays idle and emits tailscale.feature.enabled=0
// rather than fetching. A nil featureCheck preserves the always-enabled
// behavior. featureCheck errors fail open (the collector proceeds as enabled).
func New(a api, proc *flowlog.Processor, interval, lag time.Duration, featureCheck FeatureCheck, onIngest func(source, signal string, records, bytes int)) *Collector {
	return &Collector{
		api:          a,
		proc:         proc,
		interval:     interval,
		lag:          lag,
		seen:         dedup.New(dedupCapacity),
		featureCheck: featureCheck,
		onIngest:     onIngest,
	}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "flowlogs" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Lag returns the configured trailing safety margin, or 120s when unset.
func (c *Collector) Lag() time.Duration {
	if c.lag > 0 {
		return c.lag
	}
	return defaultLag
}

// CollectWindow fetches flow logs for [from, to] and processes them.
//
// When a featureCheck is configured it runs first: a disabled feature emits
// tailscale.feature.enabled=0 and returns to with no error (idle, not a
// failure), while an enabled feature emits =1 and proceeds. A featureCheck
// error fails open (proceed without emitting the gauge).
//
// A fetch error carrying a genuine HTTP 403 (a *tsapi.StatusError with
// Code == 403, see isForbidden) is also treated as the feature being
// disabled: it emits =0 and returns to with no error so the scheduler
// advances instead of retrying. Any other fetch error — including one whose
// text merely contains "403" or "forbidden" — is ambiguous and returns the
// zero time so the scheduler does not advance the checkpoint and the window is
// retried.
//
// Connections already seen on a previous tick (boundary overlap) are filtered
// out before processing so their metrics are emitted only once.
func (c *Collector) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	if c.featureCheck != nil {
		enabled, err := c.featureCheck(ctx)
		switch {
		case err != nil:
			// Fail open: proceed as enabled without emitting the gauge.
		case !enabled:
			c.emitFeature(e, false)
			return to, nil
		default:
			c.emitFeature(e, true)
		}
	}

	resp, err := c.api.NetworkFlowLogs(ctx, from, to)
	if err != nil {
		if isForbidden(err) {
			// The feature requires Premium/Enterprise and being enabled; a 403
			// means it is off, not a transient failure. Report it and advance.
			c.emitFeature(e, false)
			return to, nil
		}
		return time.Time{}, err
	}

	deduped := c.dedupe(resp)
	c.proc.ProcessAll(deduped, e)
	if c.onIngest != nil {
		c.onIngest(semconv.IngestSourcePoll, semconv.IngestSignalFlow, len(deduped.Logs), 0)
	}
	return to, nil
}

// dedupe returns a copy of resp with already-seen connections removed and any
// FlowLog left with zero connections dropped. A connection's identity is its
// node, window, protocol, and 5-tuple endpoints; the first sighting wins.
func (c *Collector) dedupe(resp flowlog.NetworkResponse) flowlog.NetworkResponse {
	out := flowlog.NetworkResponse{Logs: make([]flowlog.FlowLog, 0, len(resp.Logs))}
	for i := range resp.Logs {
		fl := resp.Logs[i]
		filtered := flowlog.FlowLog{
			Logged:          fl.Logged,
			NodeID:          fl.NodeID,
			Start:           fl.Start,
			End:             fl.End,
			VirtualTraffic:  c.keepNew(fl, fl.VirtualTraffic),
			SubnetTraffic:   c.keepNew(fl, fl.SubnetTraffic),
			ExitTraffic:     c.keepNew(fl, fl.ExitTraffic),
			PhysicalTraffic: c.keepNew(fl, fl.PhysicalTraffic),
		}
		if len(filtered.VirtualTraffic)+len(filtered.SubnetTraffic)+
			len(filtered.ExitTraffic)+len(filtered.PhysicalTraffic) == 0 {
			continue
		}
		out.Logs = append(out.Logs, filtered)
	}
	return out
}

// keepNew returns the subset of counts whose connection key has not been seen
// before, marking each kept connection as seen.
func (c *Collector) keepNew(fl flowlog.FlowLog, counts []flowlog.ConnectionCounts) []flowlog.ConnectionCounts {
	if len(counts) == 0 {
		return nil
	}
	kept := make([]flowlog.ConnectionCounts, 0, len(counts))
	for i := range counts {
		if c.seen.Add(connKey(fl, counts[i])) {
			kept = append(kept, counts[i])
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// connKey builds the boundary de-dup identity for a connection:
// nodeId|start|end|proto|src|dst. The window timestamps are included so an
// identical 5-tuple in a different window is still counted.
func connKey(fl flowlog.FlowLog, cc flowlog.ConnectionCounts) string {
	var b strings.Builder
	b.WriteString(fl.NodeID)
	b.WriteByte('|')
	b.WriteString(fl.Start.Format(time.RFC3339Nano))
	b.WriteByte('|')
	b.WriteString(fl.End.Format(time.RFC3339Nano))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(cc.Proto))
	b.WriteByte('|')
	b.WriteString(cc.Src)
	b.WriteByte('|')
	b.WriteString(cc.Dst)
	return b.String()
}

// emitFeature records the feature.enabled gauge for network-flow-logging.
func (c *Collector) emitFeature(e telemetry.Emitter, enabled bool) {
	var v float64
	if enabled {
		v = 1
	}
	e.Gauge(docFeatureEnabled.Name, docFeatureEnabled.Unit,
		docFeatureEnabled.Description,
		v, telemetry.Attrs{semconv.AttrFeature: featureName})
}

// isForbidden reports whether err is (or wraps) a *tsapi.StatusError with HTTP
// status 403, indicating the feature is disabled rather than a transient
// failure. This mirrors the logstream collector's precedent (see
// internal/collector/logstream/logstream.go) of classifying by the typed
// status code rather than by matching text in err.Error(): the flow-logs
// error text embeds the full request URL plus up to 16KB of response body, so
// a substring match on "403"/"forbidden" can misfire on unrelated content
// (e.g. a proxy port like 10.0.0.1:8403, or a 5xx error page whose body
// happens to mention "Forbidden") and would incorrectly advance the
// checkpoint, silently dropping the window. Only a genuine typed 403 is
// treated as "feature disabled"; every other error is ambiguous and must be
// retried.
func isForbidden(err error) bool {
	var se *tsapi.StatusError
	return errors.As(err, &se) && se.Code == 403
}
