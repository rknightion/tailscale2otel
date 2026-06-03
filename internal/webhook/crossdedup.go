package webhook

import "github.com/rknightion/tailscale2otel/internal/audit"

// webhookActionMap maps the Tailscale webhook event types that overlap with
// configuration-audit events onto the SAME canonical (verb, subject) vocabulary
// the audit side derives from its Action + Target.Type (see
// audit.CrossSourceKey). Only confidently-overlapping types are listed; an
// unmapped type yields no cross-source key (ok=false) so its events are never
// suppressed.
//
// BEST-EFFORT: this mapping and the subjectIDKeys list below are derived from the
// documented webhook payload, NOT from a live capture of overlapping webhook +
// audit events. They likely need empirical tuning — a webhook node id may not be
// byte-identical to an audit Target.ID, and a webhook delivery timestamp may
// differ from the audit eventTime by more than crossKeyBucket — before
// cross-source dedup actually suppresses in production. Until then it is
// harmless: a non-matching key simply does not dedup. (S4-10 live capture.)
var webhookActionMap = map[string]struct{ verb, subject string }{
	"nodeCreated":     {"create", "node"},
	"nodeDeleted":     {"delete", "node"},
	"nodeApproved":    {"update", "node"},
	"userCreated":     {"create", "user"},
	"userDeleted":     {"delete", "user"},
	"userApproved":    {"update", "user"},
	"userSuspended":   {"update", "user"},
	"userRoleUpdated": {"update", "user"},
}

// subjectIDKeys are the webhook event Data keys consulted, in order, for the
// subject identity (a node or user id) used in the cross-source key.
var subjectIDKeys = []string{"nodeID", "nodeId", "deviceID", "deviceId", "userID", "userId", "id", "loginName", "email"}

// crossKey derives the normalized cross-source de-dup key for a webhook event,
// reusing audit.NormalizedCrossKey so both sources share one key format. It
// returns ok=false when the type is not in webhookActionMap or no subject id is
// present, in which case the event is never cross-deduped.
func crossKey(ev event) (string, bool) {
	va, ok := webhookActionMap[ev.Type]
	if !ok {
		return "", false
	}
	id := subjectID(ev)
	if id == "" {
		return "", false
	}
	return audit.NormalizedCrossKey(va.verb, va.subject, id, parseTimestamp(ev.Timestamp))
}

// subjectID returns the first non-empty Data value among subjectIDKeys.
func subjectID(ev event) string {
	for _, k := range subjectIDKeys {
		if v := ev.Data[k]; v != "" {
			return v
		}
	}
	return ""
}
