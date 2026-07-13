package app

import (
	"sort"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/contacts"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/dns"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/flowlogs"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/keys"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/logstream"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/oauthapps"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/postureintegrations"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/services"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/settings"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/users"
	"github.com/rknightion/tailscale2otel/v2/internal/collector/webhooks"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
)

// collectorDoc is the admin info-tooltip data for one collector: a one-line
// purpose plus the metric descriptors it emits. The metric list is pulled from
// the in-code catalogs (the same source as docs/metrics.md), so the tooltip can
// never drift from what is actually emitted. Keys are collector Name() values.
type collectorDoc struct {
	about   string
	metrics func() []metricdoc.Metric
}

// concat builds a metrics func that joins several catalogs — used by the window
// collectors, which emit through shared processors living in other packages
// (flowlogs via internal/flowlog, auditlogs via internal/audit).
func concat(srcs ...func() []metricdoc.Metric) func() []metricdoc.Metric {
	return func() []metricdoc.Metric {
		var out []metricdoc.Metric
		for _, src := range srcs {
			out = append(out, src()...)
		}
		return out
	}
}

// collectorDocs maps each collector Name() to its tooltip data. The guard test
// TestCollectorDocs_CoverAllRegistered ensures every registerable collector has
// an entry here, so a new collector cannot ship without its tooltip.
var collectorDocs = map[string]collectorDoc{
	"devices": {
		about:   "Lists every tailnet device (rich GET /devices?fields=all): per-device and aggregate metrics, and refreshes the IP→name enrichment cache used by flow/audit.",
		metrics: devices.Catalog,
	},
	"users": {
		about:   "Reports the tailnet user inventory — aggregate counts by role/status/type plus per-user device count, connection state and last-seen time.",
		metrics: users.Catalog,
	},
	"keys": {
		about:   "Reports auth/API key inventory — per-key expiry, counts by derived type (and revoked/invalid state), and a warning log for keys nearing expiry.",
		metrics: keys.Catalog,
	},
	"settings": {
		about:   "Reports tailnet feature settings — one gauge per boolean feature plus the device key-expiry duration.",
		metrics: settings.Catalog,
	},
	"acl": {
		about:   "Tracks the tailnet ACL policy file — when it last changed, its document size, and per-section rule counts.",
		metrics: acl.Catalog,
	},
	"dns": {
		about:   "Reports the tailnet DNS configuration — counts of nameservers, search paths and split-DNS zones, plus the MagicDNS flag.",
		metrics: dns.Catalog,
	},
	"contacts": {
		about:   "Reports tailnet account/support/security contacts — whether each contact email still needs verification (the address itself is never emitted).",
		metrics: contacts.Catalog,
	},
	"webhooks": {
		about:   "Inventories the tailnet's configured webhook endpoints (where Tailscale posts events). URLs, secrets and creator names are never emitted.",
		metrics: webhooks.Catalog,
	},
	"posture_integrations": {
		about:   "Reports device-posture integrations (MDM/EDR such as Intune) — integration count, per-integration match counts, and last-sync time for staleness alerting.",
		metrics: postureintegrations.Catalog,
	},
	"logstream": {
		about:   "Reports configuration/network log-streaming delivery health — Tailscale's own view of whether it is delivering audit/flow logs to your configured SIEM sink.",
		metrics: logstream.Catalog,
	},
	"oauth_apps": {
		about:   "Inventories the tailnet's OAuth applications (device-provisioning, alpha API) — app count plus per-app scope and allowed-node-attribute gauges. Idles silently on tailnets without the feature.",
		metrics: oauthapps.Catalog,
	},
	"services": {
		about:   "Reports Tailscale Services (VIP) — service count plus per-service exposed-port rules and (optionally) backing-host counts bucketed by approval/config state.",
		metrics: services.Catalog,
	},
	"nodemetrics": {
		about:   "Scrapes per-node tailscaled /metrics endpoints and re-emits every sample centrally, carrying node identity as labels (plus dynamic-discovery stats).",
		metrics: nodemetrics.Catalog,
	},
	"flowlogs": {
		about:   "Polls Tailscale network flow logs for each time window and converts them to flow metrics + log records (the same processor used by the streaming receiver).",
		metrics: concat(flowlogs.Catalog, flowlog.Catalog),
	},
	"flowlogs-feature": {
		about:   "Lightweight probe used in stream-only mode: reports whether network flow logging is enabled on the tailnet, without polling any logs.",
		metrics: flowlogs.Catalog,
	},
	"auditlogs": {
		about:   "Polls the tailnet configuration audit log for each time window and emits audit log records plus an events counter (shared with the streaming receiver).",
		metrics: audit.Catalog,
	},
}

// collectorBrief returns the tooltip purpose and emitted-metric briefs for the
// named collector, sorted by metric name for a stable display. It returns zero
// values when no doc is registered for the name (the tooltip is then omitted).
func collectorBrief(name string) (string, []statusdata.MetricBrief) {
	d, ok := collectorDocs[name]
	if !ok {
		return "", nil
	}
	var ms []statusdata.MetricBrief
	if d.metrics != nil {
		for _, m := range d.metrics() {
			ms = append(ms, statusdata.MetricBrief{Name: m.Name, Description: m.Description})
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	}
	return d.about, ms
}
