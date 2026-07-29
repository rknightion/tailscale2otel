package annotations

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/audit"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// Rule is one curated annotatable event: which emitted signal it reads, which
// records of that signal qualify, what makes two of them distinct occurrences,
// and exactly which attributes may reach the annotation text.
//
// The set is CLOSED and lives in Rules(). Adding one is a deliberate product
// decision about dashboard readability, not a drive-by.
//
// # There is no state/event distinction here, deliberately
//
// A snapshot collector re-emits its whole current set on every tick, so "a
// record arrived" does not mean "something happened". The usual answer is a
// silent priming pass over the first observed set — but that needs a per-run
// emitter to hang the run boundary on, and this process builds ONE emitter per
// tailnet for its whole lifetime (see internal/app, `emitter := tp.Emitter()`).
//
// So instead, a re-emitted record is made to derive the SAME dedupe key every
// tick, by giving each rule an Identity that names what actually makes an
// occurrence distinct. Re-emission then dedupes for free, a cold start
// publishes each currently-true condition exactly once — the honest answer
// rather than a swallowed one — and the resulting first-run burst is absorbed
// by the category's rollup and the publisher's local ceiling.
type Rule struct {
	// ID is the stable rule identity. It is part of every dedupe key and is
	// published as a `rule:` tag, so renaming one republishes recent
	// annotations and breaks any dashboard query filtering on it. Frozen.
	ID string
	// Category gates the rule and decides whether it rolls up.
	Category Category
	// EventName is the OTEL event name of the source signal.
	EventName string
	// Match reports whether this record qualifies. It reads the RAW attributes
	// (see Recorder) and must never panic on a missing or unexpectedly-typed
	// one: an unmatched record is simply not annotated, and no record is ever
	// dropped from the telemetry pipeline by anything in this package.
	Match func(attrs telemetry.Attrs) bool
	// Identity returns the values forming this occurrence's source identity,
	// which DedupeKey hashes. It reads the RAW attributes — the values are
	// hashed and never published — and takes the event time because some
	// sources carry only a REMAINING duration, from which the fixed instant
	// that identifies the occurrence has to be reconstructed.
	//
	// It must be enough to tell two real occurrences apart and no more.
	// Including a value that changes on re-delivery (a poll timestamp, a
	// countdown) defeats dedupe entirely and annotates on every tick forever.
	Identity func(attrs telemetry.Attrs, eventTime time.Time) []string
	// Detail is the ALLOW-LIST of attribute keys rendered into the annotation
	// text, in order. Anything not named here never reaches Grafana, so a field
	// added to a source record later cannot silently ride out.
	Detail []string
	// Title renders the short human label used as the text prefix and in a
	// rolled-up summary. It reads the REDACTED attributes, because its output
	// is published.
	Title func(attrs telemetry.Attrs) string
	// SeverityAttr, when set, names the attribute whose value becomes the
	// `severity:` tag. It must be a bounded value set.
	SeverityAttr string
}

// Frozen rule IDs. Each is part of a dedupe key and a published `rule:` tag.
const (
	// RuleConfigChange is the curated subset of the Tailscale configuration
	// audit log.
	RuleConfigChange = "tailscale.config_change"
	// RuleKeyExpiring is an auth key / API key entering its warning window.
	RuleKeyExpiring = "tailscale.key_expiring"
	// RuleDeviceKeyExpiring is a device node key entering its warning window.
	RuleDeviceKeyExpiring = "tailscale.device_key_expiring"
	// RuleStartup is the process lifecycle marker, written by preflight rather
	// than derived from any record.
	RuleStartup = "tailscale2otel.startup"
)

// Source event names this package reads. They are the names the collectors
// declare in their own catalogs; a rename there without a rename here silently
// stops annotating, which is why annotations_test.go checks every EventName
// against internal/catalog's declared log events.
const (
	eventConfigAudit     = "tailscale.config.audit"
	eventKeyExpiring     = "tailscale.key.expiring"
	eventDeviceKeyExpiry = "tailscale.device.key_expiring"
)

