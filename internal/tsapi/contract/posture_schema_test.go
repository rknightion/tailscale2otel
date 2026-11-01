package contract_test

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi/contract"
)

// responseForSynthesis changes only test inputs, never the vendored document or
// production decoding. Live GETs with fields=all and fields=id,postureStatus
// observed postureStatus as {postureName:{passing,impacting,failingAssertions}},
// including passing, non-impacting failing and impacting failing verdicts. The
// vendored Device schema instead describes the inner verdict as postureStatus.
// Keep every boundary case; wrapping that verdict schema restores the observed
// map contract without accepting the inaccurate single-verdict wire shape.
func responseForSynthesis(t *testing.T, operation string, response oas.Schema) oas.Schema {
	t.Helper()
	if operation != "listTailnetDevices" {
		return response
	}
	corrected, err := postureSynthesisOverlay(response)
	if err != nil {
		t.Fatal(err)
	}
	return corrected
}

func postureSynthesisOverlay(response oas.Schema) (oas.Schema, error) {
	reassess := errors.New("upstream device schema changed; reassess postureStatus synthesis overlay")
	devices := response.Properties["devices"]
	if response.Type != "object" || devices.Type != "array" || devices.Items == nil || devices.Items.Type != "object" {
		return oas.Schema{}, reassess
	}
	verdict := devices.Items.Properties["postureStatus"]
	expected := oas.Schema{Type: "object", Properties: map[string]oas.Schema{
		"passing":           {Type: "boolean"},
		"impacting":         {Type: "boolean"},
		"failingAssertions": {Type: "array", Items: &oas.Schema{Type: "string"}},
	}}
	if !reflect.DeepEqual(verdict, expected) {
		return oas.Schema{}, reassess
	}
	// SynthesizeBody walks declared properties, whereas BoundaryVariants also
	// exercises AdditionalProperties. Seed one synthetic map key for both paths
	// and keep the typed map value for every boundary variant.
	status := oas.Schema{Type: "object", Properties: map[string]oas.Schema{"posture:synthetic": verdict}, AdditionalProperties: &verdict}
	item := *devices.Items
	item.Properties = maps.Clone(item.Properties)
	item.Properties["postureStatus"] = status
	devices.Items = &item
	response.Properties = maps.Clone(response.Properties)
	response.Properties["devices"] = devices
	return response, nil
}

func TestPostureSynthesisOverlay(t *testing.T) {
	original := loadSpec(t).Ops["listTailnetDevices"].Response
	before, _ := json.Marshal(original)
	corrected, err := postureSynthesisOverlay(original)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("overlay mutated the parsed upstream schema")
	}
	op, _ := contract.ByID("listTailnetDevices")
	bodies := [][]byte{oas.SynthesizeBody(corrected, 8)}
	for _, v := range oas.BoundaryVariants(corrected, 8) {
		if v.Kind.TypeDependent() {
			bodies = append(bodies, v.Body)
		}
	}
	for _, body := range bodies {
		if rep := contract.Decode(op, body); rep.Err != nil {
			t.Errorf("observed map must decode: %v", rep.Err)
		}
		var doc struct {
			Devices []struct {
				Status map[string]json.RawMessage `json:"postureStatus"`
			} `json:"devices"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Devices) == 0 {
			t.Fatal("synthesis failed to exercise a device")
		}
		raw, ok := doc.Devices[0].Status["posture:synthetic"]
		if !ok {
			t.Fatalf("synthesis did not exercise a named posture: %s", body)
		}
		var verdict map[string]json.RawMessage
		if err := json.Unmarshal(raw, &verdict); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"passing", "impacting", "failingAssertions"} {
			if _, ok := verdict[field]; !ok {
				t.Errorf("synthesis lost verdict field %s: %s", field, raw)
			}
		}
	}
	// A corrected upstream map or any other schema drift must retire/reassess the
	// overlay rather than silently wrapping the new shape a second time.
	upstreamFixed := corrected
	devices := upstreamFixed.Properties["devices"]
	item := *devices.Items
	item.Properties = maps.Clone(item.Properties)
	status := item.Properties["postureStatus"]
	status.Properties = nil // upstream's actual typed-map correction has no synthetic property
	item.Properties["postureStatus"] = status
	devices.Items = &item
	upstreamFixed.Properties = maps.Clone(upstreamFixed.Properties)
	upstreamFixed.Properties["devices"] = devices
	for name, response := range map[string]oas.Schema{"upstream corrected": upstreamFixed, "missing response": {}} {
		_, err := postureSynthesisOverlay(response)
		if err == nil || !strings.Contains(err.Error(), "reassess postureStatus synthesis overlay") {
			t.Errorf("%s: upstream change must require reassessment, got %v", name, err)
		}
	}
}
