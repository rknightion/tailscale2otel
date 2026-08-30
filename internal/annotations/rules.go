package annotations

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
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
	// RulePolicySnapshot marks the change edge of an ACL policy snapshot. The
	// snapshot emitter also emits heartbeat records; those are deliberately not
	// annotations because a heartbeat is not a policy change.
	RulePolicySnapshot = "tailscale.policy_snapshot"
	// RulePolicyDiff marks the unified diff emitted for a changed ACL policy.
	RulePolicyDiff = "tailscale.policy_diff"
	// RuleInventoryChange marks one change-driven device inventory record.
	RuleInventoryChange = "tailscale.device_inventory_change"
	// RuleRiskFinding marks one bounded ACL risk finding.
	RuleRiskFinding = "tailscale.acl.risk_finding"
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
	eventPolicySnapshot  = "tailscale.acl.policy_snapshot"
	eventPolicyDiff      = "tailscale.acl.policy_diff"
	eventInventoryChange = "tailscale.device.change"
	eventRiskyRule       = "tailscale.acl.risky_rule"
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

	// Snapshot attributes are the canonical shape from internal/snapshot. The
	// ACL etag is retained as a compatibility/detail field by the policy
	// collector and is also a useful fallback identity for hand-written emit
	// sites.
	attrSnapshotKind     = "tailscale.snapshot.kind"
	attrSnapshotReason   = "tailscale.snapshot.reason"
	attrSnapshotRevision = "tailscale.snapshot.revision"
	attrACLEtag          = "tailscale.acl.etag"

	// Device inventory change attributes are a deliberately small, bounded
	// contract. Names, users and tags remain details (and therefore continue to
	// pass through the configured PII redactor), never Grafana tags.
	attrDeviceChange = "tailscale.device.change"
	attrDeviceField  = "tailscale.device.field"

	// Risk attributes are emitted by the ACL risk pass. risk_class is a closed
	// enum; rule is free text and is redacted as configured before it reaches
	// annotation text.
	attrRiskClass   = "tailscale.acl.risk_class"
	attrRiskSection = "tailscale.acl.section"
	attrRiskRule    = "tailscale.acl.rule"
)

// Rules returns the curated, closed rule set.
//
// # Why these rules and not more
//
// The original categories were selected on #518. Wave 2 adds only sources that
// are change-driven (or have a stable revision identity), and the reasons are
// here rather than in a commit message because "it is missing" and "it was
// excluded" look identical from outside:
//
//   - tailscale.acl.validation_issue and tailscale.device.tailnet_lock_error
//     describe a STANDING posture, not a point in time. They are re-emitted for
//     as long as the condition holds, so annotating them draws a picket fence
//     across the dashboard rather than marking a moment. A posture regression
//     wants an alert, which the repo already ships.
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
			ID:        RulePolicySnapshot,
			Category:  CategoryPolicyChange,
			EventName: eventPolicySnapshot,
			// A snapshot heartbeat keeps Loki query results fresh but does not
			// represent a policy edit. Only the change edge gets an annotation.
			Match: func(a telemetry.Attrs) bool {
				return attrString(a, attrSnapshotReason) == "change"
			},
			// Chunks from one snapshot share a revision, emission id and event
			// timestamp. Revision is the source identity, so a large body still
			// produces one marker rather than one marker per chunk.
			Identity: func(a telemetry.Attrs, eventTime time.Time) []string {
				return revisionIdentity(a, eventTime)
			},
			Detail: []string{
				attrSnapshotKind, attrSnapshotReason, attrSnapshotRevision,
				attrACLEtag, "tailscale.snapshot.bytes",
			},
			Title: func(telemetry.Attrs) string { return "ACL policy changed" },
		},
		{
			ID:        RuleInventoryChange,
			Category:  CategoryInventory,
			EventName: eventInventoryChange,
			// The event is dedicated, but classify its bounded transition kind
			// explicitly so malformed/future values cannot create an unqueryable
			// annotation class.
			Match: func(a telemetry.Attrs) bool {
				switch attrString(a, attrDeviceChange) {
				case "added", "removed", "changed":
					return true
				default:
					return false
				}
			},
			// A source event timestamp is part of the identity: one device can
			// materially change more than once, while re-delivery of one record
			// retains its timestamp and therefore dedupes.
			Identity: func(a telemetry.Attrs, eventTime time.Time) []string {
				deviceID := attrString(a, semconv.HostID)
				if deviceID == "" {
					deviceID = attrString(a, semconv.HostName)
				}
				return []string{
					deviceID,
					attrString(a, attrDeviceChange),
					attrString(a, attrDeviceField),
					eventTime.UTC().Format(time.RFC3339Nano),
				}
			},
			Detail: []string{
				attrDeviceChange, attrDeviceField,
				semconv.HostName, semconv.HostID, semconv.OSType, semconv.OSVersion,
				semconv.AttrUser, semconv.AttrTags,
			},
			Title: func(a telemetry.Attrs) string {
				kind := attrString(a, attrDeviceChange)
				if kind == "" {
					kind = "changed"
				}
				return "device " + kind
			},
		},
		{
			ID:        RuleRiskFinding,
			Category:  CategoryRisk,
			EventName: eventRiskyRule,
			// Risk class is a bounded classifier emitted by the risk pass. An
			// unknown value is not a useful marker and must not silently become
			// a new unbounded tag/detail vocabulary.
			Match: func(a telemetry.Attrs) bool {
				switch attrString(a, attrRiskClass) {
				case "unrestricted", "ssh_wildcard", "autoapprover_wildcard":
					return true
				default:
					return false
				}
			},
			// section + class + rule identify one finding. The producer emits
			// findings only on a policy revision change, and retaining this
			// identity suppresses a finding that remains present on later
			// observations instead of drawing a marker on every poll.
			Identity: func(a telemetry.Attrs, _ time.Time) []string {
				return []string{
					attrString(a, attrRiskClass),
					attrString(a, attrRiskSection),
					attrString(a, attrRiskRule),
				}
			},
			Detail: []string{attrRiskClass, attrRiskSection, attrRiskRule},
			Title: func(a telemetry.Attrs) string {
				class := attrString(a, attrRiskClass)
				if class == "" {
					class = "finding"
				}
				return "ACL risk " + strings.ReplaceAll(class, "_", " ")
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

// revisionIdentity returns the stable source revision carried by snapshot
// records. The etag fallback keeps the rule useful for a minimal policy-diff
// emitter that has not copied the generic snapshot revision attribute. If
// neither is available, the event timestamp is the only identity available;
// it still dedupes redelivery when the source preserves its timestamp.
func revisionIdentity(a telemetry.Attrs, eventTime time.Time) []string {
	revision := attrString(a, attrSnapshotRevision)
	if revision == "" {
		revision = attrString(a, attrACLEtag)
	}
	if revision != "" {
		return []string{revision}
	}
	return []string{eventTime.UTC().Format(time.RFC3339Nano)}
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