// Audit record attribute keys read by the config-change rule. Mirrors the
// constants in internal/audit's processor, which are unexported there.
const (
	attrAuditAction      = "tailscale.audit.action"
	attrAuditOrigin      = "tailscale.audit.origin"
	attrAuditEventGroup  = "tailscale.audit.event_group_id"
	attrAuditOld         = "tailscale.audit.old"
	attrAuditNew         = "tailscale.audit.new"
	attrAuditActorType   = "tailscale.actor.type"
	attrTargetID         = "tailscale.target.id"
	attrTargetName       = "tailscale.target.name"
	attrTargetType       = "tailscale.target.type"
	attrTargetProperty   = "tailscale.target.property"
	attrKeyID            = "tailscale.key.id"
	attrKeyType          = "tailscale.key.type"
	attrKeyAuthKind      = "tailscale.key.auth_kind"
	attrKeyDescription   = "tailscale.key.description"
	attrKeyExpiresIn     = "tailscale.key.expires_in_seconds"
	attrKeyOwner         = "tailscale.key.owner"
	attrDeviceExpiryDays = "tailscale.device.key_expires_in_days"
)

// Rules returns the curated, closed rule set.
//
// # Why these three and not more
//
// The categories were selected on #518. Two candidate sources were considered
// and REJECTED, and the reasons are here rather than in a commit message
// because "it is missing" and "it was excluded" look identical from outside:
//
//   - tailscale.acl.risky_rule / tailscale.acl.validation_issue /
//     tailscale.device.tailnet_lock_error describe a STANDING posture, not a
//     point in time. They are re-emitted for as long as the condition holds, so
//     annotating them draws a picket fence across the dashboard rather than
//     marking a moment. A posture regression wants an alert, which the repo
//     already ships.
//   - tailscale.network.flow is per-connection, and annotating traffic would
//     bury every real marker under thousands of them.
func Rules() []Rule {
	return []Rule{
		{
			ID:        RuleConfigChange,
			Category:  CategoryConfigChange,
			EventName: eventConfigAudit,
			// The curated vocabulary is internal/audit's, reached through the
			// one exported wrapper, so there is a single allow-list rather than
			// a second copy that drifts. Everything outside it is routine audit
			// traffic (node tag churn, machine renames, posture self-reports)
			// that would drown the useful markers.
			Match: func(a telemetry.Attrs) bool {
				_, ok := audit.ChangeCategory(
					attrString(a, attrTargetProperty),
					attrString(a, attrTargetType),
					attrString(a, attrAuditAction),
				)
				return ok
			},
			// The audit API gives no per-record id — eventGroupID is shared by
			// every change in one group — so identity is the group plus what
			// was changed plus the instant it happened. The event time is safe
			// to include here, and necessary: it is a property of the record
			// (preserved on re-delivery), and without it two identical changes
			// an hour apart would collapse into one marker.
			Identity: func(a telemetry.Attrs, eventTime time.Time) []string {
				return []string{
					attrString(a, attrAuditEventGroup),
					attrString(a, attrTargetID),
					attrString(a, attrTargetProperty),
					attrString(a, attrAuditAction),
					strconv.FormatInt(eventTime.UTC().UnixMilli(), 10),
				}
			},
			Detail: []string{
				attrAuditAction, attrTargetType, attrTargetName, attrAuditOrigin,
				attrAuditActorType, semconv.AttrUserName, attrAuditOld, attrAuditNew,
			},
			Title: func(a telemetry.Attrs) string {
				category, _ := audit.ChangeCategory(
					attrString(a, attrTargetProperty),
					attrString(a, attrTargetType),
					attrString(a, attrAuditAction),
				)
				return strings.TrimSpace(category + " " + strings.ToLower(attrString(a, attrAuditAction)))
			},
		},
		{
			ID:        RuleKeyExpiring,
			Category:  CategoryExpiry,
			EventName: eventKeyExpiring,
			// The collector emits this only inside the configured expiry_warn
			// window, so arriving at all is the qualification.
			Match: nil,
			// expires_in_seconds is a COUNTDOWN: it shrinks on every poll, so
			// using it directly would mint a new identity per tick and annotate
			// forever. Adding it back to the event time reconstructs the fixed
			// expiry instant, which is what actually identifies the occurrence
			// — and bucketing to the hour absorbs poll jitter without merging
			// two genuinely different expiries.
			Identity: func(a telemetry.Attrs, eventTime time.Time) []string {
				return []string{
					attrString(a, attrKeyID),
					expiryBucket(eventTime, attrSeconds(a, attrKeyExpiresIn), time.Hour),
				}
			},
			Detail: []string{
				attrKeyType, attrKeyAuthKind, attrKeyDescription, attrKeyOwner, attrKeyExpiresIn,
			},
			Title: func(a telemetry.Attrs) string {
				kind := attrString(a, attrKeyType)
				if kind == "" {
					kind = "key"
				}
				return kind + " expiring"
			},
		},
		{
			ID:        RuleDeviceKeyExpiring,
			Category:  CategoryExpiry,
			EventName: eventDeviceKeyExpiry,
			Match:     nil,
			// Same countdown reconstruction as the key rule, in days rather
			// than seconds, bucketed to the day: the source itself is only
			// two-decimal-day precision, so an hour bucket would split one
			// expiry across buckets as the value is recomputed.
			Identity: func(a telemetry.Attrs, eventTime time.Time) []string {
				days := attrFloat(a, attrDeviceExpiryDays)
				return []string{
					attrString(a, semconv.HostID),
					expiryBucket(eventTime, days*24*float64(time.Hour), 24*time.Hour),
				}
			},
			Detail: []string{semconv.HostName, attrDeviceExpiryDays},
			Title: func(a telemetry.Attrs) string {
				host := attrString(a, semconv.HostName)
				if host == "" {
					host = "device"
				}
				return host + " node key expiring"
			},
		},
	}
}

