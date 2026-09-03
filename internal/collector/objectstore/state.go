package objectstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

const scanPrefix = "scan/"
const scanStartMarker = "-"
const gapPrefix = "gap/"

type scanState struct {
	Positions map[string]string
	StaleKeys []string
}

func loadScanState(
	cp collector.CheckpointStore,
	configuredBasePrefix string,
	layout Layout,
) (scanState, error) {
	state := scanState{Positions: map[string]string{}}
	for _, row := range cp.Keys() {
		encoded, ok := strings.CutPrefix(row, scanPrefix)
		if !ok {
			continue
		}
		// An EMPTY encoded prefix is legitimate: LayoutFlat with no configured
		// prefix scans the bucket root, whose prefix is the empty string. Only the
		// key half must be present.
		encodedPrefix, encodedKey, ok := strings.Cut(encoded, "/")
		if !ok || encodedKey == "" || strings.Contains(encodedKey, "/") {
			return scanState{}, fmt.Errorf("objectstore: invalid scan checkpoint %q", row)
		}
		prefixBytes, err := base64.RawURLEncoding.DecodeString(encodedPrefix)
		if err != nil {
			return scanState{}, fmt.Errorf("objectstore: decode scan prefix: %w", err)
		}
		var key string
		if encodedKey != scanStartMarker {
			keyBytes, err := base64.RawURLEncoding.DecodeString(encodedKey)
			if err != nil {
				return scanState{}, fmt.Errorf("objectstore: decode scan key: %w", err)
			}
			key = string(keyBytes)
		}
		prefix := string(prefixBytes)
		if key != "" && !strings.HasPrefix(key, prefix) {
			return scanState{}, fmt.Errorf("objectstore: scan key is outside its prefix")
		}
		if !isConfiguredScanPrefix(prefix, configuredBasePrefix, layout) {
			state.StaleKeys = append(state.StaleKeys, row)
			continue
		}
		if _, duplicate := state.Positions[prefix]; duplicate {
			return scanState{}, fmt.Errorf("objectstore: duplicate scan checkpoint for prefix %q", prefix)
		}
		state.Positions[prefix] = key
	}
	sort.Strings(state.StaleKeys)
	return state, nil
}

// isConfiguredScanPrefix reports whether a persisted scan row names a prefix the
// CURRENT layout still enumerates. A row that does not is returned as stale and
// deleted by the cycle that loads it, which is what makes a layout switch safe in
// both directions: the day-partition rows a partitioned deployment wrote are
// pruned on the first flat cycle, and the single flat row is pruned on the first
// partitioned cycle. Nothing is left behind to be listed under one layout and
// counted against the prefix cap under the other.
func isConfiguredScanPrefix(prefix, configuredBasePrefix string, layout Layout) bool {
	if layout == LayoutFlat {
		// Exactly the one prefix scanPrefixes lists, compared the same way it is
		// built, so a configured prefix written with or without a trailing slash
		// resolves to the same durable row.
		return prefix == flatPrefix(configuredBasePrefix)
	}
	return isConfiguredDayPrefix(prefix, configuredBasePrefix)
}

func isConfiguredDayPrefix(prefix, configuredBasePrefix string) bool {
	// scanBase, NOT strings.Trim: this must derive the base exactly the way
	// dayPrefixes does when it builds the prefix being tested. Trimming both ends
	// here while the builder trimmed only the trailing slash meant a configured
	// "/flow" listed "/flow/2026/07/24/" and then looked for a row under
	// "flow/", so every persisted scan position was classified stale and DELETED
	// by the cycle that loaded it (#498).
	base := scanBase(configuredBasePrefix)
	datePart := prefix
	if base != "" {
		want := base + "/"
		if !strings.HasPrefix(prefix, want) {
			return false
		}
		datePart = strings.TrimPrefix(prefix, want)
	}
	if len(datePart) != len("2006/01/02/") || datePart[len(datePart)-1] != '/' {
		return false
	}
	_, err := time.Parse("2006/01/02", strings.TrimSuffix(datePart, "/"))
	return err == nil
}

type checkpointBatch struct {
	updates map[string]time.Time
	deletes []string
}

func newCheckpointBatch() *checkpointBatch {
	return &checkpointBatch{updates: map[string]time.Time{}}
}

func (b *checkpointBatch) setScanPosition(
	cp collector.CheckpointStore,
	prefix string,
	key string,
	at time.Time,
) {
	rowPrefix := scanRowPrefix(prefix)
	b.removeScanRows(cp, rowPrefix)
	encodedKey := scanStartMarker
	if key != "" {
		encodedKey = base64.RawURLEncoding.EncodeToString([]byte(key))
	}
	b.updates[rowPrefix+encodedKey] = at
}

func (b *checkpointBatch) clearScanPosition(cp collector.CheckpointStore, prefix string) {
	b.removeScanRows(cp, scanRowPrefix(prefix))
}

func (b *checkpointBatch) removeScanRows(cp collector.CheckpointStore, rowPrefix string) {
	for _, row := range cp.Keys() {
		if strings.HasPrefix(row, rowPrefix) {
			b.delete(row)
		}
	}
	for row := range b.updates {
		if strings.HasPrefix(row, rowPrefix) {
			delete(b.updates, row)
		}
	}
}

