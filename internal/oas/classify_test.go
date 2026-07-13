package oas_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/oas"
)

// opJSON wraps device-item properties into a full listTailnetDevices spec.
// The response schema is: object{devices: array[object{<props>}]}.
// Brace count in prefix = 13 opens; suffix needs 13 closes (props contributes 0 net).
func opJSON(props string) string {
	return fmt.Sprintf(
		`{"paths":{"/d":{"get":{"operationId":"listTailnetDevices","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"devices":{"type":"array","items":{"type":"object","properties":{%s}}}}}}}}}}}}}`,
		props,
	)
}

// opWithRequestBody builds a spec with a requestBody.content["application/json"].schema.required.
func opWithRequestBody(required []string) string {
	reqStr := `[]`
	if len(required) > 0 {
		reqStr = `[`
		for i, r := range required {
			if i > 0 {
				reqStr += ","
			}
			reqStr += `"` + r + `"`
		}
		reqStr += `]`
	}
	return fmt.Sprintf(
		`{"paths":{"/d":{"get":{"operationId":"listTailnetDevices","requestBody":{"content":{"application/json":{"schema":{"type":"object","required":%s,"properties":{"name":{"type":"string"}}}}}},"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}}}}}`,
		reqStr,
	)
}

func mustSpec(t *testing.T, j string) *oas.Spec {
	t.Helper()
	s, err := oas.ParseSpec([]byte(j))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	return s
}

func TestClassify_RemovedResponseField_Breaking(t *testing.T) {
	old := mustSpec(t, opJSON(`"nodeId":{"type":"string"},"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"name":{"type":"string"}`))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.RemovedResponseField {
		t.Fatalf("Kind = %q, want RemovedResponseField", cs[0].Kind)
	}
	if cs[0].Severity != oas.Breaking {
		t.Fatalf("Severity = %q, want Breaking", cs[0].Severity)
	}
}

func TestClassify_NewField_Info(t *testing.T) {
	old := mustSpec(t, opJSON(`"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"name":{"type":"string"},"newField":{"type":"string"}`))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.NewOptionalField {
		t.Fatalf("Kind = %q, want NewOptionalField", cs[0].Kind)
	}
	if cs[0].Severity != oas.Info {
		t.Fatalf("Severity = %q, want Info", cs[0].Severity)
	}
}

func TestClassify_TypeChange_Breaking(t *testing.T) {
	// name: string → integer ⇒ TypeChanged/Breaking
	old := mustSpec(t, opJSON(`"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"name":{"type":"integer"}`))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.TypeChanged {
		t.Fatalf("Kind = %q, want TypeChanged", cs[0].Kind)
	}
	if cs[0].Severity != oas.Breaking {
		t.Fatalf("Severity = %q, want Breaking", cs[0].Severity)
	}
}

func TestClassify_EndpointRemoved_Breaking(t *testing.T) {
	// op present in old, absent in newer ⇒ EndpointRemoved/Breaking
	old := mustSpec(t, opJSON(`"name":{"type":"string"}`))
	// newer has no paths/ops
	newer := mustSpec(t, `{"paths":{}}`)
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.EndpointRemoved {
		t.Fatalf("Kind = %q, want EndpointRemoved", cs[0].Kind)
	}
	if cs[0].Severity != oas.Breaking {
		t.Fatalf("Severity = %q, want Breaking", cs[0].Severity)
	}
}

func TestClassify_IgnoresUnconsumedOps(t *testing.T) {
	// change on an op NOT in opIDs ⇒ 0 changes
	old := mustSpec(t, opJSON(`"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"name":{"type":"integer"}`)) // type change — but op not in list
	cs := oas.Classify(old, newer, []string{"someOtherOp"})
	if len(cs) != 0 {
		t.Fatalf("want 0 changes, got %d: %+v", len(cs), cs)
	}
}

func TestHasActionable(t *testing.T) {
	infoOnly := []oas.Change{
		{OpID: "op", Kind: oas.NewOptionalField, Severity: oas.Info, Detail: "x"},
	}
	if oas.HasActionable(infoOnly) {
		t.Fatal("Info-only changes should not be actionable")
	}

	withWarning := []oas.Change{
		{OpID: "op", Kind: oas.NewOptionalField, Severity: oas.Info, Detail: "x"},
		{OpID: "op", Kind: oas.EnumValueRemoved, Severity: oas.Warning, Detail: "y"},
	}
	if !oas.HasActionable(withWarning) {
		t.Fatal("Warning change should make actionable")
	}

	withBreaking := []oas.Change{
		{OpID: "op", Kind: oas.EndpointRemoved, Severity: oas.Breaking, Detail: "z"},
	}
	if !oas.HasActionable(withBreaking) {
		t.Fatal("Breaking change should make actionable")
	}

	empty := []oas.Change{}
	if oas.HasActionable(empty) {
		t.Fatal("empty changes should not be actionable")
	}
}

func TestClassify_EnumValueAdded_Info(t *testing.T) {
	// enum value added ⇒ EnumValueAdded/Info (non-breaking)
	old := mustSpec(t, opJSON(`"state":{"type":"string","enum":["active","inactive"]}`))
	newer := mustSpec(t, opJSON(`"state":{"type":"string","enum":["active","inactive","pending"]}`))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.EnumValueAdded {
		t.Fatalf("Kind = %q, want EnumValueAdded", cs[0].Kind)
	}
	if cs[0].Severity != oas.Info {
		t.Fatalf("Severity = %q, want Info (enum additions are non-breaking)", cs[0].Severity)
	}
}

