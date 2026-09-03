package contract_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/tsapi/contract"
)

type leaf struct {
	Name  string
	Count int
}

type nested struct {
	ID       string
	Tags     []string
	Leaf     leaf
	LeafPtr  *leaf
	Leaves   []leaf
	ByRegion map[string]leaf
	When     time.Time
	Raw      json.RawMessage
	Blob     []byte
	Anything any
	hidden   string //nolint:unused // deliberately unexported: must not be walked
}

type selfRef struct {
	Name  string
	Child *selfRef
}

type embedded struct {
	leaf
	Extra string
}

func TestFieldPaths_FlatStruct(t *testing.T) {
	got := contract.FieldPaths(reflect.TypeOf(leaf{}))
	want := []string{"Count", "Name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FieldPaths(leaf) = %v, want %v", got, want)
	}
}

func TestFieldPaths_NestedSlicesMapsAndScalars(t *testing.T) {
	got := contract.FieldPaths(reflect.TypeOf(nested{}))
	want := []string{
		"Anything",
		"Blob",
		"ByRegion{}.Count",
		"ByRegion{}.Name",
		"ID",
		"Leaf.Count",
		"Leaf.Name",
		"LeafPtr.Count",
		"LeafPtr.Name",
		"Leaves[].Count",
		"Leaves[].Name",
		"Raw",
		"Tags[]",
		"When",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FieldPaths(nested) =\n%v\nwant\n%v", got, want)
	}
}

// TestFieldPaths_SliceAndPointerRootsAreUnwrapped: the manifest's response types
// are things like []RichDevice and *DNSConfig; the element type is the unit a
// disposition is assigned against, so the root wrapper must not appear in paths.
func TestFieldPaths_SliceAndPointerRootsAreUnwrapped(t *testing.T) {
	want := []string{"Count", "Name"}
	for _, typ := range []reflect.Type{
		reflect.TypeOf([]leaf{}),
		reflect.TypeOf(&leaf{}),
		reflect.TypeOf([]*leaf{}),
	} {
		if got := contract.FieldPaths(typ); !reflect.DeepEqual(got, want) {
			t.Errorf("FieldPaths(%s) = %v, want %v", typ, got, want)
		}
	}
}

// TestFieldPaths_SelfReferentialTerminates guards the walker against the
// unbounded recursion a self-referential type would otherwise cause.
func TestFieldPaths_SelfReferentialTerminates(t *testing.T) {
	got := contract.FieldPaths(reflect.TypeOf(selfRef{}))
	if len(got) == 0 {
		t.Fatal("FieldPaths(selfRef) returned nothing")
	}
	for _, p := range got {
		if len(p) > 200 {
			t.Fatalf("path did not terminate: %q", p)
		}
	}
}

// TestFieldPaths_EmbeddedStructIsFlattened mirrors encoding/json field
// promotion: an embedded struct's fields appear at the parent level.
func TestFieldPaths_EmbeddedStructIsFlattened(t *testing.T) {
	got := contract.FieldPaths(reflect.TypeOf(embedded{}))
	want := []string{"Count", "Extra", "Name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FieldPaths(embedded) = %v, want %v", got, want)
	}
}

// TestResponseTypes_CoversEveryManifestOp is the seam that keeps the field
// inventory honest: every consumed op must declare what it decodes into, or its
// fields would silently escape the disposition contract.
func TestResponseTypes_CoversEveryManifestOp(t *testing.T) {
	rt := contract.ResponseTypes()
	for _, op := range contract.Manifest {
		spec, ok := rt[op.ID]
		if !ok {
			t.Errorf("op %q has no entry in ResponseTypes() — add one (or mark it Opaque)", op.ID)
			continue
		}
		if spec.Opaque == "" && spec.Type == nil {
			t.Errorf("op %q: neither Type nor Opaque set", op.ID)
		}
		if spec.Opaque != "" && spec.Type != nil {
			t.Errorf("op %q: Opaque and Type are mutually exclusive", op.ID)
		}
	}
	for id := range rt {
		if _, ok := contract.ByID(id); !ok {
			t.Errorf("ResponseTypes() has %q which is not in the manifest", id)
		}
	}
}

// TestInventory_IsSortedAndUnique keeps generated baselines diff-stable.
func TestInventory_IsSortedAndUnique(t *testing.T) {
	inv := contract.FieldInventory()
	seen := map[string]bool{}
	for i, e := range inv {
		key := e.Op + "\x00" + e.Path
		if seen[key] {
			t.Errorf("duplicate inventory entry %s / %s", e.Op, e.Path)
		}
		seen[key] = true
		if i > 0 {
			prev := inv[i-1]
			if prev.Op > e.Op || (prev.Op == e.Op && prev.Path >= e.Path) {
				t.Fatalf("inventory not sorted at %d: %s/%s after %s/%s", i, e.Op, e.Path, prev.Op, prev.Path)
			}
		}
	}
	if len(inv) == 0 {
		t.Fatal("FieldInventory() is empty")
	}
}
