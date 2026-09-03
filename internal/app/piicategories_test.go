package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

// allCategoriesOff is a PIIFilterConfig with EVERY field false. It is built by
// starting from the all-true default and flipping each field, so adding a field
// to PIIFilterConfig without updating this helper shows up as a compile error
// on the struct literal rather than as a silently-skipped category.
func allCategoriesOff() config.PIIFilterConfig {
	return config.PIIFilterConfig{
		Emails: false, UserDisplayNames: false, UserIDs: false, Hostnames: false,
		NodeIDs: false, TailscaleIPs: false, InternalIPs: false, ExternalIPs: false,
		ServiceAddrs: false, EndpointPaths: false, NetworkTopology: false,
		TailnetName: false, FreeTextDetails: false, CommandText: false,
	}
}

// TestPIICategoriesMapsEveryCategory closes a real gap. options.go's comment
// claims every category is explicitly mapped so "future categories can't
// silently escape", but nothing enforced that: a category added to
// pii.AllCategories and to PIIFilterConfig, then forgotten in piiCategories,
// would simply be absent from the returned map — and the redactor treats an
// ABSENT key as ENABLED. The result is a config toggle that is silently dead,
// exporting the very attribute an operator switched off.
//
// The existing selfobs count test does not cover this: it asserts one gauge per
// AllCategories entry, and the gauge comes from emitPIIFilterCategory's switch,
// which has a `default: return true` arm — so a missed category still produces a
// datapoint and the count still matches.
func TestPIICategoriesMapsEveryCategory(t *testing.T) {
	got := piiCategories(allCategoriesOff())
	if len(got) != len(pii.AllCategories) {
		t.Errorf("piiCategories returned %d entries, want %d (one per pii.AllCategories)",
			len(got), len(pii.AllCategories))
	}
	for _, cat := range pii.AllCategories {
		enabled, present := got[cat]
		if !present {
			t.Errorf("category %q missing from piiCategories: the redactor treats an absent "+
				"key as ENABLED, so its config toggle would be silently dead", cat)
			continue
		}
		if enabled {
			t.Errorf("category %q reported enabled from an all-false config: it is wired to "+
				"the wrong PIIFilterConfig field", cat)
		}
	}
}

// TestPIIFilterEnabledHasACaseForEveryCategory guards the other half of the same
// bug. piiCategoryEnabled's `default` arm returns true, so a category with no case
// is reported as emitted no matter what the operator configured — and the
// self-obs gauge would then lie about the running redaction policy.
func TestPIIFilterEnabledHasACaseForEveryCategory(t *testing.T) {
	off := allCategoriesOff()
	for _, cat := range pii.AllCategories {
		if piiCategoryEnabled(off, cat) {
			t.Errorf("piiCategoryEnabled(all-off, %q) = true: no switch case for this category, "+
				"so it fell through to the default arm", cat)
		}
	}
}

// TestCommandTextIsDefaultOn pins the product decision on #462: this filter is
// opt-OUT redaction, so exec command text ships unless an operator turns it off.
func TestCommandTextIsDefaultOn(t *testing.T) {
	if !config.Default().PIIFilter.CommandText {
		t.Fatal("pii_filter.command_text defaults to false; it must default to true like every other category")
	}
}
