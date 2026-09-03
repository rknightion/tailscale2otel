package devices

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// eventDeviceChange is the single structured log event for device inventory
// transitions. The change and field attributes distinguish additions/removals
// from one-field updates while keeping the Loki query surface small.
const eventDeviceChange = "tailscale.device.change"

const (
	attrDeviceChange = "tailscale.device.change"
	attrDeviceField  = "tailscale.device.field"

	// The audit old/new keys already have the free_text_details PII treatment
	// used for arbitrary before/after values. Reusing them avoids introducing a
	// second unclassified generic value pair for this structured event.
	attrDeviceOld = "tailscale.audit.old"
	attrDeviceNew = "tailscale.audit.new"

	// This is the full API name when it differs from the short host.name. It is
	// already classified as a hostname by the shared PII registry.
	attrNodeHostname = "tailscale.node.hostname"
)

// deviceChangeState is the bounded prior-state snapshot retained by the
// collector. It deliberately contains only the inventory fields that form the
// change-log contract; connectivity, addresses, DERP measurements and posture
// attributes have their own signals and are not churn events here.
//
// Slices are normalized before storage, so API ordering changes do not create a
// false change. The JSON tags are used only for the add/remove state carried in
// the old/new detail attribute; the ID is intentionally omitted from that JSON
// because host.id is the classified identity attribute for the record.
type deviceChangeState struct {
	Name              string   `json:"name,omitempty"`
	Hostname          string   `json:"hostname,omitempty"`
	OS                string   `json:"os,omitempty"`
	OSVersion         string   `json:"os_version,omitempty"`
	User              string   `json:"user,omitempty"`
	ClientVersion     string   `json:"client_version,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	AdvertisedRoutes  []string `json:"advertised_routes,omitempty"`
	EnabledRoutes     []string `json:"enabled_routes,omitempty"`
	Expires           string   `json:"expires,omitempty"`
	KeyExpiryDisabled bool     `json:"key_expiry_disabled"`
}

type deviceFieldChange struct {
	name string
	old  string
	new  string
}

// WithChangeLog gates the device inventory change log. The first successful
// poll after construction establishes a silent baseline; later polls emit
// additions, removals and one event for each changed field. The option is
// intentionally explicit because device names and users are PII-bearing log
// attributes even though the emitter still applies pii_filter.
func WithChangeLog(enabled bool) Option {
	return func(c *Collector) { c.changeLogEnabled = enabled }
}

// changeState returns the normalized, bounded state used by change detection.
func changeState(d tsapi.RichDevice) (string, deviceChangeState, bool) {
	id := d.ID
	if id == "" {
		// ID is present on Tailscale's rich response. Keep tests and alternate
		// providers useful when only nodeId is available, without allowing a
		// record with no stable identity to collapse every device into one key.
		id = d.NodeID
	}
	if id == "" {
		return "", deviceChangeState{}, false
	}
	return id, deviceChangeState{
		Name:              d.Name,
		Hostname:          d.Hostname,
		OS:                d.OS,
		OSVersion:         d.Distro.Version,
		User:              d.User,
		ClientVersion:     d.ClientVersion,
		Tags:              normalizeChangeList(d.Tags),
		AdvertisedRoutes:  normalizeChangeList(d.AdvertisedRoutes),
		EnabledRoutes:     normalizeChangeList(d.EnabledRoutes),
		Expires:           formatChangeTime(d.Expires),
		KeyExpiryDisabled: d.KeyExpiryDisabled,
	}, true
}

func normalizeChangeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	// The rich API should not duplicate these, but treating the wire as a set
	// avoids false changes if a provider repeats a tag or route.
	compact := out[:0]
	for _, value := range out {
		if len(compact) == 0 || compact[len(compact)-1] != value {
			compact = append(compact, value)
		}
	}
	return compact
}

func formatChangeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func changeStateJSON(state deviceChangeState) string {
	data, err := json.Marshal(state)
	if err != nil {
		// deviceChangeState contains only JSON-safe scalar/slice fields; retain a
		// stable empty value if that invariant ever changes rather than making a
		// telemetry path fail a successful inventory poll.
		return "{}"
	}
	return string(data)
}

func (s deviceChangeState) attrs(id string) telemetry.Attrs {
	attrs := telemetry.Attrs{semconv.HostID: id}
	if host := s.Hostname; host != "" {
		attrs[semconv.HostName] = host
	} else if s.Name != "" {
		attrs[semconv.HostName] = s.Name
	}
	if s.Name != "" {
		attrs[attrNodeHostname] = s.Name
	}
	if s.OS != "" {
		attrs[semconv.OSType] = s.OS
	}
	if s.OSVersion != "" {
		attrs[semconv.OSVersion] = s.OSVersion
	}
	if s.User != "" {
		attrs[semconv.AttrUser] = s.User
	}
	if len(s.Tags) > 0 {
		attrs[semconv.AttrTags] = strings.Join(s.Tags, ",")
	}
	if s.ClientVersion != "" {
		attrs[attrClientVersion] = s.ClientVersion
	}
	return attrs
}

// fieldChanges compares only the fields named by the task's device inventory
// change contract. The order is fixed so two API responses with the same state
// produce byte-for-byte equivalent event order.
func fieldChanges(old, current deviceChangeState) []deviceFieldChange {
	changes := make([]deviceFieldChange, 0, 11)
	add := func(name, before, after string) {
		if before != after {
			changes = append(changes, deviceFieldChange{name: name, old: before, new: after})
		}
	}
	add("name", old.Name, current.Name)
	add("hostname", old.Hostname, current.Hostname)
	add("os", old.OS, current.OS)
	add("os_version", old.OSVersion, current.OSVersion)
	add("user", old.User, current.User)
	add("client_version", old.ClientVersion, current.ClientVersion)
	add("tags", strings.Join(old.Tags, ","), strings.Join(current.Tags, ","))
	add("routes_advertised", strings.Join(old.AdvertisedRoutes, ","), strings.Join(current.AdvertisedRoutes, ","))
	add("routes_enabled", strings.Join(old.EnabledRoutes, ","), strings.Join(current.EnabledRoutes, ","))
	add("key_expiry", old.Expires, current.Expires)
	add("key_expiry_disabled", strconv.FormatBool(old.KeyExpiryDisabled), strconv.FormatBool(current.KeyExpiryDisabled))
	return changes
}

func (c *Collector) emitDeviceChange(e telemetry.Emitter, at time.Time, id string, current deviceChangeState, transition string, field deviceFieldChange) {
	attrs := current.attrs(id)
	attrs[attrDeviceChange] = transition
	attrs[attrDeviceField] = field.name
	if field.old != "" {
		attrs[attrDeviceOld] = field.old
	}
	if field.new != "" {
		attrs[attrDeviceNew] = field.new
	}
	if field.name == "device" {
		if transition == "removed" {
			attrs[attrDeviceOld] = changeStateJSON(current)
		} else {
			attrs[attrDeviceNew] = changeStateJSON(current)
		}
	}
	e.LogEvent(telemetry.Event{
		Name:      eventDeviceChange,
		Severity:  telemetry.SeverityInfo,
		Timestamp: at,
		Body:      "device " + transition,
		Attrs:     attrs,
	})
}

// observeDeviceChanges establishes or advances the in-memory baseline. It is
// called only after DevicesRich succeeds. A successful empty response is still
// a baseline, so the first non-empty response after process start cannot create
// a startup storm.
func (c *Collector) observeDeviceChanges(e telemetry.Emitter, devs []tsapi.RichDevice, at time.Time) {
	current := make(map[string]deviceChangeState, len(devs))
	for i := range devs {
		if id, state, ok := changeState(devs[i]); ok {
			current[id] = state
		}
	}
	if !c.changeLogBaselineReady {
		c.changeLogBaseline = current
		c.changeLogBaselineReady = true
		return
	}

	ids := make([]string, 0, len(c.changeLogBaseline)+len(current))
	seen := make(map[string]struct{}, len(c.changeLogBaseline)+len(current))
	for id := range c.changeLogBaseline {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range current {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	for _, id := range ids {
		previous, hadPrevious := c.changeLogBaseline[id]
		next, hasNext := current[id]
		switch {
		case !hadPrevious && hasNext:
			c.emitDeviceChange(e, at, id, next, "added", deviceFieldChange{name: "device"})
		case hadPrevious && !hasNext:
			c.emitDeviceChange(e, at, id, previous, "removed", deviceFieldChange{name: "device"})
		case hadPrevious && hasNext:
			for _, field := range fieldChanges(previous, next) {
				c.emitDeviceChange(e, at, id, next, "changed", field)
			}
		}
	}
	c.changeLogBaseline = current
}
