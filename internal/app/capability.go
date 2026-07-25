package app

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/apistate"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/tsscope"
)

// This file owns the configuration-to-capability matrix (#430) and the OAuth
// scope preflight (#425) that populates one of its columns.
//
// One model, three consumers: the startup diagnostics (ScopeWarnings), the admin
// status page + /api/status.json (statusdata.Status.CapabilityMatrix), and the
// two metrics below, which give dashboards a way to render an optional-feature
// panel's empty state as "not enabled" rather than "broken".
//
// Two rules the design exists to enforce:
//
//  1. **Registration truth is supplied, never re-derived.** registerCollectors is
//     the only place that knows whether a collector was registered and why not,
//     and it records no reason of its own. Re-implementing its gating here would
//     drift silently the first time that file changed, and the matrix would then
//     confidently describe a configuration that is not running. So the caller
//     hands in CollectorDecisions.
//
//  2. **Preflight warns, never blocks.** The scope map below is a model of
//     upstream's documentation, and upstream adds scopes. A modelling bug must
//     cost a spurious WARN line, never a collector that refuses to start. The
//     server stays authoritative: a real permission problem shows up as
//     apistate.StateScopeDenied in the State column regardless of what the
//     preflight predicted.

// Metric source names and descriptors live in the leaf package
// internal/appcatalog (see appcatalog.DocCapabilityStatus /
// DocCapabilityScopeSatisfied). They cannot live here: internal/catalog
// aggregates every package's descriptors and must never import internal/app,
// because internal/app imports internal/catalog to render the status page.
const (
	MetricCapabilityStatus         = appcatalog.MetricCapabilityStatus
	MetricCapabilityScopeSatisfied = appcatalog.MetricCapabilityScopeSatisfied
)

// Bounded per-entity subrequest names, shared with the apistate coverage tally.
const (
	// SubrequestDevicePosture is the per-device posture-attributes call.
	SubrequestDevicePosture = "device_posture"
	// SubrequestDeviceInvites is the per-device device-share-invites call.
	SubrequestDeviceInvites = "device_invites"
	// SubrequestUserInvites is the tailnet user-invites call made by the users
	// collector alongside its main listing.
	SubrequestUserInvites = "user_invites"
)

// CapabilityCatalog returns the capability-matrix descriptors, re-exported from
// internal/appcatalog so callers in this package have one obvious home for them.
func CapabilityCatalog() []metricdoc.Metric { return appcatalog.CapabilityCatalog() }

// ScopeRequirement is one capability's documented OAuth-scope requirement.
//
// The zero value is deliberately the "we do not know" answer, so a capability
// added to the map without a decision fails safe into a silent unknown rather
// than into a confident wrong warning.
type ScopeRequirement struct {
	// Scopes must ALL be satisfied for the capability to work. Empty with
	// Documented=true means the capability needs no Tailscale API scope at all.
	Scopes []string
	// Documented reports whether upstream publishes a scope answer for this
	// capability. False -> statusdata.ScopeUnknown, and never warned about.
	Documented bool
}

// scoped and unscoped are the two "we know the answer" constructors; the third
// state (we do not know) is the zero ScopeRequirement.
func scoped(s ...string) ScopeRequirement { return ScopeRequirement{Scopes: s, Documented: true} }
func unscoped() ScopeRequirement          { return ScopeRequirement{Documented: true} }

// CapabilityScopes maps a capability key to the OAuth scopes upstream documents
// for it, transcribed from tailscale.com/docs/reference/trust-credentials
// (verified 2026-07-25).
//
// `services` (VIP services) and `user_invites` are recorded as UNDOCUMENTED on
// purpose: neither appears in upstream's scopes table, and inventing a plausible
// name (`services:read`?) would produce confident false warnings on correctly
// scoped deployments. An honest unknown is worth more than a guess.
//
// Note the spellings that are easy to get wrong: it is `devices_invites:read`
// (underscore, and "devices" plural), `devices:posture_attributes:read`,
// `feature_settings:read` for BOTH tailnet settings and posture integrations,
// and `account_settings:read` for contacts.
var CapabilityScopes = map[string]ScopeRequirement{
	"devices":  scoped("devices:core:read"),
	"users":    scoped("users:read"),
	"acl":      scoped("policy_file:read"),
	"dns":      scoped("dns:read"),
	"settings": scoped("feature_settings:read"),
	"contacts": scoped("account_settings:read"),
	"webhooks": scoped("webhooks:read"),
	// The keys collector lists BOTH auth keys and API access tokens from one
	// endpoint, and upstream scopes them separately, so it genuinely needs both.
	"keys":                 scoped("auth_keys:read", "api_access_tokens:read"),
	"posture_integrations": scoped("feature_settings:read"),
	"log_stream":           scoped("log_streaming:read"),
	"oauth_apps":           scoped("oauth_keys:read"),
	"flowlogs":             scoped("logs:network:read"),
	"auditlogs":            scoped("logs:configuration:read"),

	// Per-entity subrequests, keyed by their own capability so each gets its own
	// preflight datapoint. A missing devices_invites:read 403s every per-device
	// invite call while the enclosing devices tick still reports a clean scrape,
	// which is exactly the blindness this row makes visible.
	SubrequestDevicePosture: scoped("devices:posture_attributes:read"),
	SubrequestDeviceInvites: scoped("devices_invites:read"),

	// No Tailscale API scope needed: node-metrics scrapes tailscaled's own :5252
	// endpoint on each node, and object-store ingestion reads flow logs from a
	// bucket using S3 credentials.
	"nodemetrics": unscoped(),
	"objectstore": unscoped(),

	// Undocumented upstream (see above).
	"services":            {},
	SubrequestUserInvites: {},
}

