package audit

import (
	"strings"
	"time"
)

// crossKeyBucket is the time granularity at which an audit event and a webhook
// event describing the same change are treated as coincident. It is a
// best-effort knob: a webhook's delivery timestamp and an audit log's eventTime
// for the same change may differ, so this bucket (and the type<->action mapping
// on the webhook side) likely needs tuning against a live capture of overlapping
// events before cross-source dedup suppresses reliably. See internal/webhook.
const crossKeyBucket = time.Second

// NormalizedCrossKey is the SINGLE HOME for the normalized cross-source
// de-duplication key shared by the audit Processor and the webhook Server. A
// webhook event carries no eventGroupID, so the two sources are reconciled on a
// canonical (verb, subjectType, subjectID) tuple plus the event time bucketed to
// crossKeyBucket. Any empty component yields ok=false so the caller skips
// cross-dedup rather than risk collapsing unrelated events.
func NormalizedCrossKey(verb, subjectType, subjectID string, t time.Time) (string, bool) {
	if verb == "" || subjectType == "" || subjectID == "" {
		return "", false
	}
	return "xsrc|" + verb + "|" + subjectType + "|" + subjectID + "|" +
		t.UTC().Truncate(crossKeyBucket).Format(time.RFC3339), true
}

// CrossSourceKey derives the normalized cross-source key for an audit event by
// lowercasing its Action and Target.Type into the shared vocabulary and using
// the Target.ID as the subject. It returns ok=false when the event lacks the
// action, target type, or target id needed to key it.
func CrossSourceKey(ev Event) (string, bool) {
	return NormalizedCrossKey(strings.ToLower(ev.Action), strings.ToLower(ev.Target.Type), ev.Target.ID, ev.EventTime)
}
