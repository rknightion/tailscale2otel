package dedup

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestSetRetainsOnlyFixedSizeDigests(t *testing.T) {
	s := New(2)
	s.CompareAndAdd(strings.Repeat("key", 1<<20), strings.Repeat("value", 1<<20))
	for key, value := range s.seen {
		if len(key) != sha256.Size || len(value) != sha256.Size {
			t.Fatalf("retained key/value sizes = %d/%d, want %d/%d", len(key), len(value), sha256.Size, sha256.Size)
		}
	}
	if got := len(s.order[0]); got != sha256.Size {
		t.Fatalf("retained FIFO key size = %d, want %d", got, sha256.Size)
	}
}