// CollectorCapability maps a collector's Name() to its capability key. Most are
// identity; the exceptions are the ones where the collector name and the
// provider feature key genuinely differ.
var CollectorCapability = map[string]string{
	"devices":              "devices",
	"users":                "users",
	"keys":                 "keys",
	"settings":             "settings",
	"acl":                  "acl",
	"dns":                  "dns",
	"contacts":             "contacts",
	"webhooks":             "webhooks",
	"posture_integrations": "posture_integrations",
	// The collector is named "logstream"; the provider feature key is "log_stream".
	"logstream":   "log_stream",
	"oauth_apps":  "oauth_apps",
	"services":    "services",
	"nodemetrics": "nodemetrics",
	"flowlogs":    "flowlogs",
	// The feature probe stands in for the poller when flow logs arrive by stream
	// or object store; it reads the same tailnet settings, so it shares the
	// flowlogs capability.
	"flowlogs-feature": "flowlogs",
	"objectstore":      "objectstore",
	"auditlogs":        "auditlogs",
}

// SubrequestDecision is one optional per-entity subrequest and whether the
// configuration turned it on.
type SubrequestDecision struct {
	// Name is a bounded subrequest key (a Subrequest* constant).
	Name    string
	Enabled bool
}

// CollectorDecision is one collector's registration outcome, as decided by
// registerCollectors. The caller supplies it; nothing here second-guesses it.
type CollectorDecision struct {
	// Collector is the collector's Name().
	Collector string
	// Capability is the provider feature key. Left empty, it is resolved from
	// CollectorCapability.
	Capability string
	// ConfigEnabled is the collectors.<name>.enabled flag.
	ConfigEnabled bool
	// ProviderSupported is provider.Supports(Capability).
	ProviderSupported bool
	// Registered is whether a collector was actually put on the registry.
	Registered bool
	// Source is the effective ingestion source for the log collectors
	// ("poll"/"stream"/"both"/"objectstore"); empty for snapshot collectors.
	Source string
	// Subrequests are this collector's optional per-entity calls.
	Subrequests []SubrequestDecision
}

// CapabilityInputs is everything BuildCapabilityMatrix needs. It is the whole
// input surface: the function reads no globals and no clock.
type CapabilityInputs struct {
	Decisions []CollectorDecision
	// Scopes are the OAuth scopes REQUESTED in configuration
	// (tailscale.auth.oauth.scopes), not what the server granted — nothing in
	// this codebase ever observes the granted set.
	Scopes []string
	// ScopesKnown is true only when the credential is an OAuth client. An API key
	// or a Headscale token carries no scope list, so every row preflights as
	// ScopeUnknown rather than being wrongly reported insufficient.
	ScopesKnown bool
	// Tracker is the live per-operation availability tracker. Optional: nil (or
	// empty) leaves every State at "unknown".
	Tracker *apistate.Tracker
}

// stateRank orders availability states by how much an operator needs to see
// them, highest wins when aggregating a collector's operations.
//
// Actionable states outrank everything, most severe first, so a partial failure
// can never be hidden by a sibling success. Below them "supported" outranks
// "disabled": a collector with one optional feature off and everything else
// working should read as working, not as disabled. "unknown" is the floor.
var stateRank = map[apistate.State]int{
	apistate.StateUnknown:            0,
	apistate.StateDisabled:           1,
	apistate.StateSupported:          2,
	apistate.StateTransientFailure:   3,
	apistate.StateScopeDenied:        4,
	apistate.StateCredentialRejected: 5,
}

