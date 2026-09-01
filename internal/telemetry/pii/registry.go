package pii

// keyCategory maps a fixed-meaning attribute key to its category.
var keyCategory = map[string]Category{
	"tailscale.user":                     CatEmails,
	"tailscale.src.user":                 CatEmails, // flow endpoint identity, from the record's srcNode block
	"tailscale.dst.user":                 CatEmails, // flow endpoint identity, from the record's dstNodes block
	"user.name":                          CatEmails, // audit actor + device-invite acceptor + users collector login
	"user.full_name":                     CatUserDisplayNames,
	"user.id":                            CatUserIDs,
	"host.name":                          CatHostnames,
	"tailscale.node.hostname":            CatHostnames,
	"host.id":                            CatNodeIDs,
	"tailscale.node.id":                  CatNodeIDs,
	"tailscale.service.name":             CatServiceAddrs,
	"tailscale.service.display_name":     CatServiceAddrs,
	"endpoint":                           CatEndpointPaths,
	"tailscale.route.cidr":               CatNetworkTopology,
	"tailscale.dns.resolver.domain":      CatNetworkTopology,
	"tailscale.dns.search_path.domain":   CatNetworkTopology, // J-A3 search-path info gauge
	"tailscale.tailnet":                  CatTailnetName,
	"tailscale.audit.old":                CatFreeTextDetails,
	"tailscale.audit.new":                CatFreeTextDetails,
	"tailscale.audit.details":            CatFreeTextDetails,
	"tailscale.device.posture.details":   CatFreeTextDetails, // #56: dynamic posture-log attribute map
	"error.message":                      CatFreeTextDetails,
	"tailscale.target.name":              CatFreeTextDetails,
	"tailscale.key.description":          CatFreeTextDetails,
	"tailscale.oauth_app.name":           CatFreeTextDetails, // #167: operator-chosen app label (like key.description)
	"tailscale.key.owner":                CatUserIDs,         // #165: key owner userId on per-key gauges + keys.by_owner
	"value":                              CatFreeTextDetails,
	"tailscale.acl.rule":                 CatFreeTextDetails, // J-B1 risky-rule src/dst contents
	"tailscale.webhook.node.id":          CatNodeIDs,
	"tailscale.webhook.node.device.name": CatHostnames,
	"tailscale.webhook.node.managed_by":  CatEmails,
	"tailscale.webhook.actor":            CatEmails,
	"tailscale.webhook.url":              CatEndpointPaths,
	"tailscale.webhook.user":             CatEmails,
	"coordination.lease_name":            CatFreeTextDetails, // operator-chosen Kubernetes object name
	"coordination.namespace":             CatFreeTextDetails, // operator-chosen Kubernetes namespace
	"coordination.identity":              CatHostnames,       // pod hostname used by client-go leader election

	// Kubernetes-audit keys (#462), from tsrecorder's API-server-proxy events.
	// Note tailscale.k8s.path is the QUERY-FREE kubernetes.Path — the raw
	// request.Path is never emitted at all, because it carries the exec command
	// line inside its query string.
	"tailscale.k8s.user":           CatEmails,
	"tailscale.k8s.src_node":       CatHostnames,
	"tailscale.k8s.recorder":       CatHostnames,
	"tailscale.k8s.src_node_id":    CatNodeIDs,
	"tailscale.k8s.path":           CatEndpointPaths,
	"tailscale.k8s.object_name":    CatFreeTextDetails, // arbitrary Kubernetes object names
	"tailscale.k8s.label_selector": CatFreeTextDetails,
	"tailscale.k8s.field_selector": CatFreeTextDetails,
	"tailscale.k8s.pod":            CatFreeTextDetails,
	"tailscale.k8s.container":      CatFreeTextDetails,
	"tailscale.k8s.command":        CatCommandText, // human-typed; may contain a pasted secret

	// Span-only keys (#212). Spans go through the same policy as metrics and logs
	// via telemetry.piiSpanExporter, so these must be classified here too.
	"url.full":             CatEndpointPaths,   // full request URL: carries the tailnet name + device id
	"tailscale.endpoint":   CatEndpointPaths,   // elided endpoint label (same class as the "endpoint" metric attr)
	"exception.message":    CatFreeTextDetails, // RecordError: sanitized error text, may embed identifiers
	"exception.stacktrace": CatFreeTextDetails, // RecordError(WithStackTrace): file paths
}