func (b *checkpointBatch) delete(key string) {
	delete(b.updates, key)
	for _, existing := range b.deletes {
		if existing == key {
			return
		}
	}
	b.deletes = append(b.deletes, key)
}

func (b *checkpointBatch) apply(cp collector.CheckpointStore) error {
	return collector.UpdateCheckpointBatch(cp, b.updates, b.deletes)
}

func scanRowPrefix(prefix string) string {
	return scanPrefix + base64.RawURLEncoding.EncodeToString([]byte(prefix)) + "/"
}

type gapState struct {
	Identity    string
	Key         string
	FirstFailed time.Time
	NextAttempt time.Time
	Attempts    int
	Quarantined bool
}

func loadGaps(cp collector.CheckpointStore) ([]gapState, error) {
	var gaps []gapState
	seen := map[string]struct{}{}
	for _, row := range cp.Keys() {
		if !strings.HasPrefix(row, gapPrefix) {
			continue
		}
		firstFailed, ok := cp.Get(row)
		if !ok {
			return nil, fmt.Errorf("objectstore: gap checkpoint disappeared while loading")
		}
		gap, err := decodeGapRow(row, firstFailed)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[gap.Identity]; duplicate {
			return nil, fmt.Errorf("objectstore: duplicate gap checkpoint")
		}
		seen[gap.Identity] = struct{}{}
		gaps = append(gaps, gap)
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].FirstFailed.Equal(gaps[j].FirstFailed) {
			return gaps[i].Identity < gaps[j].Identity
		}
		return gaps[i].FirstFailed.Before(gaps[j].FirstFailed)
	})
	return gaps, nil
}

func (b *checkpointBatch) persistGap(cp collector.CheckpointStore, gap gapState) {
	b.removeGapRows(cp, gap.Identity)
	b.updates[encodeGapRow(gap)] = gap.FirstFailed
}

func (b *checkpointBatch) resolveGap(cp collector.CheckpointStore, identity string) {
	b.removeGapRows(cp, identity)
}

func (b *checkpointBatch) removeGapRows(cp collector.CheckpointStore, identity string) {
	for _, row := range cp.Keys() {
		if !strings.HasPrefix(row, gapPrefix) {
			continue
		}
		firstFailed, ok := cp.Get(row)
		if !ok {
			continue
		}
		gap, err := decodeGapRow(row, firstFailed)
		if err == nil && gap.Identity == identity {
			b.delete(row)
		}
	}
	for row, firstFailed := range b.updates {
		if !strings.HasPrefix(row, gapPrefix) {
			continue
		}
		gap, err := decodeGapRow(row, firstFailed)
		if err == nil && gap.Identity == identity {
			delete(b.updates, row)
		}
	}
}

func encodeGapRow(gap gapState) string {
	status := "pending"
	next := gap.NextAttempt.Unix()
	if gap.Quarantined {
		status = "quarantined"
		next = 0
	}
	return gapPrefix +
		status + "/" +
		strconv.Itoa(gap.Attempts) + "/" +
		strconv.FormatInt(next, 10) + "/" +
		base64.RawURLEncoding.EncodeToString([]byte(gap.Identity)) + "/" +
		base64.RawURLEncoding.EncodeToString([]byte(gap.Key))
}

func decodeGapRow(row string, firstFailed time.Time) (gapState, error) {
	encoded, ok := strings.CutPrefix(row, gapPrefix)
	if !ok {
		return gapState{}, fmt.Errorf("objectstore: invalid gap checkpoint")
	}
	parts := strings.SplitN(encoded, "/", 5)
	if len(parts) != 5 {
		return gapState{}, fmt.Errorf("objectstore: invalid gap checkpoint")
	}
	attempts, err := strconv.Atoi(parts[1])
	if err != nil || attempts < 1 {
		return gapState{}, fmt.Errorf("objectstore: invalid gap attempt count")
	}
	nextUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return gapState{}, fmt.Errorf("objectstore: invalid gap retry time")
	}
	identityBytes, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(identityBytes) == 0 {
		return gapState{}, fmt.Errorf("objectstore: invalid gap object identity")
	}
	keyBytes, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || len(keyBytes) == 0 {
		return gapState{}, fmt.Errorf("objectstore: invalid gap object key")
	}
	gap := gapState{
		Identity:    string(identityBytes),
		Key:         string(keyBytes),
		FirstFailed: firstFailed.UTC(),
		Attempts:    attempts,
	}
	switch parts[0] {
	case "pending":
		if nextUnix <= 0 {
			return gapState{}, fmt.Errorf("objectstore: pending gap has no retry time")
		}
		gap.NextAttempt = time.Unix(nextUnix, 0).UTC()
	case "quarantined":
		if nextUnix != 0 {
			return gapState{}, fmt.Errorf("objectstore: quarantined gap has a retry time")
		}
		gap.Quarantined = true
	default:
		return gapState{}, fmt.Errorf("objectstore: invalid gap status")
	}
	return gap, nil
}

func objectDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

func seenRow(identity string) string {
	return seenPrefix + base64.RawURLEncoding.EncodeToString([]byte(identity))
}

func decodeIdentity(encoded string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("objectstore: invalid seen object identity")
	}
	return string(raw), nil
}
