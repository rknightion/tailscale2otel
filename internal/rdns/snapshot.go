package rdns

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
)

const (
	snapshotFileName           = "rdns.json"
	snapshotVersion            = 1
	maxSnapshotEntries         = 4096
	maxSnapshotBytes     int64 = 4 << 20
	maxSnapshotNameBytes       = 255
	snapshotFileMode           = 0o600
)

var errSnapshotTooLarge = errors.New("rdns snapshot exceeds its byte bound")

// SnapshotPath returns the deterministic sidecar path for a checkpoint file.
// An empty checkpoint path means the checkpoint store is memory-only, so there
// is no honest durable location for the RDNS cache either.
func SnapshotPath(checkpointPath string) string {
	checkpointPath = strings.TrimSpace(checkpointPath)
	if checkpointPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(checkpointPath), snapshotFileName)
}

type diskSnapshot struct {
	Version int                 `json:"version"`
	Entries []diskSnapshotEntry `json:"entries"`
}

type diskSnapshotEntry struct {
	Addr    string    `json:"addr"`
	Name    string    `json:"name"`
	Expires time.Time `json:"expires"`
}

// markSnapshotDirtyLocked records a cache mutation for the next periodic or
// lifecycle flush. Callers must hold c.mu.
func (c *Cache) markSnapshotDirtyLocked() {
	if c.snapshotPath == "" {
		return
	}
	c.snapshotDirty = true
	c.snapshotGeneration++
}

// loadSnapshot is deliberately fail-open. The cache is an enrichment, not a
// source of truth: a missing, unreadable, oversized, incompatible, or corrupt
// sidecar simply leaves the cache cold.
func (c *Cache) loadSnapshot() {
	if c.snapshotPath == "" {
		return
	}
	data, err := safefile.ReadRegular(c.snapshotPath, maxSnapshotBytes, safefile.NoSymlink)
	if err != nil || len(data) == 0 {
		return
	}

	var snapshot diskSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return
	}
	limit := c.snapshotEntryLimit()
	if snapshot.Version != snapshotVersion || snapshot.Entries == nil || len(snapshot.Entries) > limit {
		return
	}

	now := c.now()
	entries := make(map[netip.Addr]entry, len(snapshot.Entries))
	for _, persisted := range snapshot.Entries {
		addr, err := netip.ParseAddr(persisted.Addr)
		if err != nil || !addr.IsValid() || persisted.Expires.IsZero() || !validSnapshotName(persisted.Name) {
			// Any structurally invalid record makes the whole snapshot untrusted
			// and forces a cold start. Expired records are valid but deliberately
			// skipped so they can never be resurrected after a restart.
			return
		}
		if !now.Before(persisted.Expires) {
			continue
		}
		if _, duplicate := entries[addr]; duplicate {
			return
		}
		entries[addr] = entry{name: persisted.Name, expires: persisted.Expires}
	}
	c.entries = entries
}

func (c *Cache) snapshotEntryLimit() int {
	if c.max < maxSnapshotEntries {
		return c.max
	}
	return maxSnapshotEntries
}

func validSnapshotName(name string) bool {
	if name == "" || len(name) > maxSnapshotNameBytes ||
		strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") {
		return name == ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// persistSnapshotIfDirty avoids rewriting an unchanged sidecar on every report
// tick. It is intentionally only a hint: a mutation racing this check remains
// dirty and is picked up by the next tick or lifecycle flush.
func (c *Cache) persistSnapshotIfDirty() {
	if c.snapshotPath == "" {
		return
	}
	c.mu.Lock()
	dirty := c.snapshotDirty
	c.mu.Unlock()
	if dirty {
		c.persistSnapshot()
	}
}

// persistSnapshot serializes a consistent view and atomically replaces the
// sidecar. snapshotMu orders captures as well as writes: without it, two
// concurrent resolves could write an older captured map after a newer one.
// Persistence failures disable future attempts for this cache instance; the
// in-memory resolver remains fully functional.
func (c *Cache) persistSnapshot() {
	if c.snapshotPath == "" {
		return
	}
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	if c.snapshotOff {
		return
	}

	c.mu.Lock()
	data, err := c.marshalSnapshotLocked()
	generation := c.snapshotGeneration
	c.mu.Unlock()
	if err != nil {
		c.snapshotOff = true
		return
	}
	if err := writeSnapshotAtomic(c.snapshotPath, data); err != nil {
		c.snapshotOff = true
		return
	}
	c.mu.Lock()
	if c.snapshotGeneration == generation {
		c.snapshotDirty = false
	}
	c.mu.Unlock()
}

func (c *Cache) marshalSnapshotLocked() ([]byte, error) {
	now := c.now()
	entries := make([]diskSnapshotEntry, 0, min(len(c.entries), c.snapshotEntryLimit()))
	for addr, cached := range c.entries {
		// Never carry an expired entry across a restart. This also excludes a
		// normally servable stale entry: stale serving is an in-process grace
		// policy, not permission to resurrect state after a restart.
		if !now.Before(cached.expires) || !validSnapshotName(cached.name) {
			continue
		}
		entries = append(entries, diskSnapshotEntry{
			Addr:    addr.String(),
			Name:    cached.name,
			Expires: cached.expires.UTC(),
		})
	}

	// Keep the entries with the most remaining TTL when the in-memory cache is
	// larger than the intentionally small durable sidecar. Ties are resolved by
	// address, and the final serialization order is address-sorted, so identical
	// state always produces identical bytes.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Expires.Equal(entries[j].Expires) {
			return entries[i].Addr < entries[j].Addr
		}
		return entries[i].Expires.After(entries[j].Expires)
	})
	if limit := c.snapshotEntryLimit(); len(entries) > limit {
		entries = entries[:limit]
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Addr < entries[j].Addr })

	data, err := json.Marshal(diskSnapshot{Version: snapshotVersion, Entries: entries})
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSnapshotBytes {
		return nil, errSnapshotTooLarge
	}
	return data, nil
}

func writeSnapshotAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed, renamed := false, false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()

	if err := f.Chmod(snapshotFileMode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	syncSnapshotDir(dir)
	return nil
}

func syncSnapshotDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
