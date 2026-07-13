package app

import (
	"log/slog"
	"strings"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
)

// migrateCheckpointKeys reconciles stored checkpoint keys with the current
// tailnet-namespacing shape. A window collector's key is "<name>" in
// single-tailnet mode and "<tailnet>/<name>" in multi mode (WithCheckpointNamespace),
// so a single<->multi transition or a tailnet rename changes it — and nothing
// migrated the old key, so the collector cold-started and re-emitted its overlap
// window as duplicates while the superseded key lingered forever (#105).
//
// For each window collector whose current key is absent, this adopts a single
// stored legacy key with the same collector basename (the value written under the
// old shape), preserving the high-water mark. When more than one legacy key could
// match (e.g. one un-namespaced key vs. several tailnets), it declines to guess
// and lets that collector cold-start. Any stored key matching no current
// collector after migration is logged (a removed collector or a renamed/removed
// tailnet) but left in place, so a temporary removal keeps its cursor.
//
// It is a no-op in steady state (every current key already present, no strays).
func (a *App) migrateCheckpointKeys(logger *slog.Logger) {
	type want struct{ key, name string }
	var wants []want
	current := map[string]struct{}{}
	multi := a.multiTailnet()
	for _, rt := range a.runtimes {
		ns := ""
		if multi {
			ns = rt.name
		}
		for _, e := range rt.registry.Entries() {
			wc, ok := e.Collector.(collector.WindowCollector)
			if !ok {
				continue
			}
			key := wc.Name()
			if ns != "" {
				key = ns + "/" + wc.Name()
			}
			wants = append(wants, want{key: key, name: wc.Name()})
			current[key] = struct{}{}
		}
	}
	if len(wants) == 0 {
		return
	}

	stored := map[string]struct{}{}
	for _, k := range a.store.Keys() {
		stored[k] = struct{}{}
	}

	claimed := map[string]bool{} // a legacy key can seed at most one collector
	for _, w := range wants {
		if _, ok := stored[w.key]; ok {
			continue // current key present — nothing to migrate
		}
		// A legacy candidate: a stored key with the same collector basename that is
		// not itself a desired current key and not already claimed.
		var cand string
		n := 0
		for k := range stored {
			if k == w.key || claimed[k] {
				continue
			}
			if _, isCurrent := current[k]; isCurrent {
				continue
			}
			if checkpointBasename(k) != w.name {
				continue
			}
			cand = k
			n++
		}
		if n != 1 {
			if n > 1 {
				logger.Warn("multiple legacy checkpoint keys match a collector after a namespace-shape change; "+
					"cold-starting it rather than guessing which cursor to adopt",
					"collector", w.name, "target_key", w.key)
			}
			continue
		}
		t, ok := a.store.Get(cand)
		if !ok {
			continue
		}
		if err := a.store.Set(w.key, t); err != nil {
			logger.Warn("failed to migrate checkpoint key", "from", cand, "to", w.key, "error", err)
			continue
		}
		if err := a.store.Delete(cand); err != nil {
			logger.Warn("migrated checkpoint key but failed to remove the old one", "old", cand, "error", err)
		}
		claimed[cand] = true
		delete(stored, cand)
		stored[w.key] = struct{}{}
		logger.Info("migrated checkpoint cursor after a tailnet namespace-shape change",
			"from", cand, "to", w.key, "high_water_mark", t)
	}

	for k := range stored {
		if _, ok := current[k]; ok {
			continue
		}
		logger.Warn("stale checkpoint key matches no registered collector; it will not be used "+
			"(a removed collector or a renamed/removed tailnet). Remove it from the checkpoint file to tidy up.",
			"key", k)
	}
}

// checkpointBasename returns the collector-name portion of a checkpoint key:
// "<tailnet>/<name>" -> "<name>", and a bare "<name>" -> "<name>". Collector
// names never contain "/", so the last segment is always the collector name.
func checkpointBasename(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}
