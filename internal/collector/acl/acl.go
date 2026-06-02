// Package acl is a snapshot collector for the tailnet ACL policy file. It is
// stateful: it remembers the last-seen ETag so it can report when the policy
// last changed (tailscale.acl.last_changed, Unix seconds) without parsing the
// HuJSON document.
package acl

import (
	"context"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const defaultInterval = 600 * time.Second

// Metric names emitted by this collector.
const (
	metricLastChanged = "tailscale.acl.last_changed"
	metricSize        = "tailscale.acl.size"
)

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	PolicyFileRaw(ctx context.Context) (*tsclient.RawACL, error)
}

// Collector implements collector.SnapshotCollector for the ACL policy file.
// It keeps state across ticks: the last-seen ETag and the wall-clock time at
// which that ETag was first observed.
type Collector struct {
	api      api
	interval time.Duration
	now      func() time.Time

	lastETag    string
	haveETag    bool
	lastChanged time.Time
}

// New returns an ACL collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval. A nil now defaults to time.Now.
func New(a api, interval time.Duration, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{api: a, interval: interval, now: now}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "acl" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches the raw ACL and emits the last-changed timestamp. When the
// ETag differs from the previously stored one (including the first observation)
// it records now() as the change time; otherwise it keeps the prior value.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	raw, err := c.api.PolicyFileRaw(ctx)
	if err != nil {
		return err
	}

	if !c.haveETag || raw.ETag != c.lastETag {
		c.lastETag = raw.ETag
		c.haveETag = true
		c.lastChanged = c.now()
	}

	e.Gauge(metricLastChanged, semconv.UnitSeconds, "Unix time the ACL policy last changed (by ETag)",
		float64(c.lastChanged.Unix()), nil)

	// Trivial presence/size signal: bytes of the raw HuJSON policy document.
	// (A per-section rule count would require parsing HuJSON, so it is omitted.)
	e.Gauge(metricSize, semconv.UnitBytes, "size of the raw HuJSON ACL document in bytes",
		float64(len(raw.HuJSON)), nil)

	return nil
}
