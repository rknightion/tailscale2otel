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

// deviceChurnActions are the NODE actions that count as device churn (B7).
var deviceChurnActions = map[string]bool{"CREATE": true, "DELETE": true, "EXPIRED": true}

// apiKeyActions are the API_KEY lifecycle actions worth a curated counter.
var apiKeyActions = map[string]bool{"CREATE": true, "DELETE": true, "REVOKE": true}

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
