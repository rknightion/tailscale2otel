// Package logstream is a stateful snapshot collector for the tailnet's
// configuration/network log-streaming DELIVERY HEALTH (GET
// /logging/{type}/stream/status) — Tailscale's own view of whether it is
// successfully delivering audit/flow logs to the configured SIEM sink.
//
// The API's cumulative counters (numBytesSent, numTotalRequests, …) are emitted
// as deltas via the Emitter's additive Counter (mirroring nodemetrics): the
// first scrape of each (logType, counter) seeds a baseline and emits nothing;
// later scrapes emit current-minus-previous, or the current value on a reset
// (the stream config was recreated). This makes the collector stateful.
//
// Gating is bulletproof because the collector is enabled by default: any 4xx
// (StatusError) OR a 2xx body with no recognized status fields → configured=0,
// idle, NO error (a tailnet with no SIEM sink never produces scrape-error
// noise); only 5xx/transport errors surface as scrape failures.
package logstream

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

// Compile-time assertions.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

const defaultInterval = 600 * time.Second

// logTypes are the two streamable log types, matching the audit/flow naming.
var logTypes = []string{"configuration", "network"}

const attrType = "tailscale.logstream.type"

const (
	metricConfigured      = "tailscale.logstream.configured"
	metricBytesSent       = "tailscale.logstream.bytes_sent"
	metricEntriesSent     = "tailscale.logstream.entries_sent"
	metricRequests        = "tailscale.logstream.requests"
	metricRequestsFailed  = "tailscale.logstream.requests_failed"
	metricSpoofedEntries  = "tailscale.logstream.spoofed_entries"
	metricMaxBodyRequests = "tailscale.logstream.max_body_requests"
	metricLastActivity    = "tailscale.logstream.last_activity"
	metricError           = "tailscale.logstream.error"
)

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	LogStreamStatus(ctx context.Context, logType string) (*tsapi.LogStreamStatus, error)
}

// Collector implements collector.SnapshotCollector for log-stream delivery health.
type Collector struct {
	api      api
	interval time.Duration
	// prev holds the last cumulative counter value per (logType -> metricName)
	// for delta emission. The collector is stateful between ticks.
	prev map[string]map[string]float64
}

// New returns a logstream collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval, prev: map[string]map[string]float64{}}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "logstream" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect probes each log type's stream-status endpoint, gating 4xx/empty as
// "not configured" and emitting health (delta counters + gauges + error log)
// for configured streams. A 5xx/transport error on any type is returned (the
// other type is still attempted first).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	var firstErr error
	for _, lt := range logTypes {
		st, err := c.api.LogStreamStatus(ctx, lt)
		if err != nil {
			var se *tsapi.StatusError
			if errors.As(err, &se) && se.Code >= 400 && se.Code < 500 {
				c.emitConfigured(e, lt, 0)
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !streamConfigured(st) {
			c.emitConfigured(e, lt, 0)
			continue
		}
		c.emitHealth(e, lt, st)
	}
	return firstErr
}

func (c *Collector) emitConfigured(e telemetry.Emitter, lt string, v float64) {
	e.Gauge(docConfigured.Name, docConfigured.Unit, docConfigured.Description, v, telemetry.Attrs{attrType: lt})
}

// streamConfigured reports whether a 200 status body describes an actually
// configured stream (vs an all-zero body for an unconfigured log type).
func streamConfigured(st *tsapi.LogStreamStatus) bool {
	return st.MaxNumEntries > 0 || st.MaxBodySize > 0 || st.NumTotalRequests > 0 || !st.LastActivity.IsZero()
}

func (c *Collector) emitHealth(e telemetry.Emitter, lt string, st *tsapi.LogStreamStatus) {
	attrs := telemetry.Attrs{attrType: lt}
	c.emitConfigured(e, lt, 1)

	c.emitDelta(e, lt, docBytesSent, float64(st.NumBytesSent), attrs)
	c.emitDelta(e, lt, docEntriesSent, float64(st.NumEntriesSent), attrs)
	c.emitDelta(e, lt, docRequests, float64(st.NumTotalRequests), attrs)
	c.emitDelta(e, lt, docRequestsFailed, float64(st.NumFailedRequests), attrs)
	c.emitDelta(e, lt, docSpoofedEntries, float64(st.NumSpoofedEntries), attrs)
	c.emitDelta(e, lt, docMaxBodyRequests, float64(st.NumMaxBodyRequests), attrs)

	if !st.LastActivity.IsZero() {
		e.Gauge(docLastActivity.Name, docLastActivity.Unit, docLastActivity.Description,
			float64(st.LastActivity.Unix()), attrs)
	}

	errVal := 0.0
	if st.LastError != "" {
		errVal = 1
	}
	e.Gauge(docError.Name, docError.Unit, docError.Description, errVal, attrs)
	if st.LastError != "" {
		e.LogEvent(telemetry.Event{
			Name:     docErrorLog.Name,
			Severity: telemetry.SeverityError,
			Body:     st.LastError,
			// Raw upstream error text — free-text; drop the body when free_text_details is off (#197).
			BodyPII: []pii.Category{pii.CatFreeTextDetails},
			Attrs:   telemetry.Attrs{attrType: lt},
		})
	}
}

// emitDelta seeds a baseline on first observation (emitting nothing) and emits
// the positive delta thereafter, or the current value on a counter reset (the
// cumulative dropped because the stream config was recreated).
func (c *Collector) emitDelta(e telemetry.Emitter, lt string, doc metricdoc.Metric, cumulative float64, attrs telemetry.Attrs) {
	pm := c.prev[lt]
	if pm == nil {
		pm = map[string]float64{}
		c.prev[lt] = pm
	}
	prevVal, seen := pm[doc.Name]
	pm[doc.Name] = cumulative
	if !seen {
		return
	}
	delta := cumulative - prevVal
	if cumulative < prevVal {
		delta = cumulative
	}
	if delta > 0 {
		e.Counter(doc.Name, doc.Unit, doc.Description, delta, attrs)
	}
}