// BuildCapabilityMatrix is the single source of the configuration-to-capability
// matrix. Pure: same inputs, same rows, no clock and no I/O.
//
// It returns one row per collector plus one per optional subrequest, sorted by
// collector then subrequest so the JSON is stable across polls.
func BuildCapabilityMatrix(in CapabilityInputs) []statusdata.CapabilityRow {
	byCollector := trackerByCollector(in.Tracker)

	rows := make([]statusdata.CapabilityRow, 0, len(in.Decisions))
	for _, d := range in.Decisions {
		capability := d.Capability
		if capability == "" {
			capability = CollectorCapability[d.Collector]
		}
		entries := byCollector[d.Collector]

		row := statusdata.CapabilityRow{
			Collector:         d.Collector,
			Capability:        capability,
			ConfigEnabled:     d.ConfigEnabled,
			ProviderSupported: d.ProviderSupported,
			Active:            d.Registered,
			Reason:            capabilityReason(d),
			Source:            d.Source,
		}
		applyScope(&row, capability, in.Scopes, in.ScopesKnown)
		applyState(&row, entries, true)
		rows = append(rows, row)

		for _, sub := range d.Subrequests {
			srow := statusdata.CapabilityRow{
				Collector:  d.Collector,
				Subrequest: sub.Name,
				Capability: sub.Name,
				// A subrequest cannot be enabled beyond its parent, and cannot run
				// unless the parent collector actually runs.
				ConfigEnabled:     d.ConfigEnabled && sub.Enabled,
				ProviderSupported: d.ProviderSupported,
				Active:            d.Registered && sub.Enabled,
			}
			srow.Reason = capabilityReason(CollectorDecision{
				ConfigEnabled:     srow.ConfigEnabled,
				ProviderSupported: srow.ProviderSupported,
				Registered:        srow.Active,
			})
			applyScope(&srow, sub.Name, in.Scopes, in.ScopesKnown)
			// A subrequest's state comes from a tracker entry recorded under the
			// subrequest name as its operation; absent one it stays "unknown"
			// (the per-subrequest coverage tally is a separate signal).
			applyState(&srow, filterOperations(entries, sub.Name), false)
			rows = append(rows, srow)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Collector != rows[j].Collector {
			return rows[i].Collector < rows[j].Collector
		}
		return rows[i].Subrequest < rows[j].Subrequest
	})
	return rows
}

// capabilityReason states why a row is not active, checking provider support
// first: an unsupported control plane holds regardless of what the configuration
// asks for, so it is the more useful answer when both are true.
func capabilityReason(d CollectorDecision) string {
	switch {
	case !d.ProviderSupported:
		return statusdata.CapabilityReasonUnsupported
	case !d.ConfigEnabled:
		return statusdata.CapabilityReasonConfigDisabled
	case !d.Registered:
		return statusdata.CapabilityReasonNotRegistered
	default:
		return ""
	}
}