// ipValueKeys are keys whose VALUE is an IP that must be range-classified. For mixed
// keys (src/dst.node, exit_node, node), a non-IP value falls back to ipKeyFallback[key].
var ipValueKeys = map[string]bool{
	"source.address":                 true,
	"destination.address":            true,
	"tailscale.dns.resolver.address": true,
	"tailscale.src.node":             true,
	"tailscale.dst.node":             true,
	"tailscale.exit_node":            true,
	"tailscale.node":                 true,
}

// ipKeyFallback is the category for an ipValueKey whose value is NOT an IP.
var ipKeyFallback = map[string]Category{
	"tailscale.src.node":  CatHostnames,
	"tailscale.dst.node":  CatHostnames,
	"tailscale.exit_node": CatHostnames,
	"tailscale.node":      CatHostnames,
}

// identityKeys are attr keys that constitute a gauge/updowncounter series' identity.
// A gauge is suppressed only when ALL of its present identity keys are redacted.
var identityKeys = map[string]bool{
	"host.name":                        true,
	"host.id":                          true,
	"tailscale.node.hostname":          true,
	"tailscale.node.id":                true,
	"tailscale.exit_node":              true,
	"tailscale.node":                   true,
	"tailscale.dns.resolver.address":   true,
	"tailscale.dns.resolver.domain":    true,
	"tailscale.src.node":               true,
	"tailscale.dst.node":               true,
	"tailscale.service.name":           true,
	"tailscale.dns.search_path.domain": true, // J-A3: domain is this info gauge's identity
	"user.id":                          true, // #74: per-user gauge identity (CatUserIDs)
	"user.name":                        true, // #74: per-user gauge identity (CatEmails)
	"tailscale.key.owner":              true, // #165: keys.by_owner series identity (CatUserIDs)
	"coordination.lease_name":          true,
	"coordination.namespace":           true,
	"coordination.identity":            true,
}

// geoNonIdentifier are the GeoIP-enrichment keys (#461). Every one is DERIVED
// from an address that is already on the record — source.address /
// destination.address are the redactable originals, classified above by IP
// range. Enrichment therefore adds no new identifying surface: an operator who
// has redacted external IPs has removed the input, and one who has not is
// already exporting a more precise identifier than "Germany".
//
// The coordinates are the only entries worth a second look, and they are the
// coordinates of an IP BLOCK, not of a person: MaxMind resolves to a city or
// regional centroid, and the accuracy radius is measured in kilometers. They are
// also confined to flow LOGS beside the full address, so they never widen what a
// metric exposes. Geolocating a tailnet address is impossible by construction —
// geoip.Enrichable refuses the CGNAT range and the ULA before any lookup.
var geoNonIdentifier = map[string]bool{
	"source.geo.country.iso_code":      true,
	"source.geo.continent.code":        true,
	"source.geo.locality.name":         true,
	"source.geo.region.iso_code":       true,
	"source.geo.location.lat":          true,
	"source.geo.location.lon":          true,
	"destination.geo.country.iso_code": true,
	"destination.geo.continent.code":   true,
	"destination.geo.locality.name":    true,
	"destination.geo.region.iso_code":  true,
	"destination.geo.location.lat":     true,
	"destination.geo.location.lon":     true,
	"source.as.number":                 true,
	"source.as.organization.name":      true,
	"destination.as.number":            true,
	"destination.as.organization.name": true,
	// Device geography, on the bounded fleet rollup only.
	"geo.country.iso_code": true,
	"geo.continent.code":   true,
	// Self-observability dimensions: which database, which MaxMind edition.
	// Closed sets naming software, not addresses or people.
	"geoip.database": true,
	"geoip.edition":  true,
}

