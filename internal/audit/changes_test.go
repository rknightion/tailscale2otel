package audit_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// evWith builds a minimal audit Event with the given target type/property,
// action, and actor type — enough to exercise the change classifier.
func evWith(targetType, property, action, actorType string) audit.Event {
	return audit.Event{
		EventTime:    time.Date(2026, 6, 2, 19, 0, 5, 0, time.UTC),
		Type:         "CONFIG",
		EventGroupID: "g-" + property + action,
		Origin:       "ADMIN_CONSOLE",
		Actor:        audit.Actor{ID: "u1", Type: actorType, LoginName: "a@example.com"},
		Target:       audit.Target{ID: "n1", Name: "node.ts.net", Type: targetType, Property: property},
		Action:       action,
	}
}

func TestProcessEmitsCuratedChangeCounter(t *testing.T) {
	cases := []struct {
		name       string
		ev         audit.Event
		wantChange string // "" => must NOT emit the changes counter
	}{
		// Property-derived
		{"acl", evWith("TAILNET", "ACL", "UPDATE", "USER"), "acl"},
		{"key_expiry flag", evWith("NODE", "KEY_EXPIRY", "DISABLE", "NODE"), "key_expiry"},
		{"key_expiry time", evWith("NODE", "KEY_EXPIRY_TIME", "UPDATE", "USER"), "key_expiry"},
		{"exit_node", evWith("NODE", "EXIT_NODE", "UPDATE", "USER"), "exit_node"},
		{"tailnet_lock", evWith("TAILNET", "TKA", "DISABLE", "USER"), "tailnet_lock"},
		{"user_role", evWith("USER", "USER_ROLE", "UPDATE", "USER"), "user_role"},
		{"posture_integration", evWith("TAILNET", "POSTURE_INTEGRATION", "UPDATE", "USER"), "posture_integration"},
		{"collect_posture_identity", evWith("TAILNET", "COLLECT_POSTURE_IDENTITY", "ENABLE", "USER"), "collect_posture_identity"},
		{"magic_dns", evWith("TAILNET", "MAGIC_DNS", "ENABLE", "USER"), "magic_dns"},
		{"dns_config", evWith("TAILNET", "DNS_CONFIG", "UPDATE", "USER"), "dns_config"},
		{"logstream_endpoint", evWith("TAILNET", "LOGSTREAM_ENDPOINT", "CREATE", "USER"), "logstream_endpoint"},
		{"node_share", evWith("SHARE", "NODE_SHARE", "CREATE", "USER"), "node_share"},
		{"tailnet_invite", evWith("INVITE", "TAILNET_INVITE", "CREATE", "USER"), "tailnet_invite"},
		{"auth_provider", evWith("TAILNET", "AUTH_PROVIDER", "MIGRATE_AUTH_PROVIDER", "USER"), "auth_provider"},
		{"secret", evWith("NODE", "SECRET", "CREATE", "SECRET_SCANNER"), "secret"},
		// Type+action-derived (B7 churn + api keys); empty property
		{"device create", evWith("NODE", "", "CREATE", "USER"), "device"},
		{"device delete", evWith("NODE", "", "DELETE", "NODE"), "device"},
		{"device expired", evWith("NODE", "", "EXPIRED", "NODE"), "device"},
		{"api_key create", evWith("API_KEY", "", "CREATE", "USER"), "api_key"},
		{"api_key delete", evWith("API_KEY", "", "DELETE", "USER"), "api_key"},
		{"api_key revoke", evWith("API_KEY", "", "REVOKE", "USER"), "api_key"},
		// Precedence: NODE with a curated property is the property category, not device
		{"node key_expiry not device", evWith("NODE", "KEY_EXPIRY", "DELETE", "USER"), "key_expiry"},
		// Not classified — must NOT emit
		{"node login noise", evWith("NODE", "", "LOGIN", "NODE"), ""},
		{"machine_name noise", evWith("NODE", "MACHINE_NAME", "UPDATE", "NODE"), ""},
		{"posture_identity noise", evWith("NODE", "POSTURE_IDENTITY", "UPDATE", "NODE"), ""},
		{"acl_tags noise", evWith("NODE", "ACL_TAGS", "UPDATE", "NODE"), ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			audit.NewProcessor().Process(tc.ev, rec.Emitter())

			pts := rec.MetricPoints(audit.MetricAuditChanges)
			if tc.wantChange == "" {
				if len(pts) != 0 {
					t.Fatalf("changes counter emitted %d points, want 0 for unclassified event", len(pts))
				}
				return
			}
			if len(pts) != 1 {
				t.Fatalf("changes counter points = %d, want 1", len(pts))
			}
			mp := pts[0]
			if mp.Value != 1 {
				t.Errorf("value = %v, want 1", mp.Value)
			}
			if mp.Unit != "{event}" {
				t.Errorf("unit = %q, want {event}", mp.Unit)
			}
			if mp.Kind != "sum" || !mp.Monotonic {
				t.Errorf("kind=%q monotonic=%v, want sum/true", mp.Kind, mp.Monotonic)
			}
			if got := mp.Attrs["tailscale.audit.change"]; got != tc.wantChange {
				t.Errorf("change attr = %q, want %q", got, tc.wantChange)
			}
			if got := mp.Attrs["tailscale.audit.action"]; got != tc.ev.Action {
				t.Errorf("action attr = %q, want %q", got, tc.ev.Action)
			}
			if got := mp.Attrs["tailscale.actor.type"]; got != tc.ev.Actor.Type {
				t.Errorf("actor.type attr = %q, want %q", got, tc.ev.Actor.Type)
			}
			// PII fence: never high-cardinality identity on the counter.
			for _, k := range []string{"user.id", "user.name", "user.full_name", "tailscale.target.id", "tailscale.target.name"} {
				if _, ok := mp.Attrs[k]; ok {
					t.Errorf("changes counter must not carry %q", k)
				}
			}
		})
	}
}

