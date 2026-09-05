// Package logstream is a stateful snapshot collector for the tailnet's
// configuration/network log-streaming configuration and DELIVERY HEALTH (GET
// /logging/{type}/stream and /logging/{type}/stream/status) — Tailscale's own
// view of the configured SIEM destination and whether it is successfully
// delivering audit/flow logs to that sink.
//
// The API's cumulative counters (numBytesSent, numTotalRequests, …) are emitted
// as deltas via the Emitter's additive Counter (mirroring nodemetrics): the
// first scrape of each (logType, counter) seeds a baseline and emits nothing;
// later scrapes emit current-minus-previous, or the current value on a reset
// (the stream config was recreated). This makes the collector stateful.
//
// Delivery-health gating (#420): only a 404 — the endpoint is absent for this
// tailnet — OR a 2xx body with no recognized status fields means "no stream
// configured", which emits configured=0, stays idle and returns NO error, so a
// tailnet with no SIEM sink never produces scrape-error noise. The separate
// configuration endpoint has an ambiguous 404, so that outcome is recorded as
// unknown and emits no configuration assertion.
//
// It used to fold ANY 4xx into that same benign reading, which is precisely the
// bug #420 was filed for: a revoked credential (401) and a missing logs:read
// scope (403) both surfaced as a deliberate-looking configured=0. Those now
// classify as credential_rejected / scope_denied, return an error, and
// deliberately emit NO configured gauge — being denied tells us nothing about
// whether a sink exists, and claiming 0 would be inventing an answer.
package logstream

import (
	"context"
	"net/http"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
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

// Configuration labels are separate from the delivery-health type label so the
// existing status and counter series keep their shape. Both values are bounded
// by the API's supported log types/destination enum before entering telemetry.
const (
	attrLogType         = "tailscale.logstream.log_type"
	attrDestinationType = "tailscale.logstream.destination_type"
	destinationUnknown  = "unknown"
)

// opStatusPrefix is the upstream operationId of the status probe. The two log
// types are INDEPENDENT probes with independent outcomes (one tailnet routinely
// streams configuration logs and not network logs), so the availability signal
// is keyed per log type: apistate.EmitAvailability takes only a collector and an
// operation, and a single shared name would let the two probes overwrite each
// other's state every tick. The suffix keeps the label bounded at exactly two
// values.
const opStatusPrefix = "getLogStreamingStatus"

// opConfigurationPrefix is the upstream operationId of the read-only
// destination configuration lookup. The suffix keeps availability independent
// for the two supported log types, matching the status operation above.
const opConfigurationPrefix = "getLogStreamingConfiguration"

// statusOperation returns the availability operation name for a log type.
func statusOperation(logType string) string { return opStatusPrefix + "." + logType }

// configurationOperation returns the availability operation name for a log
// type.
func configurationOperation(logType string) string {
	return opConfigurationPrefix + "." + logType
}

// statusDisposition is the DEFAULT disposition: no status code is read as
// "feature disabled" beyond the built-in 404. Upstream does not document 403 on
// this endpoint as a plan gate — it is a scope denial, and reading it as
// disabled is what turned a permission regression into a healthy zero.
var statusDisposition = apistate.Disposition{}

// configurationDisposition is the default disposition: a 403 means the
// credential lacks the endpoint's log_streaming:read scope. The collector has
// a local 404 override because this endpoint explicitly documents that status
// as ambiguous rather than as disabled.
var configurationDisposition = apistate.Disposition{}

const (
	metricConfigured            = "tailscale.logstream.configured"
	metricDestinationConfigured = "tailscale.logstream.destination.configured"
	metricBytesSent             = "tailscale.logstream.bytes_sent"
	metricEntriesSent           = "tailscale.logstream.entries_sent"
	metricRequests              = "tailscale.logstream.requests"
	metricRequestsFailed        = "tailscale.logstream.requests_failed"
	metricSpoofedEntries        = "tailscale.logstream.spoofed_entries"
	metricMaxBodyRequests       = "tailscale.logstream.max_body_requests"
	metricLastActivity          = "tailscale.logstream.last_activity"
	metricError                 = "tailscale.logstream.error"
)

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	LogStreamStatus(ctx context.Context, logType string) (*tsapi.LogStreamStatus, error)
	LogStreamConfiguration(ctx context.Context, logType string) (*tsapi.LogStreamConfiguration, error)
}

