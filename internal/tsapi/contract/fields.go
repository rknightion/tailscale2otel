package contract

// Field inventory: the reflection half of the consumed-field disposition
// contract (#422).
//
// The universe of "decoded response fields" is the set of EXPORTED fields on the
// Go types the tsapi.Client methods return — i.e. what actually survives decode
// and is available to collectors. Paths are Go field paths, not wire names,
// because the disposition being recorded is about OUR handling of the value; a
// wire rename shows up as a decode failure in the fuzz/live lanes, not here.
//
// Path grammar (mirrors internal/oas.flattenPaths' array convention):
//
//	Field                 struct field
//	Parent.Child          nested struct
//	Slice[]               slice/array element
//	Map{}                 map value
//	Ptr.Child             pointers are transparent
//
// Embedded structs are flattened, matching encoding/json field promotion.
// Unexported fields are skipped: they are never populated by a JSON decode.

import (
	"encoding/json"
	"reflect"
	"sort"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/audit"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// maxFieldDepth bounds the walk so a self-referential type cannot recurse
// forever. The deepest real response type is well under this.
const maxFieldDepth = 12

// ResponseTypeSpec says what one consumed operation decodes into.
//
// Exactly one of Type and Opaque is set. Opaque names the reason a response has
// no field structure to enumerate (e.g. a raw HuJSON body): such an operation
// carries a single op-level disposition instead of per-field ones.
type ResponseTypeSpec struct {
	// Type is the Go type the Client method returns. Slices and pointers are
	// unwrapped to the element type by FieldPaths.
	Type reflect.Type
	// Opaque, when non-empty, is the reason this response has no enumerable
	// fields. Mutually exclusive with Type.
	Opaque string
	// External marks a type declared outside this repository (the upstream
	// tailscale-client-go library). Such types cannot be annotated in-repo, which
	// is precisely why dispositions live in an out-of-band baseline file rather
	// than in struct tags. Informational — it changes nothing about the walk.
	External bool
}

// responseTypes maps operationId → the decoded response type. It is the single
// place that has to change when a consumed operation's return type changes;
// TestResponseTypes_CoversEveryManifestOp keeps it in lockstep with Manifest.
var responseTypes = map[string]ResponseTypeSpec{
	"listTailnetDevices":         {Type: reflect.TypeOf([]tsapi.RichDevice(nil))},
	"listConfigurationAuditLogs": {Type: reflect.TypeOf(audit.ConfigurationResponse{})},
	"listNetworkFlowLogs":        {Type: reflect.TypeOf(flowlog.NetworkResponse{})},
	"listTailnetKeys":            {Type: reflect.TypeOf([]tsapi.Key(nil))},
	"listUsers":                  {Type: reflect.TypeOf([]tsclient.User(nil)), External: true},
	"listWebhooks":               {Type: reflect.TypeOf([]tsclient.Webhook(nil)), External: true},
	"getContacts":                {Type: reflect.TypeOf(tsclient.Contacts{}), External: true},
	"getTailnetSettings":         {Type: reflect.TypeOf(tsapi.TailnetSettings{})},
	"getPolicyFile": {
		External: true,
		Opaque: "response is a raw HuJSON policy document (tsclient.RawACL.HuJSON) — " +
			"an opaque string we never structurally decode; disposition is recorded at op level",
	},
	"getLogStreamingStatus":      {Type: reflect.TypeOf(tsapi.LogStreamStatus{})},
	"getPostureIntegrations":     {Type: reflect.TypeOf([]tsapi.PostureIntegration(nil))},
	"listServices":               {Type: reflect.TypeOf([]tsapi.VIPService(nil))},
	"listServiceHosts":           {Type: reflect.TypeOf([]tsapi.ServiceHost(nil))},
	"getDnsConfiguration":        {Type: reflect.TypeOf(tsapi.DNSConfig{})},
	"listDeviceInvites":          {Type: reflect.TypeOf([]tsapi.DeviceInvite(nil))},
	"getDevicePostureAttributes": {Type: reflect.TypeOf(tsapi.DeviceAttributes{})},
	"listUserInvites":            {Type: reflect.TypeOf([]tsapi.UserInvite(nil))},
	"listOAuthApps":              {Type: reflect.TypeOf([]tsapi.OAuthApp(nil))},
	"listOrganizationTailnets":   {Type: reflect.TypeOf([]tsapi.OrganizationTailnet(nil))},
}

// ResponseTypes returns the operationId → decoded-response-type registry.
func ResponseTypes() map[string]ResponseTypeSpec {
	out := make(map[string]ResponseTypeSpec, len(responseTypes))
	for k, v := range responseTypes {
		out[k] = v
	}
	return out
}

// OpaqueOpPath is the synthetic field path used for an operation whose response
// has no enumerable structure (ResponseTypeSpec.Opaque). It keeps the baseline
// shape uniform: every operation contributes at least one dispositioned row.
const OpaqueOpPath = "(whole response)"

// InventoryEntry is one row of the decoded-field inventory.
type InventoryEntry struct {
	// Op is the OAS operationId.
	Op string
	// Path is the Go field path within the decoded response element type, or
	// OpaqueOpPath for an operation with no enumerable fields.
	Path string
	// Type names the decoded response element type, for review context.
	Type string
	// External reports whether Type is declared outside this repository.
	External bool
}

// FieldInventory returns every (op, field path) pair the manifest decodes,
// sorted by op then path. This is the live side of the disposition contract:
// the baseline file is checked against it.
func FieldInventory() []InventoryEntry {
	var out []InventoryEntry
	for _, op := range Manifest {
		spec, ok := responseTypes[op.ID]
		if !ok {
			continue // TestResponseTypes_CoversEveryManifestOp reports this
		}
		if spec.Opaque != "" {
			out = append(out, InventoryEntry{
				Op: op.ID, Path: OpaqueOpPath, Type: spec.Opaque, External: spec.External,
			})
			continue
		}
		typeName := elemType(spec.Type).String()
		for _, p := range FieldPaths(spec.Type) {
			out = append(out, InventoryEntry{Op: op.ID, Path: p, Type: typeName, External: spec.External})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Op != out[j].Op {
			return out[i].Op < out[j].Op
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// FieldPaths returns the sorted, de-duplicated set of leaf field paths for t.
// Slice, array and pointer wrappers at the root are unwrapped first, so
// FieldPaths([]RichDevice{}) describes a single RichDevice.
func FieldPaths(t reflect.Type) []string {
	seen := map[string]bool{}
	walkFields(elemType(t), "", map[reflect.Type]bool{}, 0, seen)
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// elemType strips pointer, slice and array wrappers from t.
func elemType(t reflect.Type) reflect.Type {
	for t != nil {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			t = t.Elem()
		default:
			return t
		}
	}
	return t
}

// scalarLeaf reports whether t must be recorded as a leaf rather than descended
// into. time.Time and json.RawMessage are structs/slices by kind but are values
// as far as any consumer is concerned; []byte likewise.
func scalarLeaf(t reflect.Type) bool {
	switch t {
	case reflect.TypeOf(time.Time{}), reflect.TypeOf(json.RawMessage(nil)):
		return true
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return true // []byte and named byte-slice types
	}
	return false
}

// walkFields records every leaf path reachable from t under prefix.
// visiting guards against self-referential types on the current path; depth is
// the hard backstop.
func walkFields(t reflect.Type, prefix string, visiting map[reflect.Type]bool, depth int, out map[string]bool) {
	if t == nil || depth > maxFieldDepth {
		record(out, prefix)
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if scalarLeaf(t) {
		record(out, prefix)
		return
	}

	switch t.Kind() {
	case reflect.Struct:
		if visiting[t] {
			record(out, prefix) // cycle: stop, record the node itself
			return
		}
		visiting[t] = true
		defer delete(visiting, t)

		any := false
		for i := range t.NumField() {
			f := t.Field(i)
			// An embedded struct of an unexported TYPE still has its own exported
			// fields promoted by encoding/json, so it must be descended into even
			// though f.IsExported() is false for it.
			promotes := f.Anonymous && elemType(f.Type).Kind() == reflect.Struct && !scalarLeaf(elemType(f.Type))
			if !f.IsExported() && !promotes {
				continue // never populated by a JSON decode
			}
			if f.Tag.Get("json") == "-" {
				continue // explicitly not part of the wire shape
			}
			any = true
			childPrefix := prefix + "." + f.Name
			if prefix == "" {
				childPrefix = f.Name
			}
			if promotes {
				childPrefix = prefix
			}
			walkFields(f.Type, childPrefix, visiting, depth+1, out)
		}
		if !any {
			record(out, prefix) // struct with no exported fields: a leaf
		}
	case reflect.Slice, reflect.Array:
		walkFields(t.Elem(), prefix+"[]", visiting, depth+1, out)
	case reflect.Map:
		walkFields(t.Elem(), prefix+"{}", visiting, depth+1, out)
	default:
		record(out, prefix)
	}
}

// record adds a non-empty path to the result set.
func record(out map[string]bool, prefix string) {
	if prefix != "" {
		out[prefix] = true
	}
}