// expiryBucket reconstructs the fixed instant a countdown points at and
// truncates it to bucket, so the same expiry observed on successive polls
// renders the same string. Returned as an RFC3339 instant rather than a raw
// number because it goes into a hash whose components are length-prefixed
// strings, and a stable human-readable form makes a mismatched key debuggable.
//
// A non-positive or absent countdown yields "" rather than a bogus instant:
// identity then rests on the entity id alone, which over-dedupes (one marker
// per key until the retention window drops it) rather than under-dedupes (a
// marker every poll, forever). Of the two failure modes only one is visible to
// an operator as spam.
func expiryBucket(eventTime time.Time, remaining float64, bucket time.Duration) string {
	if remaining <= 0 {
		return ""
	}
	return eventTime.Add(time.Duration(remaining)).UTC().Truncate(bucket).Format(time.RFC3339)
}

// attrString renders one attribute value as a string for matching and for text.
// telemetry.Attrs is map[string]any over a documented value set (string, bool,
// int, int64, float64, []string), so this covers all of it; anything else
// renders with %v rather than panicking, because a rule must never be able to
// take the process down.
func attrString(attrs telemetry.Attrs, key string) string {
	v, ok := attrs[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []string:
		return strings.Join(t, ",")
	default:
		return fmt.Sprintf("%v", t)
	}
}

// attrFloat reads a numeric attribute, tolerating the string form the
// collectors use for formatted values. A missing or unparseable value reads as
// 0, which expiryBucket treats as "no usable countdown".
func attrFloat(attrs telemetry.Attrs, key string) float64 {
	switch t := attrs[key].(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

// attrSeconds reads a numeric attribute expressed in seconds and returns it as
// a float count of nanoseconds, ready to add to a time.Time.
func attrSeconds(attrs telemetry.Attrs, key string) float64 {
	return attrFloat(attrs, key) * float64(time.Second)
}

// renderText builds the annotation body from the rule's title plus its
// allow-listed detail attributes. Empty values are skipped so the text does not
// carry a wall of "key=" for attributes this record happens not to have — which
// is also what a redacted-away attribute looks like, so a suppressed category
// leaves no trace rather than an empty placeholder naming it.
func renderText(rule Rule, attrs telemetry.Attrs) string {
	var b strings.Builder
	title := strings.TrimSpace(rule.Title(attrs))
	if title == "" {
		title = rule.ID
	}
	b.WriteString(title)
	for _, key := range rule.Detail {
		value := strings.TrimSpace(attrString(attrs, key))
		if value == "" {
			continue
		}
		_, _ = fmt.Fprintf(&b, " | %s=%s", key, value)
	}
	return b.String()
}
