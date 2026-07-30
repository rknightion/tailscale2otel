package contract_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/oas"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi/contract"
)

// The boundary matrix (#433): every consumed operation × every boundary shape,
// driven through the real tsapi.Client decoders.
//
// Before this, TestFuzz_SchemaSynthDecodes gave each operation ONE representative
// body and TestFuzz_EdgeVariants covered five edge shapes for exactly ONE of the
// eighteen — listTailnetDevices, by hand. The other seventeen had no boundary
// coverage at all, so the shapes that actually break decoders (null, an empty
// container, nulled nullable fields, extreme values, an unknown enum member, an
// additive field, a wrong container shape) were untested everywhere else.
//
// The variants come from each operation's own response schema (oas.BoundaryVariants),
// so this covers a NEW consumed operation the day it joins the manifest, with no
// per-operation authoring. Failures name the operation, the boundary kind and the
// exact body.

// boundaryExemptions records operations excluded from a specific boundary kind,
// with the reason, in the same shape as oasMistypedOps. There are none: the six
// operations whose OAS field types disagree with the wire are handled by kind
// (TypeDependent) rather than by exemption, because their shape-only boundaries are
// perfectly testable. An entry here is a debt, not a configuration knob.
var boundaryExemptions = map[string]map[oas.BoundaryKind]string{}

func TestBoundary_EveryConsumedOperationSurvivesEveryShape(t *testing.T) {
	spec := loadSpec(t)

	covered := map[string]int{}
	for _, op := range contract.Manifest {
		o, ok := spec.Ops[op.ID]
		if !ok {
			t.Errorf("%s: not in the vendored OAS, so no boundary bodies can be derived", op.ID)
			continue
		}

		for _, v := range oas.BoundaryVariants(o.Response, 8) {
			if reason, skip := boundaryExemptions[op.ID][v.Kind]; skip {
				t.Logf("%s/%s: exempt — %s", op.ID, v.Kind, reason)
				continue
			}
			// getPolicyFile reads raw HuJSON bytes, so it accepts literally any body
			// and has no decode contract to assert. It is already FuzzSkip.
			if op.FuzzSkip {
				continue
			}
			// The six operations whose OAS field TYPES disagree with the wire cannot
			// have a type-derived body synthesized for them (see oasMistypedOps).
			// Their shape-only boundaries still run.
			if reason, mistyped := oasMistypedOps[op.ID]; mistyped && v.Kind.TypeDependent() {
				t.Logf("%s/%s: skipped, type-dependent body against an inaccurate OAS type — %s",
					op.ID, v.Kind, reason)
				continue
			}

			covered[op.ID]++
			rep := contract.Decode(op, v.Body)
			switch {
			case v.MustDecode && rep.Err != nil:
				t.Errorf("%s/%s: decoder rejected a body it must accept: %v\nbody=%s",
					op.ID, v.Kind, rep.Err, v.Body)
			case v.MustError && rep.Err == nil:
				t.Errorf("%s/%s: decoder ACCEPTED a body it must reject. A wrong container shape "+
					"that decodes cleanly reports zero rows instead of surfacing that the response "+
					"moved.\nbody=%s", op.ID, v.Kind, v.Body)
			}
		}
	}

	// Guard against the matrix quietly covering nothing: 18 operations, one of them
	// FuzzSkip, six type-mistyped (4 shape-only kinds each), eleven full (8 kinds).
	const wantOps = 17
	if len(covered) != wantOps {
		t.Errorf("boundary matrix ran against %d operations, want %d: %v", len(covered), wantOps, covered)
	}
	total := 0
	for _, n := range covered {
		total += n
	}
	if want := 11*8 + 6*4; total != want {
		t.Errorf("boundary matrix ran %d (operation, kind) pairs, want %d — a silent drop in "+
			"coverage looks exactly like a passing test", total, want)
	}
}

// The wrong-shape expectation was verified against all 18 consumed operations
// before being asserted, and this pins the two real top-level shapes so a manifest
// entry cannot silently acquire the wrong one. listUserInvites and listDeviceInvites
// return a bare array; every other consumed operation returns an object.
func TestBoundary_TopLevelShapesAreWhatTheManifestClaims(t *testing.T) {
	spec := loadSpec(t)

	bareArray := map[string]bool{"listUserInvites": true, "listDeviceInvites": true}
	for _, op := range contract.Manifest {
		o, ok := spec.Ops[op.ID]
		if !ok {
			continue
		}
		wantArray := bareArray[op.ID]
		gotArray := o.Response.Type == "array"
		if op.ID == "getPolicyFile" {
			continue // HuJSON; the OAS types it as a bare object with no properties
		}
		if gotArray != wantArray {
			t.Errorf("%s: OAS response is array=%v, manifest treats it as array=%v",
				op.ID, gotArray, wantArray)
		}
		// The sentinel in KnownTopLevelKeys must agree with the schema, or Decode
		// skips the unknown-key check on an object response and the additive-field
		// boundary stops proving anything.
		sentinel := len(op.KnownTopLevelKeys) == 1 && op.KnownTopLevelKeys[0] == ""
		if sentinel != wantArray {
			t.Errorf("%s: bare-array sentinel=%v but the response is array=%v",
				op.ID, sentinel, wantArray)
		}
	}
}

// The additive-field boundary must actually be VISIBLE to the harness: Decode
// reports unexpected top-level keys, and if it did not, an additive field would be
// accepted without anything having checked that it was noticed.
func TestBoundary_AdditiveFieldIsReportedAsAnUnknownKey(t *testing.T) {
	spec := loadSpec(t)

	sawAny := false
	for _, op := range contract.Manifest {
		o, ok := spec.Ops[op.ID]
		if !ok || op.FuzzSkip {
			continue
		}
		if _, mistyped := oasMistypedOps[op.ID]; mistyped {
			continue
		}
		if len(op.KnownTopLevelKeys) == 1 && op.KnownTopLevelKeys[0] == "" {
			continue // bare array: no top-level keys to report
		}

		for _, v := range oas.BoundaryVariants(o.Response, 8) {
			if v.Kind != oas.BoundaryAdditiveField {
				continue
			}
			rep := contract.Decode(op, v.Body)
			found := false
			for _, k := range rep.UnknownTopLevelKeys {
				if strings.HasPrefix(k, "ts2otel") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: the injected additive field was not reported as an unknown "+
					"top-level key (got %v) — the decoder tolerating it is only half the "+
					"contract; the harness has to see it", op.ID, rep.UnknownTopLevelKeys)
			}
			sawAny = true
		}
	}
	if !sawAny {
		t.Error("no operation was checked, so this test asserted nothing")
	}
}
