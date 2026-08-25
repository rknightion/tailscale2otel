package enrich

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUpsertUnverifiedBoundsRetainedTextBytes(t *testing.T) {
	huge := strings.Repeat("界", 20_000)
	c := NewDeviceCache()
	c.UpsertUnverified([]DeviceMeta{{
		NodeID: "node-1", ID: huge, Name: huge, Hostname: huge, OS: huge,
		OSVersion: huge, User: huge, Tags: []string{huge, huge, huge},
	}})
	entries := c.SnapshotUnverified()
	if len(entries) != 1 {
		t.Fatalf("unverified entries = %d, want 1", len(entries))
	}
	m := entries[0]
	values := []string{m.ID, m.NodeID, m.Name, m.Hostname, m.OS, m.OSVersion, m.User}
	values = append(values, m.Tags...)
	total := 0
	for i, value := range values {
		if len(value) > MaxUnverifiedFieldBytes {
			t.Errorf("field %d retained %d bytes, want <= %d", i, len(value), MaxUnverifiedFieldBytes)
		}
		if !utf8.ValidString(value) {
			t.Errorf("field %d is invalid UTF-8", i)
		}
		total += len(value)
	}
	if total > MaxUnverifiedEntryBytes {
		t.Fatalf("entry retained %d bytes, want <= %d", total, MaxUnverifiedEntryBytes)
	}
}
