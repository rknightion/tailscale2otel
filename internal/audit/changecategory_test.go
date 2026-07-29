package audit

import "testing"

// TestChangeCategoryMatchesClassifyChange pins the exported wrapper to the
// unexported classifier it fronts. The wrapper exists so internal/annotations
// can reuse this one curated vocabulary rather than growing a second copy of
// it; two copies would drift silently, and the schema guard in taxonomy_test.go
// only watches this one.
func TestChangeCategoryMatchesClassifyChange(t *testing.T) {
	// Every curated property, plus the two type+action paths, plus the misses.
	var events []Event
	for property := range propertyCategories {
		events = append(events, Event{Target: Target{Property: property}})
	}
	for action := range deviceChurnActions {
		events = append(events, Event{Action: action, Target: Target{Type: "NODE"}})
	}
	for action := range apiKeyActions {
		events = append(events, Event{Action: action, Target: Target{Type: "API_KEY"}})
	}
	events = append(events,
		Event{}, // nothing at all
		Event{Action: "LOGIN", Target: Target{Type: "USER"}},                          // uncurated type
		Event{Action: "LOGIN", Target: Target{Type: "NODE"}},                          // curated type, uncurated action
		Event{Action: "UPDATE", Target: Target{Type: "API_KEY"}},                      // curated type, uncurated action
		Event{Target: Target{Property: "ACL_TAGS"}},                                   // deliberately excluded property
		Event{Action: "CREATE", Target: Target{Type: "NODE", Property: "KEY_EXPIRY"}}, // precedence
	)

	for _, ev := range events {
		wantCat, wantOK := classifyChange(ev)
		gotCat, gotOK := ChangeCategory(ev.Target.Property, ev.Target.Type, ev.Action)
		if gotCat != wantCat || gotOK != wantOK {
			t.Errorf("ChangeCategory(%q, %q, %q) = (%q, %v), want (%q, %v)",
				ev.Target.Property, ev.Target.Type, ev.Action, gotCat, gotOK, wantCat, wantOK)
		}
	}
}

// TestChangeCategoryPropertyWinsOverType is the precedence rule stated
// explicitly rather than only implied by the agreement test above: a NODE
// CREATE whose property is curated must classify by the property, or a key
// expiry change would be recorded as routine device churn.
func TestChangeCategoryPropertyWinsOverType(t *testing.T) {
	cat, ok := ChangeCategory("KEY_EXPIRY", "NODE", "CREATE")
	if !ok || cat != "key_expiry" {
		t.Fatalf("ChangeCategory(KEY_EXPIRY, NODE, CREATE) = (%q, %v), want (key_expiry, true)", cat, ok)
	}
}