// nonIdentifier is the explicit allowlist of keys that are never PII/identifiers.
var nonIdentifier = map[string]bool{
	// Kubernetes-audit (#462): every one of these is normalized to a closed
	// admit-set before emission (unknown -> "other"), so none can carry a
	// client-controlled value. tailscale.k8s.command_class in particular is the
	// bounded classification of the exec command line, and is listed here rather
	// than under CatCommandText on purpose: it holds no free text, and it must
	// keep working when an operator redacts the raw command it was derived from.
	"tailscale.k8s.verb": true, "tailscale.k8s.resource": true,
	"tailscale.k8s.subresource": true, "tailscale.k8s.api_group": true,
	"tailscale.k8s.namespace": true, "tailscale.k8s.user_agent": true,
	"tailscale.k8s.command_class": true, "tailscale.k8s.session_type": true,
	"coordination.mode": true, "coordination.state": true,

	"network.io.direction": true, "network.transport": true, "network.type": true,
	"network.protocol.name": true, "source.port": true, "destination.port": true,
	"os.type": true, "os.version": true, "tailscale.traffic_type": true,
	"tailscale.dst.service": true, "tailscale.tags": true, "tailscale.collector": true,
	"tailscale.feature": true, "metric.name": true, "component": true, "dedup.set": true,
	"source": true, "signal": true, "outcome": true, "operation": true, "receiver": true,
	"metric.group": true, "cpu.mode": true,
	"tailscale.webhook.type": true, "reason": true, "type": true, "status": true, "field": true,
	"limit":       true,
	"field_class": true, "state": true, "trust": true, "consistency": true,
	"tailscale.reporter.trust": true, "tailscale.reporter.consistency": true,
	"scope": true, "tailscale.logstream.type": true,
	"tailscale.webhook.key.expiration": true, "tailscale.webhook.old_roles": true, "tailscale.webhook.new_roles": true,
	"tailscale.contact.type": true, "tailscale.posture.provider": true, "tailscale.setting.name": true,
	"tailscale.setting.role": true, "tailscale.webhook_endpoint.provider": true,
	"tailscale.dns.resolver.kind": true, "tailscale.dns.resolver.use_with_exit_node": true,
	"tailscale.acl.section": true, "tailscale.acl.position": true, "tailscale.acl.autoapprover_kind": true,
	"tailscale.acl.etag": true, "tailscale.acl.risk_class": true,
	"tailscale.key.type": true, "tailscale.key.auth_kind": true, "tailscale.key.revoked": true,
	"tailscale.key.invalid": true, "tailscale.key.expires_in_seconds": true,
	"tailscale.user.role": true, "tailscale.user.status": true, "tailscale.user.type": true,
	"tailscale.user_invite.role": true, "tailscale.user_invite.accepted": true,
	"tailscale.user_invite.id": true, "tailscale.lifecycle.transition": true,
	"tailscale.authorized": true, "tailscale.external": true, "tailscale.derp.region": true,
	// The numeric DERP region a relayed flow was carried through. Tailscale's DERP
	// relays are shared public infrastructure, so the ID names a piece of their
	// estate, not anything about this tailnet — the same non-identifier reasoning
	// as tailscale.derp.region above, which is the region NAME from a different API.
	"tailscale.derp.region_id": true,
	"tailscale.derp.preferred": true, "tailscale.device_invite.accepted": true,
	"tailscale.device_invite.allow_exit_node": true, "tailscale.device_invite.multi_use": true,
	"tailscale.client_version": true, "tailscale.tag": true, "tailscale.connectivity.capability": true,
	"tailscale.exit_node.state": true, "tailscale.exit_node.enabled": true,
	"os": true, "os_version": true, "ts_version": true, "auto_update": true, "encrypted": true, "track": true,
	"tailscale.service.approval": true, "tailscale.service.configured": true,
	"tailscale.audit.action": true, "tailscale.audit.origin": true, "tailscale.audit.change": true,
	"tailscale.audit.type": true, "tailscale.audit.deferred_at": true,
	// Device inventory change records use a closed transition and field
	// vocabulary; identity and before/after values have their own
	// classifications above and remain subject to pii_filter.
	"tailscale.device.change": true, "tailscale.device.field": true,
	"tailscale.actor.tags": true, "tailscale.target.ephemeral": true,
	"tailscale.actor.type": true, "tailscale.target.type": true, "tailscale.target.property": true,
	"tailscale.tx.bytes": true, "tailscale.rx.bytes": true, "tailscale.tx.packets": true,
	"tailscale.rx.packets": true, "tailscale.connections": true, "error.type": true,
	// Flow capture-window bounds: RFC3339 timestamps describing when traffic was
	// observed. They identify a time range, never a person or a device.
	"tailscale.flow.window.start": true, "tailscale.flow.window.end": true,
	// Flow endpoint tags and OS. Tags are operator-assigned role labels
	// ("tag:servers") and OS is a platform name — neither identifies a person or
	// a specific device. The matching .user keys ARE identifiers and are
	// classified as CatEmails above.
	"tailscale.src.tags": true, "tailscale.dst.tags": true,
	"tailscale.src.os": true, "tailscale.dst.os": true,
	"go.version": true, "version": true, "http.response.status_code": true, "attribute": true,
	"check":            true, // bounded operator-defined posture compliance check name (max 32 unique values)
	"tailscale.key.id": true, "tailscale.posture.integration": true,
	"tailscale.webhook_endpoint.id": true, "tailscale.audit.event_group_id": true,
	"tailscale.target.id":                        true,
	"category":                                   true, // self-obs tailscale2otel.pii_filter.category attr
	"tailscale.key.scope_values":                 true, // J-A1: OAuth capability strings (not PII)
	"tailscale.key.tags":                         true, // #165: auto-applied ACL tags (same class as tailscale.tags)
	"tailscale.oauth_app.id":                     true, // #167: opaque app ID (like tailscale.key.id)
	"tailscale.oauth_app.scope_values":           true, // #167: OAuth capability strings (like key.scope_values)
	"tailscale.oauth_app.node_attribute_count":   true, // #167: numeric count
	"tailscale.device.key_expires_in_days":       true, // J-B5: numeric days-to-expiry
	"tailscale.device.attribute_expires_in_days": true, // #164: numeric days-to-expiry (attribute analog of key_expires_in_days)
	"result":                true, // rdns cache: hit/miss/negative/success/failure
	"tailscale.health.type": true, // #171: tailscaled health-message type (code-defined enum)
	"tailscale.path":        true, // #171: folded traffic path (direct|derp|peer_relay|other)
	"tailscale.drop.reason": true, // #171: bounded drop-reason admit-set (acl|error|other)

	// Shared config snapshots: kind/reason/revision/emission identity and
	// chunking metadata are generated by internal/snapshot. Revisions may be an
	// opaque upstream ETag or a content hash, but none of these values identifies
	// a person or endpoint; the snapshot body is governed by each source's
	// explicit opt-in separately.
	"tailscale.snapshot.kind":        true,
	"tailscale.snapshot.reason":      true,
	"tailscale.snapshot.revision":    true,
	"tailscale.snapshot.emission_id": true,
	"tailscale.snapshot.bytes":       true,
	"tailscale.snapshot.seq":         true,
	"tailscale.snapshot.total":       true,

	// Span-only keys (#212) — see the span block in keyCategory above for the
	// identifier-bearing ones. Everything here is a method, a count, a duration or
	// a code-defined enum.
	"server.address":               true, // control-plane API host (api.tailscale.com), not a tailnet identifier
	"http.request.method":          true, // GET/POST/…
	"http.request.resend_count":    true, // numeric retry count
	"http.request.body.size":       true, // numeric byte count (stream + webhook receivers)
	"tailscale.rate_limit.wait_ms": true, // numeric limiter wait
	"tailscale2otel.provider":      true, // control plane: tailscale|headscale (const span/metric attr)
	"tailscale.stream.flows":       true, // numeric record counts on the receiver span
	"tailscale.stream.audits":      true,
	"tailscale.stream.skipped":     true,
	"tailscale.webhook.events":     true, // numeric event count on the webhook span
	"exception.type":               true, // Go error type name recorded by RecordError
	"attempt":                      true, // retry span-event: numeric attempt index
	"sleep_ms":                     true, // retry span-event: numeric backoff

	// EPIC-03 (#479) — API and collector fidelity. Every key below is a
	// code-defined enum, a bounded upstream vocabulary, or a numeric count, so
	// none of them are identifiers. They are registered here up front, as one
	// frozen seam, so the parallel implementation lanes never have to edit this
	// shared file (a key missing here fails TestEveryCatalogAttributeIsClassified).

	// Per-operation API availability (#420/#421/#425/#430). See internal/apistate.
	// Mostly an upstream OpenAPI operationId from the vendored spec, but NOT
	// exclusively (#524): a per-entity subrequest records under its bounded
	// subrequest name instead ("device_posture", "device_invites",
	// "user_invites"), because internal/app/capability.go joins a subrequest
	// matrix row on operation == subrequest name; and the two collectors that do
	// not call the control plane at all use a local probe name
	// ("scrapeNodeMetrics" for tailscaled's :5252, "listObjects" for the S3
	// backend). Every one of those is a compile-time constant, so the set stays
	// closed and non-identifying either way.
	"tailscale.api.operation": true,
	"tailscale.api.state":     true, // apistate.State enum
	"tailscale.subrequest":    true, // bounded per-entity subrequest type
	"tailscale.capability":    true, // enabled-collector capability name on the config→capability matrix

	// Peer-relay dimensions restored in #429. Bounded admit-sets folded from
	// scraped tailscaled labels; they describe transport and connection state,
	// never who is connected.
	"tailscale.peer_relay.transport_in":  true,
	"tailscale.peer_relay.transport_out": true,
	"tailscale.peer_relay.state":         true,

	// Invite delivery state (#412/#413): emailed | manual_link | unknown. User
	// invite emails and bearer inviteUrl values remain excluded entirely. Device
	// invite email can appear on its separately PII-gated log, while its bearer
	// inviteUrl is never decoded. These keys carry only HOW an invite was
	// delivered, never to whom.
	"tailscale.user_invite.delivery":   true,
	"tailscale.device_invite.delivery": true,

	// Linux distribution inventory (#427). Bounded platform names from the
	// device's distro block — the same class as os.type/os.version above.
	"tailscale.distro.name":     true,
	"tailscale.distro.codename": true,

	// Credential privilege classification (#415/#416/#419): tsscope.Class and the
	// tag-authority class. These are the bounded REPLACEMENTS for reasoning about
	// a raw scope count; the exact scope strings stay on log events only.
	"tailscale.key.scope_class":       true,
	"tailscale.key.tag_scope":         true,
	"tailscale.oauth_app.scope_class": true,

	// Webhook event-category coverage (#417). The 18-value subscription enum from
	// the vendored spec, unknown values folded to "other".
	"tailscale.webhook.event": true,

	// ACL policy validation (#428): error | warning | test_failure. The validator's
	// free-text messages are deliberately NOT emitted — only the bounded kind.
	"tailscale.acl.validation.kind": true,
}

// categoryForIPClass maps an ipClass to the toggle category.
func categoryForIPClass(c ipClass) (Category, bool) {
	switch c {
	case ipTailscale:
		return CatTailscaleIPs, true
	case ipInternal:
		return CatInternalIPs, true
	case ipExternal:
		return CatExternalIPs, true
	default:
		return "", false
	}
}

// IsClassified reports whether key is known to the redaction registry (any bucket).
// Used by the catalog-driven completeness guard in a later task.
func IsClassified(key string) bool {
	if _, ok := keyCategory[key]; ok {
		return true
	}
	return ipValueKeys[key] || nonIdentifier[key] || geoNonIdentifier[key]
}