// applyScope fills the preflight columns for one capability.
func applyScope(row *statusdata.CapabilityRow, capability string, scopes []string, known bool) {
	req := CapabilityScopes[capability]
	switch {
	case !req.Documented:
		row.ScopeStatus = statusdata.ScopeUnknown
		return
	case len(req.Scopes) == 0:
		row.ScopeStatus = statusdata.ScopeNotApplicable
		return
	}

	row.RequiredScopes = req.Scopes
	if !known {
		// No scope list to check against (API key / Headscale). Reporting
		// "insufficient" here would warn on every API-key deployment.
		row.ScopeStatus = statusdata.ScopeUnknown
		return
	}

	var missing []string
	for _, req := range req.Scopes {
		if !tsscope.Satisfies(scopes, req) {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		row.ScopeStatus = statusdata.ScopeSatisfied
		return
	}
	row.ScopeStatus = statusdata.ScopeInsufficient
	row.MissingScopes = missing
}

// applyState aggregates the tracked operations onto a row. withDetail attaches
// the per-operation breakdown (collector rows only — a subrequest row's single
// operation is the row itself).
func applyState(row *statusdata.CapabilityRow, entries []apistate.Entry, withDetail bool) {
	state := apistate.StateUnknown
	var last time.Time
	for _, e := range entries {
		if stateRank[e.State] > stateRank[state] {
			state = e.State
		}
		if e.LastProbe.After(last) {
			last = e.LastProbe
		}
		if withDetail {
			row.Operations = append(row.Operations, statusdata.CapabilityOperation{
				Operation: e.Operation,
				State:     string(e.State),
				LastProbe: rfc3339OrEmpty(e.LastProbe),
			})
		}
	}
	row.State = string(state)
	row.Actionable = state.Actionable()
	row.LastProbe = rfc3339OrEmpty(last)
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(rfc3339)
}

// trackerByCollector groups the tracker snapshot by collector. Nil-safe.
func trackerByCollector(t *apistate.Tracker) map[string][]apistate.Entry {
	out := map[string][]apistate.Entry{}
	for _, e := range t.Snapshot() {
		out[e.Collector] = append(out[e.Collector], e)
	}
	return out
}

// filterOperations narrows a collector's entries to one operation name.
func filterOperations(entries []apistate.Entry, operation string) []apistate.Entry {
	var out []apistate.Entry
	for _, e := range entries {
		if e.Operation == operation {
			out = append(out, e)
		}
	}
	return out
}

// EmitCapabilityStatus writes the capability state of every RUNNING collector:
// `1` for its current state and `0` for every other state.
//
// Zero-seeding rather than a GaugeSnapshot, for the same two reasons as
// apistate.EmitAvailability: under the forced cumulative temporality a
// synchronous gauge never drops a series it has seen (otel-go #3006), so
// emitting only the current state would pin the previous one at `1` forever;
// and a snapshot replaces the whole series set for a metric NAME, which is
// wrong for a name many emitters share.
//
// Subrequest rows are skipped: their attribute set is (collector, state) too, so
// including them would emit conflicting values for the same series.
func EmitCapabilityStatus(e telemetry.Emitter, rows []statusdata.CapabilityRow) {
	if e == nil {
		return
	}
	for _, row := range rows {
		if row.Subrequest != "" || !row.Active {
			continue
		}
		for _, st := range apistate.States() {
			v := 0.0
			if string(st) == row.State {
				v = 1
			}
			e.Gauge(appcatalog.DocCapabilityStatus.Name, appcatalog.DocCapabilityStatus.Unit, appcatalog.DocCapabilityStatus.Description, v,
				telemetry.Attrs{
					semconv.AttrCollector: row.Collector,
					semconv.AttrAPIState:  string(st),
				})
		}
	}
}

// EmitScopePreflight writes the advisory scope-preflight flag, one datapoint per
// capability whose requirement is both modelled and checkable.
//
// Rows with an unknown or not-applicable scope emit NOTHING rather than `0`: a
// `0` there would be indistinguishable from a real permission gap on a dashboard.
// Duplicate capabilities (two collectors sharing one) are emitted once.
func EmitScopePreflight(e telemetry.Emitter, rows []statusdata.CapabilityRow) {
	if e == nil {
		return
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Capability] {
			continue
		}
		v := 0.0
		switch row.ScopeStatus {
		case statusdata.ScopeSatisfied:
			v = 1
		case statusdata.ScopeInsufficient:
			v = 0
		default:
			continue
		}
		seen[row.Capability] = true
		e.Gauge(appcatalog.DocCapabilityScopeSatisfied.Name, appcatalog.DocCapabilityScopeSatisfied.Unit,
			appcatalog.DocCapabilityScopeSatisfied.Description, v,
			telemetry.Attrs{semconv.AttrCapability: row.Capability})
	}
}

// ScopeWarnings returns one advisory line per RUNNING row whose configured OAuth
// scopes do not cover its documented requirement.
//
// Only active rows produce a warning. A collector the operator disabled, one the
// control plane does not support, or one whose scope is unmodelled would
// otherwise make every deployment warn about things it correctly is not doing.
func ScopeWarnings(rows []statusdata.CapabilityRow) []string {
	var out []string
	for _, row := range rows {
		if !row.Active || row.ScopeStatus != statusdata.ScopeInsufficient {
			continue
		}
		what := row.Collector
		if row.Subrequest != "" {
			what = row.Collector + "/" + row.Subrequest
		}
		out = append(out, fmt.Sprintf(
			"%s needs OAuth scope %s, which tailscale.auth.oauth.scopes does not request",
			what, strings.Join(row.MissingScopes, " + ")))
	}
	return out
}

// LogScopeWarnings logs the preflight result at startup: one WARN per gap, and
// nothing at all when there are none.
//
// Advisory by design (#425). It never returns an error and never gates startup:
// the scope map models upstream documentation, upstream adds scopes, and a
// modelling bug must not be able to take down collection. The authoritative
// answer arrives at runtime as apistate.StateScopeDenied.
func LogScopeWarnings(logger *slog.Logger, rows []statusdata.CapabilityRow) {
	if logger == nil {
		return
	}
	for _, w := range ScopeWarnings(rows) {
		logger.Warn("OAuth scope preflight: "+w,
			"advisory", true,
			"remediation", "widen tailscale.auth.oauth.scopes (or use all:read) or disable the collector")
	}
}