func TestClassify_EnumValueRemoved_Warning(t *testing.T) {
	// enum value removed ⇒ EnumValueRemoved/Warning
	old := mustSpec(t, opJSON(`"state":{"type":"string","enum":["active","inactive","pending"]}`))
	newer := mustSpec(t, opJSON(`"state":{"type":"string","enum":["active","inactive"]}`))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.EnumValueRemoved {
		t.Fatalf("Kind = %q, want EnumValueRemoved", cs[0].Kind)
	}
	if cs[0].Severity != oas.Warning {
		t.Fatalf("Severity = %q, want Warning", cs[0].Severity)
	}
}

func TestClassify_NewRequiredRequestField_Breaking(t *testing.T) {
	// new required request field absent in old ⇒ NewRequiredRequestField/Breaking
	old := mustSpec(t, opWithRequestBody(nil))
	newer := mustSpec(t, opWithRequestBody([]string{"newRequired"}))
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(cs), cs)
	}
	if cs[0].Kind != oas.NewRequiredRequestField {
		t.Fatalf("Kind = %q, want NewRequiredRequestField", cs[0].Kind)
	}
	if cs[0].Severity != oas.Breaking {
		t.Fatalf("Severity = %q, want Breaking", cs[0].Severity)
	}
}

func TestClassify_IdenticalSpecs_NoChanges(t *testing.T) {
	// identical specs ⇒ zero changes
	spec := opJSON(`"name":{"type":"string"},"count":{"type":"integer"}`)
	old := mustSpec(t, spec)
	newer := mustSpec(t, spec)
	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})
	if len(cs) != 0 {
		t.Fatalf("identical specs should produce 0 changes, got %d: %+v", len(cs), cs)
	}
}

func TestClassify_SortOrder(t *testing.T) {
	// Multiple ops — verify sort: Breaking > Warning > Info, then OpID, then Detail.
	// Build a two-op spec manually.
	twoOpSpec := func(props1, props2 string) string {
		return fmt.Sprintf(
			`{"paths":{"/a":{"get":{"operationId":"opA","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{%s}}}}}}}},"/b":{"get":{"operationId":"opB","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{%s}}}}}}}}}}`,
			props1, props2,
		)
	}
	old := mustSpec(t, twoOpSpec(`"x":{"type":"string"}`, `"y":{"type":"string"}`))
	// opA: type change (Breaking), opB: new field (Info)
	newer := mustSpec(t, twoOpSpec(`"x":{"type":"integer"}`, `"y":{"type":"string"},"z":{"type":"string"}`))
	cs := oas.Classify(old, newer, []string{"opA", "opB"})
	if len(cs) != 2 {
		t.Fatalf("want 2 changes, got %d: %+v", len(cs), cs)
	}
	// Breaking comes before Info
	if cs[0].Severity != oas.Breaking {
		t.Fatalf("first change should be Breaking, got %q", cs[0].Severity)
	}
	if cs[1].Severity != oas.Info {
		t.Fatalf("second change should be Info, got %q", cs[1].Severity)
	}
}

func TestClassify_NestedArrayField_Removed(t *testing.T) {
	// Verify dotted path: devices[].nodeId removed ⇒ RemovedResponseField/Breaking
	// opJSON already wraps in devices[] array — removing nodeId from items.
	old := mustSpec(t, opJSON(`"nodeId":{"type":"string"},"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"name":{"type":"string"}`))

	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})

	found := false
	for _, c := range cs {
		if c.Kind == oas.RemovedResponseField {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected RemovedResponseField change, got: %+v", cs)
	}
}

func TestClassify_MultipleChanges_Sorted(t *testing.T) {
	// old has nodeId (string) and active (enum active/inactive)
	// newer removes nodeId, changes active enum to remove "inactive" (Warning), adds newField (Info)
	old := mustSpec(t, opJSON(`"nodeId":{"type":"string"},"active":{"type":"string","enum":["active","inactive"]},"name":{"type":"string"}`))
	newer := mustSpec(t, opJSON(`"active":{"type":"string","enum":["active"]},"name":{"type":"string"},"extra":{"type":"boolean"}`))

	cs := oas.Classify(old, newer, []string{"listTailnetDevices"})

	// Should have: RemovedResponseField(Breaking), EnumValueRemoved(Warning), NewOptionalField(Info)
	if len(cs) != 3 {
		t.Fatalf("want 3 changes, got %d: %+v", len(cs), cs)
	}

	// Verify sort order by severity rank
	severityRank := func(s oas.Severity) int {
		switch s {
		case oas.Breaking:
			return 0
		case oas.Warning:
			return 1
		case oas.Info:
			return 2
		default:
			return 99
		}
	}

	if !sort.SliceIsSorted(cs, func(i, j int) bool {
		ri, rj := severityRank(cs[i].Severity), severityRank(cs[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if cs[i].OpID != cs[j].OpID {
			return cs[i].OpID < cs[j].OpID
		}
		return cs[i].Detail < cs[j].Detail
	}) {
		t.Fatalf("changes not sorted: %+v", cs)
	}
}
