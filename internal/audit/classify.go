package audit

// propertyCategories maps a curated, security/lifecycle-relevant
// Target.Property to a stable, bounded change-category label. Membership here is
// the contract: only these properties produce a tailscale.config.audit.changes
// counter increment. POSTURE_IDENTITY (node self-report noise) is intentionally
// absent; only the admin toggle COLLECT_POSTURE_IDENTITY is curated.
var propertyCategories = map[string]string{
	"ACL":                      "acl",
	"KEY_EXPIRY":               "key_expiry",
	"KEY_EXPIRY_TIME":          "key_expiry",
	"EXIT_NODE":                "exit_node",
	"TKA":                      "tailnet_lock",
	"USER_ROLE":                "user_role",
	"POSTURE_INTEGRATION":      "posture_integration",
	"COLLECT_POSTURE_IDENTITY": "collect_posture_identity",
	"MAGIC_DNS":                "magic_dns",
	"DNS_CONFIG":               "dns_config",
	"LOGSTREAM_ENDPOINT":       "logstream_endpoint",
	"NODE_SHARE":               "node_share",
	"TAILNET_INVITE":           "tailnet_invite",
	"AUTH_PROVIDER":            "auth_provider",
	"SECRET":                   "secret",
}

// knownOrigins is the complete set of origin values defined in the Tailscale
// audit-log API (OpenAPI spec ConfigurationAuditLog.origin enum). Unknown
// values fold to "other" so the tailscale.config.audit.events metric's
// bounded-cardinality guarantee holds even when the API introduces a new
// origin, and — since Process is shared by the polling collector and the
// streaming receiver — even when a misbehaving/attacking stream source sends
// an arbitrary wire-chosen origin string (#77).
//
// Source: Tailscale OpenAPI spec §ConfigurationAuditLog.origin enum.
var knownOrigins = map[string]bool{
	"ADMIN_CONSOLE": true, "CONFIG_API": true, "CONTROL": true,
	"IDENTITY_PROVIDER": true, "NODE": true, "SUPPORT_REQUEST": true,
	"STRIPE": true, "SECURITY_NOTIFICATION": true, "LEGAL_NOTIFICATION": true,
}

// normalizeOrigin returns the origin unchanged when it is a known API value,
// and "other" for anything unrecognized. This bounds the cardinality of the
// tailscale.config.audit.events origin label.
func normalizeOrigin(origin string) string {
	if knownOrigins[origin] {
		return origin
	}
	return "other"
}

// deviceChurnActions are the NODE actions that count as device churn (B7).
var deviceChurnActions = map[string]bool{"CREATE": true, "DELETE": true, "EXPIRED": true}

// apiKeyActions are the API_KEY lifecycle actions worth a curated counter.
var apiKeyActions = map[string]bool{"CREATE": true, "DELETE": true, "REVOKE": true}

// knownActions is the complete set of action values defined in the Tailscale
// audit-log API (OpenAPI spec action enum) plus the action values observed in
// this repo's test fixtures (changes_test.go, processor_test.go) and the
// classify.go curated sets above. Unknown values fold to "other" to keep the
// tailscale.config.audit.changes metric's bounded-cardinality guarantee true
// even when the API introduces a new verb before this file is updated.
//
// Sources: Tailscale OpenAPI spec §ConfigurationAuditLog.action enum;
// internal/audit tests; classify.go deviceChurnActions + apiKeyActions.
var knownActions = map[string]bool{
	"LOGIN": true, "LOGOUT": true, "CREATE": true, "UPDATE": true,
	"DELETE": true, "CANCEL": true, "REVOKE": true, "APPROVE": true,
	"SUSPEND": true, "RESTORE": true, "ENABLE": true, "DISABLE": true,
	"ACCEPT": true, "EXPIRED": true, "PUSH_USER": true, "PUSH_GROUP": true,
	"VERIFY": true, "JOIN_WAITLIST": true, "INVITE": true, "JOIN": true,
	"LEAVE": true, "RESEND": true, "MIGRATE_AUTH_PROVIDER": true,
}

// knownActorTypes is the complete set of actor.type values defined in the
// Tailscale audit-log API (OpenAPI spec actor.type enum) plus the values
// observed in this repo's test fixtures and the curated classify.go sets.
// Unknown values fold to "other" for the same bounded-cardinality reason.
//
// Sources: Tailscale OpenAPI spec §ConfigurationAuditLog.actor.type enum;
// internal/audit tests (USER, NODE, SECRET_SCANNER); classify.go.
var knownActorTypes = map[string]bool{
	"USER": true, "NODE": true, "AUTOMATED_WORKER": true,
	"OAUTH_CLIENT": true, "SCIM": true, "MULLVAD": true,
	"LOGSTREAM": true, "SECRET_SCANNER": true,
}

// normalizeAction returns the action unchanged when it is a known API value,
// and "other" for anything unrecognized. This bounds the cardinality of the
// tailscale.config.audit.changes action label even if the API adds new verbs.
func normalizeAction(action string) string {
	if knownActions[action] {
		return action
	}
	return "other"
}

// normalizeActorType returns the actor type unchanged when it is a known API
// value, and "other" for anything unrecognized. This bounds the cardinality of
// the tailscale.config.audit.changes actor-type label.
func normalizeActorType(actorType string) string {
	if knownActorTypes[actorType] {
		return actorType
	}
	return "other"
}

// classifyChange maps an audit Event to a curated, bounded change-category
// label. It returns ok=false for events outside the curated set (the common
// case — most audit events are routine and must not inflate the counter).
//
// Precedence: a curated Target.Property wins; only when the property is absent
// or uncurated do the type+action rules (device churn, API-key lifecycle)
// apply. So a NODE event whose property is KEY_EXPIRY is "key_expiry", never
// "device".
func classifyChange(ev Event) (string, bool) {
	if cat, ok := propertyCategories[ev.Target.Property]; ok {
		return cat, true
	}
	switch ev.Target.Type {
	case "NODE":
		if deviceChurnActions[ev.Action] {
			return "device", true
		}
	case "API_KEY":
		if apiKeyActions[ev.Action] {
			return "api_key", true
		}
	}
	return "", false
}
