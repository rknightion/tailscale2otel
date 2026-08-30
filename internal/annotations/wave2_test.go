package annotations_test

import (
	"slices"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/annotations"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

func TestWave2CategoriesAreInTheClosedSet(t *testing.T) {
	want := []annotations.Category{
		annotations.CategoryConfigChange,
		annotations.CategoryExpiry,
		annotations.CategoryPolicyChange,
		annotations.CategoryInventory,
		annotations.CategoryRisk,
		annotations.CategoryLifecycle,
	}
	if got := annotations.Categories(); !slices.Equal(got, want) {
		t.Fatalf("Categories() = %v, want %v", got, want)
	}
}

func TestWave2RulesClassifyStoreEvents(t *testing.T) {
	tests := []struct {
		name     string
		ruleID   string
		event    telemetry.Event
		category annotations.Category
	}{
		{
			name:     "policy revision snapshot",
			ruleID:   annotations.RulePolicySnapshot,
			category: annotations.CategoryPolicyChange,
			event: telemetry.Event{
				Name:      "tailscale.acl.policy_snapshot",
				Timestamp: time.Unix(1_700_000_000, 0),
				Attrs: telemetry.Attrs{
					"tailscale.snapshot.reason":   "change",
					"tailscale.snapshot.revision": "revision-1",
					"tailscale.acl.etag":          "revision-1",
				},
			},
		},
		{
			name:     "device added",
			ruleID:   annotations.RuleInventoryChange,
			category: annotations.CategoryInventory,
			event: telemetry.Event{
				Name:      "tailscale.device.change",
				Timestamp: time.Unix(1_700_000_002, 0),
				Attrs: telemetry.Attrs{
					"tailscale.device.change": "added",
					semconv.HostID:            "device-1",
					semconv.HostName:          "synthetic-device",
				},
			},
		},
		{
			name:     "risk finding",
			ruleID:   annotations.RuleRiskFinding,
			category: annotations.CategoryRisk,
			event: telemetry.Event{
				Name:      "tailscale.acl.risky_rule",
				Timestamp: time.Unix(1_700_000_003, 0),
				Attrs: telemetry.Attrs{
					"tailscale.acl.risk_class": "ssh_wildcard",
					"tailscale.acl.section":    "ssh",
					"tailscale.acl.rule":       "synthetic-rule",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := ruleByID(t, tc.ruleID)
			if rule.EventName != tc.event.Name {
				t.Fatalf("rule %q EventName = %q, want %q", tc.ruleID, rule.EventName, tc.event.Name)
			}
			if rule.Match != nil && !rule.Match(tc.event.Attrs) {
				t.Fatalf("rule %q did not classify its source event", tc.ruleID)
			}

			sink := &capturingSink{}
			recorder := annotations.NewRecorder(annotations.RecorderOptions{
				Config: annotations.RecorderConfig{
					Categories: map[annotations.Category]annotations.CategoryConfig{
						annotations.CategoryPolicyChange: {Enabled: true},
						annotations.CategoryInventory:    {Enabled: true},
						annotations.CategoryRisk:         {Enabled: true},
					},
				},
				Sink:   sink,
				Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
			})
			recorder.ObserveEvent("synthetic-tailnet", tc.event)

			if len(sink.published) != 1 {
				t.Fatalf("published = %d, want 1: %+v", len(sink.published), sink.published)
			}
			got := sink.published[0]
			if got.Category != tc.category {
				t.Errorf("annotation category = %q, want %q", got.Category, tc.category)
			}
			if got.RuleID != tc.ruleID {
				t.Errorf("annotation rule = %q, want %q", got.RuleID, tc.ruleID)
			}
			if !slices.Contains(got.Tags(), "category:"+string(tc.category)) {
				t.Errorf("annotation tags = %v, missing category tag", got.Tags())
			}
		})
	}
}

func TestWave2PolicyHeartbeatAndUnknownRiskAreNotClassified(t *testing.T) {
	cases := []telemetry.Event{
		{
			Name: "tailscale.acl.policy_snapshot",
			Attrs: telemetry.Attrs{
				"tailscale.snapshot.reason":   "heartbeat",
				"tailscale.snapshot.revision": "revision-1",
			},
		},
		{
			Name: "tailscale.acl.risky_rule",
			Attrs: telemetry.Attrs{
				"tailscale.acl.risk_class": "unclassified",
			},
		},
	}
	sink := &capturingSink{}
	recorder := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryPolicyChange: {Enabled: true},
				annotations.CategoryRisk:         {Enabled: true},
			},
		},
		Sink:   sink,
		Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
	})
	for _, event := range cases {
		recorder.ObserveEvent("synthetic-tailnet", event)
	}
	if len(sink.published) != 0 {
		t.Fatalf("published = %d, want no annotation for heartbeat/unknown risk: %+v", len(sink.published), sink.published)
	}
}

func TestWave2PolicySnapshotAndDiffProduceOneAnnotation(t *testing.T) {
	sink := &capturingSink{}
	recorder := annotations.NewRecorder(annotations.RecorderOptions{
		Config: annotations.RecorderConfig{Categories: map[annotations.Category]annotations.CategoryConfig{
			annotations.CategoryPolicyChange: {Enabled: true},
		}},
		Sink: sink, Dedupe: annotations.NewDedupeStoreForTest(nil, time.Hour),
	})
	when := time.Unix(1_700_000_010, 0)
	attrs := telemetry.Attrs{
		"tailscale.snapshot.kind": "policy", "tailscale.snapshot.reason": "change",
		"tailscale.snapshot.revision": "revision-combined", "tailscale.acl.etag": "revision-combined",
	}
	recorder.ObserveEvent("synthetic-tailnet", telemetry.Event{Name: "tailscale.acl.policy_snapshot", Timestamp: when, Attrs: attrs})
	recorder.ObserveEvent("synthetic-tailnet", telemetry.Event{Name: "tailscale.acl.policy_diff", Timestamp: when, Attrs: attrs})
	if got := len(sink.published); got != 1 {
		t.Fatalf("published = %d, want one policy-change annotation per revision: %+v", got, sink.published)
	}
}