// TestAuditChangesActionNormalization verifies that:
//   - Known API action values pass through unchanged to the changes counter.
//   - Arbitrary / unknown action values are folded to "other" so the metric's
//     bounded-cardinality guarantee holds even when the API introduces new verbs.
func TestAuditChangesActionNormalization(t *testing.T) {
	cases := []struct {
		rawAction  string
		wantAction string
	}{
		// Known values from the Tailscale OpenAPI spec action enum.
		{"CREATE", "CREATE"},
		{"UPDATE", "UPDATE"},
		{"DELETE", "DELETE"},
		{"ENABLE", "ENABLE"},
		{"DISABLE", "DISABLE"},
		{"REVOKE", "REVOKE"},
		{"EXPIRED", "EXPIRED"},
		{"MIGRATE_AUTH_PROVIDER", "MIGRATE_AUTH_PROVIDER"},
		// Unknown / future values must fold to "other".
		{"SOME_FUTURE_VERB", "other"},
		{"", "other"},
		{"create", "other"}, // case-sensitive
	}
	for _, tc := range cases {
		t.Run(tc.rawAction+"/"+tc.wantAction, func(t *testing.T) {
			// Use a classified event (ACL property) so the changes counter fires.
			ev := evWith("TAILNET", "ACL", tc.rawAction, "USER")
			rec := telemetrytest.New()
			audit.NewProcessor().Process(ev, rec.Emitter())
			pts := rec.MetricPoints(audit.MetricAuditChanges)
			if len(pts) != 1 {
				t.Fatalf("changes counter points = %d, want 1", len(pts))
			}
			if got := pts[0].Attrs["tailscale.audit.action"]; got != tc.wantAction {
				t.Errorf("action attr = %q, want %q", got, tc.wantAction)
			}
		})
	}
}

// TestAuditEventsActionOriginNormalization is the regression for #77: the
// events counter (tailscale.config.audit.events) previously carried the raw
// wire action/origin values, unlike the curated changes counter which already
// normalizes via normalizeAction. Since Process is shared by both the polling
// collector and the streaming receiver, an attacker/misbehaving stream source
// could otherwise mint unbounded cardinality on this counter via crafted
// action/origin values. Known API values must still pass through unchanged;
// unknown values must fold to "other".
func TestAuditEventsActionOriginNormalization(t *testing.T) {
	cases := []struct {
		name       string
		action     string
		origin     string
		wantAction string
		wantOrigin string
	}{
		{"known/known", "CREATE", "ADMIN_CONSOLE", "CREATE", "ADMIN_CONSOLE"},
		{"known action unknown origin", "DELETE", "SOME_FUTURE_ORIGIN", "DELETE", "other"},
		{"unknown action known origin", "SOME_FUTURE_VERB", "NODE", "other", "NODE"},
		{"both unknown", "SOME_FUTURE_VERB", "SOME_FUTURE_ORIGIN", "other", "other"},
		{"empty both", "", "", "other", "other"},
		{"case-sensitive", "create", "admin_console", "other", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := sampleEvent()
			ev.Action = tc.action
			ev.Origin = tc.origin
			rec := telemetrytest.New()
			audit.NewProcessor().Process(ev, rec.Emitter())

			pts := rec.MetricPoints(audit.MetricAuditEvents)
			if len(pts) != 1 {
				t.Fatalf("events counter points = %d, want 1", len(pts))
			}
			if got := pts[0].Attrs["tailscale.audit.action"]; got != tc.wantAction {
				t.Errorf("action attr = %q, want %q", got, tc.wantAction)
			}
			if got := pts[0].Attrs["tailscale.audit.origin"]; got != tc.wantOrigin {
				t.Errorf("origin attr = %q, want %q", got, tc.wantOrigin)
			}
		})
	}
}