// Collector implements collector.SnapshotCollector for log-stream delivery health.
type Collector struct {
	api      api
	interval time.Duration
	// configurationInterval and networkInterval are optional per-type probe
	// cadences. A non-positive value inherits interval. When either is set,
	// PollInterval returns the faster cadence and Collect skips the other probe
	// until its own interval is due.
	configurationInterval time.Duration
	networkInterval       time.Duration
	independentScheduling bool
	lastProbe             map[string]time.Time
	// prev holds the last cumulative counter value per (logType -> metricName)
	// for delta emission. The collector is stateful between ticks.
	prev map[string]map[string]float64
	// tracker records per-operation availability for the admin status page. A
	// nil *apistate.Tracker is a no-op, so it needs no nil check.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420) that
// backs the admin status page. Availability METRICS are emitted regardless; the
// tracker is only the in-process introspection copy.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

// WithProbeIntervals sets independent status-probe cadences for the
// configuration and network streams. A non-positive value inherits the shared
// collector interval, preserving existing single-interval configurations.
// PollInterval exposes the cadence the registry should use when one probe is
// faster than the other.
func WithProbeIntervals(configuration, network time.Duration) Option {
	return func(c *Collector) {
		c.configurationInterval = configuration
		c.networkInterval = network
		c.independentScheduling = configuration > 0 || network > 0
	}
}

// New returns a logstream collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{
		api:       a,
		interval:  interval,
		prev:      map[string]map[string]float64{},
		lastProbe: map[string]time.Time{},
		now:       time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
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

// PollInterval returns the scheduler cadence for this collector. With no
// independent probe interval configured it is exactly the shared interval;
// otherwise it is the faster effective per-type interval.
func (c *Collector) PollInterval() time.Duration {
	shared := c.DefaultInterval()
	if !c.independentScheduling {
		return shared
	}
	configuration := c.configurationInterval
	if configuration <= 0 {
		configuration = shared
	}
	network := c.networkInterval
	if network <= 0 {
		network = shared
	}
	if network < configuration {
		return network
	}
	return configuration
}

func (c *Collector) probeInterval(logType string) time.Duration {
	d := c.networkInterval
	if logType == "configuration" {
		d = c.configurationInterval
	}
	if d <= 0 {
		return c.DefaultInterval()
	}
	return d
}

func (c *Collector) probeDue(logType string, at time.Time) bool {
	if !c.independentScheduling {
		return true
	}
	last, ok := c.lastProbe[logType]
	return !ok || !at.Before(last.Add(c.probeInterval(logType)))
}

// Collect probes each log type's read-only configuration and stream-status
// endpoints. A successful configuration lookup emits one bounded destination
// point. An ambiguous configuration 404 emits no assertion and records unknown;
// a 403 or any other failure is returned after both endpoints for both log types
// have been attempted. Delivery health keeps its existing 404/empty-200 gating
// and delta-counter behavior. Every probe emits its bounded availability state
// (#420).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	var firstErr error
	at := c.now()
	for _, lt := range logTypes {
		if !c.probeDue(lt, at) {
			continue
		}
		c.lastProbe[lt] = at

		cfg, cfgErr := c.api.LogStreamConfiguration(ctx, lt)
		cfgState := c.observeConfiguration(e, lt, cfgErr)
		if cfgErr != nil {
			// The configuration endpoint documents 404 for both an absent sink
			// and an inaccessible/unsupported log type. It is therefore an
			// explicit unknown, not disabled, and does not make this scrape fail.
			if cfgState != apistate.StateUnknown || !isNotFound(cfgErr) {
				if firstErr == nil {
					firstErr = cfgErr
				}
			}
		} else {
			c.emitConfiguration(e, lt, cfg)
		}

		st, err := c.api.LogStreamStatus(ctx, lt)
		state := apistate.Observe(e, c.tracker, c.Name(), statusOperation(lt), statusDisposition, err, c.now())
		if err != nil {
			if state == apistate.StateDisabled {
				// 404: the endpoint is not present for this tailnet, so there is
				// genuinely no stream. Benign and idle.
				c.emitConfigured(e, lt, 0)
				continue
			}
			// Deliberately no configured gauge here. A 401/403/5xx means we did
			// not learn whether a stream exists; emitting 0 would manufacture a
			// healthy-looking answer out of a failure.
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

// observeConfiguration records availability for the configuration lookup. The
// shared classifier intentionally maps every 404 to disabled, but this endpoint
// documents 404 as an ambiguous combination of no sink, unsupported log type,
// and insufficient access. Preserve that distinction by overriding only this
// operation's 404 to StateUnknown.
func (c *Collector) observeConfiguration(e telemetry.Emitter, logType string, err error) apistate.State {
	if !isNotFound(err) {
		return apistate.Observe(e, c.tracker, c.Name(), configurationOperation(logType), configurationDisposition, err, c.now())
	}
	state := apistate.StateUnknown
	operation := configurationOperation(logType)
	c.tracker.Record(c.Name(), operation, state, c.now())
	if e != nil {
		apistate.EmitAvailability(e, c.Name(), operation, state)
		apistate.EmitLastProbe(e, c.Name(), operation, c.now())
	}
	return state
}

func isNotFound(err error) bool {
	code, ok := tsapi.StatusCode(err)
	return ok && code == http.StatusNotFound
}

func (c *Collector) emitConfiguration(e telemetry.Emitter, requestedLogType string, cfg *tsapi.LogStreamConfiguration) {
	if cfg == nil {
		return
	}
	logType := boundedLogType(cfg.LogType, requestedLogType)
	destinationType := boundedDestinationType(cfg.DestinationType)
	e.Gauge(docDestinationConfigured.Name, docDestinationConfigured.Unit, docDestinationConfigured.Description,
		1, telemetry.Attrs{attrLogType: logType, attrDestinationType: destinationType})
}

func boundedLogType(value, fallback string) string {
	switch value {
	case "configuration", "network":
		return value
	default:
		return fallback
	}
}

func boundedDestinationType(value string) string {
	switch value {
	case "splunk", "elastic", "panther", "cribl", "datadog", "axiom", "s3":
		return value
	default:
		return destinationUnknown
	}
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