// capabilityMatrix assembles the matrix from live in-process state for the admin
// status page.
//
// The registration decisions are OBSERVED, not re-derived: whether a collector
// is running comes from the registry itself, so this cannot drift from
// registerCollectors the way a second copy of its gating logic would. Only the
// bounded reason is inferred, from three independent observed facts (registry
// membership, the config flag, provider support).
//
// tracker is the shared apistate.Tracker; nil leaves every State at "unknown".
func (a *App) capabilityMatrix(tracker *apistate.Tracker) []statusdata.CapabilityRow {
	scopes, known := a.oauthScopes()
	return BuildCapabilityMatrix(CapabilityInputs{
		Decisions:   a.capabilityDecisions(),
		Scopes:      scopes,
		ScopesKnown: known,
		Tracker:     tracker,
	})
}

// oauthScopes returns the REQUESTED OAuth scopes for the primary runtime and
// whether they are knowable at all. An API key carries no scope list, so the
// preflight must report "unknown" rather than guessing.
//
// In multi-tailnet mode the per-tailnet entries can differ; the primary runtime
// is the one the rest of the status header already describes, so the matrix
// stays consistent with it.
func (a *App) oauthScopes() (scopes []string, known bool) {
	if len(a.runtimes) > 0 && a.runtimes[0].authMethod != "" && a.runtimes[0].authMethod != "oauth" {
		return nil, false
	}
	resolved := a.cfg.ResolvedTailnets()
	if len(resolved) > 0 {
		t := resolved[0]
		if t.Auth.Method != "oauth" {
			return nil, false
		}
		return t.Auth.OAuth.Scopes, true
	}
	if a.cfg.Tailscale.Auth.Method != "oauth" {
		return nil, false
	}
	return a.cfg.Tailscale.Auth.OAuth.Scopes, true
}

// capabilityDecisions observes the configured-vs-registered state of every
// gateable capability across the runtimes.
func (a *App) capabilityDecisions() []CollectorDecision {
	registered := map[string]bool{}
	for _, rt := range a.runtimes {
		for _, e := range rt.registry.Entries() {
			registered[e.Collector.Name()] = true
		}
	}
	cp := a.primary().cp
	c := &a.cfg.Collectors

	decide := func(collector string, enabled bool, source string, subs ...SubrequestDecision) CollectorDecision {
		capability := CollectorCapability[collector]
		return CollectorDecision{
			Collector:         collector,
			Capability:        capability,
			ConfigEnabled:     enabled,
			ProviderSupported: cp.Supports(capability),
			Registered:        registered[collector],
			Source:            source,
			Subrequests:       subs,
		}
	}

	return []CollectorDecision{
		decide("acl", c.Acl.Enabled, ""),
		decide("auditlogs", c.Auditlogs.Enabled, c.Auditlogs.Source),
		decide("contacts", c.Contacts.Enabled, ""),
		decide("devices", c.Devices.Enabled, "",
			SubrequestDecision{Name: SubrequestDevicePosture, Enabled: c.Devices.CollectPosture},
			SubrequestDecision{Name: SubrequestDeviceInvites, Enabled: c.Devices.CollectDeviceInvites}),
		decide("dns", c.Dns.Enabled, ""),
		decide("flowlogs", c.Flowlogs.Enabled, c.Flowlogs.Source),
		decide("keys", c.Keys.Enabled, ""),
		decide("logstream", c.LogStream.Enabled, ""),
		decide("nodemetrics", c.NodeMetrics.Enabled, ""),
		decide("oauth_apps", c.OAuthApps.Enabled, ""),
		decide("posture_integrations", c.PostureIntegrations.Enabled, ""),
		decide("services", c.Services.Enabled, ""),
		decide("settings", c.Settings.Enabled, ""),
		decide("users", c.Users.Enabled, "",
			SubrequestDecision{Name: SubrequestUserInvites, Enabled: c.Users.Enabled}),
		decide("webhooks", c.Webhooks.Enabled, ""),
	}
}

// primaryAPIState returns the primary runtime's availability tracker, or nil
// when there are no runtimes yet.
//
// The tracker is per-tailnet by design: a scope denial on one tailnet says
// nothing about another, and merging them would let a healthy tailnet mask a
// broken one. The matrix describes the primary runtime for the same reason
// oauthScopes does — so every column on the status page describes one tailnet
// consistently rather than a blend.
func (a *App) primaryAPIState() *apistate.Tracker {
	if len(a.runtimes) == 0 {
		return nil
	}
	return a.runtimes[0].apiState
}
