package app

import (
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/aclpolicy"
)

func TestACLSnapshotStateStoreFollowsEvidenceDurability(t *testing.T) {
	t.Run("memory evidence stays memory", func(t *testing.T) {
		if _, ok := aclSnapshotStateStore("", "tailnet.example").(*aclpolicy.MemorySnapshotStateStore); !ok {
			t.Fatal("empty evidence path did not select the memory snapshot state store")
		}
	})

	t.Run("file evidence gets a pseudonymous per-tailnet sibling", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "checkpoints.json")
		onePath := aclSnapshotStatePath(base, "one.example")
		twoPath := aclSnapshotStatePath(base, "two.example")
		one := aclSnapshotStateStore(base, "one.example")
		two := aclSnapshotStateStore(base, "two.example")

		if _, ok := one.(*aclpolicy.FileSnapshotStateStore); !ok {
			t.Fatalf("file evidence selected %T, want *aclpolicy.FileSnapshotStateStore", one)
		}
		if _, ok := two.(*aclpolicy.FileSnapshotStateStore); !ok {
			t.Fatalf("file evidence selected %T, want *aclpolicy.FileSnapshotStateStore", two)
		}
		if onePath == twoPath {
			t.Fatalf("tailnet stores share path %q", onePath)
		}
		if filepath.Dir(onePath) != filepath.Dir(base) {
			t.Fatalf("snapshot state dir = %q, want evidence dir %q", filepath.Dir(onePath), filepath.Dir(base))
		}
		if onePath == base {
			t.Fatal("snapshot policy body must not share the checkpoint JSON file")
		}
		if filepath.Base(onePath) == "checkpoints.acl-policy-one.example.json" {
			t.Fatalf("snapshot state path exposes the raw tailnet name: %q", onePath)
		}
	})
}
