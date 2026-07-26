package objectstore

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
)

const scanPrefix = "objectstore.flowlogs.scan/"
const scanStartMarker = "-"

type scanState struct {
	Positions map[string]string
	StaleKeys []string
}

func loadScanState(cp collector.CheckpointStore, configuredBasePrefix string) (scanState, error) {
	state := scanState{Positions: map[string]string{}}
	for _, row := range cp.Keys() {
		encoded, ok := strings.CutPrefix(row, scanPrefix)
		if !ok {
			continue
		}
		encodedPrefix, encodedKey, ok := strings.Cut(encoded, "/")
		if !ok || encodedPrefix == "" || encodedKey == "" || strings.Contains(encodedKey, "/") {
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
		if !isConfiguredDayPrefix(prefix, configuredBasePrefix) {
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

func isConfiguredDayPrefix(prefix, configuredBasePrefix string) bool {
	base := strings.Trim(configuredBasePrefix, "/")
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