// TestAuditEventsFloodBoundsCardinality covers a flood of distinct
// wire-supplied action/origin values (as would arrive via the streaming
// receiver) and asserts the events counter's action/origin attributes never
// exceed the bounded admit-set (the known API enum values plus the single
// "other" overflow bucket).
func TestAuditEventsFloodBoundsCardinality(t *testing.T) {
	rec := telemetrytest.New()
	p := audit.NewProcessor()

	for i := range 2000 {
		ev := sampleEvent()
		ev.EventGroupID = fmt.Sprintf("flood-%d", i) // avoid dedup collisions
		ev.Action = fmt.Sprintf("ATTACKER_ACTION_%d", i)
		ev.Origin = fmt.Sprintf("ATTACKER_ORIGIN_%d", i)
		p.Process(ev, rec.Emitter())
	}

	pts := rec.MetricPoints(audit.MetricAuditEvents)
	distinctActions := map[string]bool{}
	distinctOrigins := map[string]bool{}
	for _, mp := range pts {
		distinctActions[mp.Attrs["tailscale.audit.action"]] = true
		distinctOrigins[mp.Attrs["tailscale.audit.origin"]] = true
	}
	if len(distinctActions) != 1 || !distinctActions["other"] {
		t.Fatalf("distinct action values from a 2000-value flood = %v, want only {other}", distinctActions)
	}
	if len(distinctOrigins) != 1 || !distinctOrigins["other"] {
		t.Fatalf("distinct origin values from a 2000-value flood = %v, want only {other}", distinctOrigins)
	}
}

// TestAuditChangesActorTypeNormalization verifies that:
//   - Known API actor.type values pass through unchanged to the changes counter.
//   - Arbitrary / unknown actor.type values are folded to "other" so the metric's
//     bounded-cardinality guarantee holds even when the API introduces new types.
func TestAuditChangesActorTypeNormalization(t *testing.T) {
	cases := []struct {
		rawType  string
		wantType string
	}{
		// Known values from the Tailscale OpenAPI spec actor.type enum.
		{"USER", "USER"},
		{"NODE", "NODE"},
		{"AUTOMATED_WORKER", "AUTOMATED_WORKER"},
		{"OAUTH_CLIENT", "OAUTH_CLIENT"},
		{"SCIM", "SCIM"},
		{"MULLVAD", "MULLVAD"},
		{"LOGSTREAM", "LOGSTREAM"},
		{"SECRET_SCANNER", "SECRET_SCANNER"},
		// Unknown / future types must fold to "other".
		{"SOME_NEW_TYPE", "other"},
		{"", "other"},
		{"user", "other"}, // case-sensitive
	}
	for _, tc := range cases {
		t.Run(tc.rawType+"/"+tc.wantType, func(t *testing.T) {
			// Use a classified event (ACL property) so the changes counter fires.
			ev := evWith("TAILNET", "ACL", "UPDATE", tc.rawType)
			rec := telemetrytest.New()
			audit.NewProcessor().Process(ev, rec.Emitter())
			pts := rec.MetricPoints(audit.MetricAuditChanges)
			if len(pts) != 1 {
				t.Fatalf("changes counter points = %d, want 1", len(pts))
			}
			if got := pts[0].Attrs["tailscale.actor.type"]; got != tc.wantType {
				t.Errorf("actor.type attr = %q, want %q", got, tc.wantType)
			}
		})
	}
}
