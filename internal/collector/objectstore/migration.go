package objectstore

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

const (
	legacyCursorKey  = "objectstore.flowlogs.cursor"
	legacySeenPrefix = "objectstore.flowlogs.seen/"
	legacyScanPrefix = "objectstore.flowlogs.scan/"
	legacyGapPrefix  = "objectstore.flowlogs.gap/"
)

// migrateLegacyState moves the pre-v1 flow/S3 checkpoint layout into the
// canonical scoped layout in one checkpoint transaction. Existing canonical
// entries win, making retries and partial manual restores idempotent.
func migrateLegacyState(
	store collector.CheckpointStore,
	legacyNamespace string,
	canonicalNamespace string,
) error {
	legacyRoot := ""
	if legacyNamespace != "" {
		legacyRoot = legacyNamespace + "/"
	}
	canonicalRoot := canonicalNamespace + "/"
	updates := map[string]time.Time{}
	var deletes []string

	for _, source := range store.Keys() {
		legacyKey, ok := strings.CutPrefix(source, legacyRoot)
		if !ok {
			continue
		}
		target, value, recognized, err := migrateLegacyRow(store, source, legacyKey)
		if err != nil {
			return err
		}
		if !recognized {
			continue
		}
		destination := canonicalRoot + target
		if _, exists := store.Get(destination); !exists {
			updates[destination] = value
		}
		deletes = append(deletes, source)
	}
	if len(updates) == 0 && len(deletes) == 0 {
		return nil
	}
	if err := collector.UpdateCheckpointBatch(store, updates, deletes); err != nil {
		return fmt.Errorf("objectstore: migrate legacy checkpoint state: %w", err)
	}
	return nil
}

func migrateLegacyRow(
	store collector.CheckpointStore,
	source string,
	legacyKey string,
) (string, time.Time, bool, error) {
	value, ok := store.Get(source)
	if !ok {
		return "", time.Time{}, false, fmt.Errorf(
			"objectstore: legacy checkpoint %q disappeared while migrating",
			source,
		)
	}
	switch {
	case legacyKey == legacyCursorKey:
		return cursorKey, value, true, nil
	case strings.HasPrefix(legacyKey, legacySeenPrefix):
		identity := strings.TrimPrefix(legacyKey, legacySeenPrefix)
		if identity == "" {
			return "", time.Time{}, false, fmt.Errorf("objectstore: invalid legacy seen checkpoint")
		}
		return seenRow(identity), value, true, nil
	case strings.HasPrefix(legacyKey, legacyScanPrefix):
		suffix := strings.TrimPrefix(legacyKey, legacyScanPrefix)
		if suffix == "" {
			return "", time.Time{}, false, fmt.Errorf("objectstore: invalid legacy scan checkpoint")
		}
		return scanPrefix + suffix, value, true, nil
	case strings.HasPrefix(legacyKey, legacyGapPrefix):
		gap, err := decodeLegacyGapRow(legacyKey, value)
		if err != nil {
			return "", time.Time{}, false, err
		}
		return encodeGapRow(gap), value, true, nil
	default:
		return "", time.Time{}, false, nil
	}
}

func decodeLegacyGapRow(row string, firstFailed time.Time) (gapState, error) {
	encoded, ok := strings.CutPrefix(row, legacyGapPrefix)
	if !ok {
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap checkpoint")
	}
	parts := strings.SplitN(encoded, "/", 4)
	if len(parts) != 4 {
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap checkpoint")
	}
	attempts, err := strconv.Atoi(parts[1])
	if err != nil || attempts < 1 {
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap attempt count")
	}
	nextUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap retry time")
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(key) == 0 {
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap object identity")
	}
	gap := gapState{
		Identity:    string(key),
		Key:         string(key),
		FirstFailed: firstFailed.UTC(),
		Attempts:    attempts,
	}
	switch parts[0] {
	case "pending":
		if nextUnix <= 0 {
			return gapState{}, fmt.Errorf("objectstore: legacy pending gap has no retry time")
		}
		gap.NextAttempt = time.Unix(nextUnix, 0).UTC()
	case "quarantined":
		if nextUnix != 0 {
			return gapState{}, fmt.Errorf("objectstore: legacy quarantined gap has a retry time")
		}
		gap.Quarantined = true
	default:
		return gapState{}, fmt.Errorf("objectstore: invalid legacy gap status")
	}
	return gap, nil
}
