package webhook

import (
	"encoding/json"

	"github.com/rknightion/tailscale2otel/internal/audit"
)

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
	"nodeCreated":    {"create", "node"},
	"nodeDeleted":    {"delete", "node"},
	"nodeApproved":   {"update", "node"},
	"nodeAuthorized": {"update", "node"}, // deprecated alias of nodeApproved (kb/1213)
	"userCreated":    {"create", "user"},
	"userDeleted":    {"delete", "user"},
}

// NOTE on deliberately-UNMAPPED types (S4-11c, after the kb/1213 catalog + live
// findings + the session-5 adversarial review). D11: cross-source dedup must
// NEVER over-suppress; default to no-dedup when uncertain.
//   - userApproved / userSuspended / userRoleUpdated are THREE INDEPENDENT user
//     changes that would all collapse to the same (update, user) cross-key. Once
//     subjectID resolves the "user" field, the same user in the same time bucket
//     would silently drop two of three distinct events — an over-suppression. They
//     are also non-viable cross-SOURCE: an audit USER Target.ID is an internal id
//     (…CNTRL form), not the login/email the webhook "user" field carries, so they
//     can never byte-match the audit side anyway. Left unmapped.
//   - policyUpdate carries NO node/user id ({newPolicy,oldPolicy,url,actor}) → an
//     id-keyed cross-key is impossible (inert).
//   - userNeedsApproval shows null data in the catalog (no subject id).
//   - nodeNeedsApproval / nodeNeedsAuthorization are "needs-action" notifications
//     with NO config-audit counterpart → mapping them risks suppressing a distinct
//     same-second nodeCreated/nodeApproved.
// userCreated/userDeleted are kept (distinct create/delete verbs → no cross-type
// collision); in practice their catalog data is null so they stay inert.

// subjectIDKeys are the webhook event Data keys consulted, in order, for the
// subject identity used in the cross-source key. Per kb/1213 the only id fields
// that actually appear are "nodeID" (node* events) and "user" (the user
// login/email on user* events) — userID/loginName/email/deviceID never appear
// (S4-11d).
var subjectIDKeys = []string{"nodeID", "user"}

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

// subjectID returns the first Data value among subjectIDKeys that decodes to a
// non-empty JSON string. Non-string values (e.g. arrays) are skipped, so a
// subject id is only used when it is unambiguously a scalar identifier.
func subjectID(ev event) string {
	for _, k := range subjectIDKeys {
		raw, ok := ev.Data[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}
