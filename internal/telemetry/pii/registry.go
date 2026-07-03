package pii

// keyCategory maps a fixed-meaning attribute key to its category.
var keyCategory = map[string]Category{
	"tailscale.user":                   CatEmails,
	"tailscale.user.login":             CatEmails,
	"tailscale.actor.login":            CatEmails,
	"tailscale.actor.display":          CatUserDisplayNames,
	"enduser.id":                       CatUserIDs,
	"host.name":                        CatHostnames,
	"tailscale.node.hostname":          CatHostnames,
	"host.id":                          CatNodeIDs,
	"tailscale.node.id":                CatNodeIDs,
	"tailscale.service.name":           CatServiceAddrs,
	"endpoint":                         CatEndpointPaths,
	"tailscale.route.cidr":             CatNetworkTopology,
	"tailscale.dns.resolver.domain":    CatNetworkTopology,
	"tailscale.dns.search_path.domain": CatNetworkTopology, // J-A3 search-path info gauge
	"tailscale.tailnet":                CatTailnetName,
	"tailscale.audit.old":              CatFreeTextDetails,
	"tailscale.audit.new":              CatFreeTextDetails,
	"tailscale.audit.details":          CatFreeTextDetails,
	"error":                            CatFreeTextDetails,
	"tailscale.target.name":            CatFreeTextDetails,
	"tailscale.key.description":        CatFreeTextDetails,
	"value":                            CatFreeTextDetails,
	"tailscale.acl.rule":               CatFreeTextDetails, // J-B1 risky-rule src/dst contents
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
	"enduser.id":                       true, // #74: per-user gauge identity (CatUserIDs)
	"tailscale.user.login":             true, // #74: per-user gauge identity (CatEmails)
}

// nonIdentifier is the explicit allowlist of keys that are never PII/identifiers.
var nonIdentifier = map[string]bool{
	"network.io.direction": true, "network.transport": true, "network.type": true,
	"network.protocol.name": true, "source.port": true, "destination.port": true,
	"os.type": true, "os.version": true, "tailscale.traffic_type": true,
	"tailscale.dst.service": true, "tailscale.tags": true, "tailscale.collector": true,
	"tailscale.feature": true, "metric.name": true, "component": true, "dedup.set": true,
	"source": true, "signal": true, "outcome": true, "metric.group": true, "cpu.mode": true,
	"tailscale.webhook.type": true, "reason": true, "type": true, "tailscale.logstream.type": true,
	"tailscale.contact.type": true, "tailscale.posture.provider": true, "tailscale.setting.name": true,
	"tailscale.setting.role": true, "tailscale.webhook_endpoint.provider": true,
	"tailscale.dns.resolver.kind": true, "tailscale.dns.resolver.use_with_exit_node": true,
	"tailscale.acl.section": true, "tailscale.acl.position": true, "tailscale.acl.autoapprover_kind": true,
	"tailscale.key.type": true, "tailscale.key.auth_kind": true, "tailscale.key.revoked": true,
	"tailscale.key.invalid": true, "tailscale.key.expires_in_seconds": true,
	"tailscale.user.role": true, "tailscale.user.status": true, "tailscale.user.type": true,
	"tailscale.user_invite.role": true, "tailscale.user_invite.accepted": true,
	"tailscale.authorized": true, "tailscale.external": true, "tailscale.derp.region": true,
	"tailscale.derp.preferred": true, "tailscale.device_invite.accepted": true,
	"tailscale.device_invite.allow_exit_node": true, "tailscale.device_invite.multi_use": true,
	"tailscale.client_version": true, "tailscale.tag": true, "tailscale.connectivity.capability": true,
	"tailscale.exit_node.state": true, "tailscale.exit_node.enabled": true,
	"os": true, "os_version": true, "ts_version": true, "auto_update": true, "encrypted": true, "track": true,
	"tailscale.service.approval": true, "tailscale.service.configured": true,
	"tailscale.audit.action": true, "tailscale.audit.origin": true, "tailscale.audit.change": true,
	"tailscale.actor.type": true, "tailscale.target.type": true, "tailscale.target.property": true,
	"tailscale.tx.bytes": true, "tailscale.rx.bytes": true, "tailscale.tx.packets": true,
	"tailscale.rx.packets": true, "tailscale.connections": true, "error.type": true,
	"go.version": true, "http.response.status_code": true, "attribute": true,
	"tailscale.key.id": true, "tailscale.posture.integration": true,
	"tailscale.webhook_endpoint.id": true, "tailscale.audit.event_group_id": true,
	"tailscale.target.id":                  true,
	"category":                             true, // self-obs tailscale2otel.pii_filter.category attr
	"tailscale.key.scope_values":           true, // J-A1: OAuth capability strings (not PII)
	"tailscale.device.key_expires_in_days": true, // J-B5: numeric days-to-expiry
	"result":                               true, // rdns cache: hit/miss/negative/success/failure
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
	return ipValueKeys[key] || nonIdentifier[key]
}
