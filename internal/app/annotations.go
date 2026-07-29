package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rknightion/tailscale2otel/v3/internal/annotations"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// defaultAnnotationStateFile is the dedupe set's filename when the operator did
// not pick one. It sits BESIDE the checkpoint file rather than inside it: the
// window pollers rewrite the checkpoint file on every tick and migrateCheckpointKeys
// walks its keys at startup, neither of which should have thousands of
// annotation hashes in scope.
const defaultAnnotationStateFile = "annotations.json"

// startAnnotator builds the Grafana annotation writer and PROVES its token by
// writing the startup marker synchronously.
//
// It returns an error only when the feature is configured and cannot work; the
// caller must treat that as fatal. When grafana_annotations.url is unset this
// assigns nothing and returns nil, and every later call site works unchanged
// because a nil *annotations.Annotator is a functioning no-op.
func (a *App) startAnnotator(ctx context.Context, cfg *config.Config, version string) error {
	if !cfg.GrafanaAnnotations.Enabled() {
		return nil
	}
	gc := cfg.GrafanaAnnotations

	names := make([]string, 0, len(a.runtimes))
	for _, rt := range cfg.Tailnets {
		names = append(names, rt.Name)
	}

	writer, err := annotations.Start(ctx, annotations.Options{
		Config: annotations.Config{
			Client: annotations.ClientConfig{
				URL:          strings.TrimSpace(gc.URL),
				Token:        gc.Token.Reveal(),
				DashboardUID: gc.DashboardUID,
				Timeout:      gc.Timeout.D(),
			},
			Categories: map[annotations.Category]annotations.CategoryConfig{
				annotations.CategoryConfigChange: {
					Enabled: gc.Categories.ConfigChange.Enabled,
					Rollup:  gc.Categories.ConfigChange.Rollup,
				},
				annotations.CategoryExpiry: {
					Enabled: gc.Categories.Expiry.Enabled,
					Rollup:  gc.Categories.Expiry.Rollup,
				},
			},
			RollupInterval:  gc.RollupInterval.D(),
			DedupeRetention: gc.DedupeRetention.D(),
			StateFile:       annotationStateFile(gc.StateFile, a.checkpointPath),
			QueueSize:       gc.QueueSize,
			MaxPerMinute:    gc.MaxPerMinute,
			ExtraTags:       gc.ExtraTags,
		},
		// The BASE process emitter, never a teed one: self-obs counters emitted
		// through the tee would be offered straight back to the recorder.
		Emitter: a.procEmitter,
		// pii_filter is applied INSIDE otelEmitter, and the tee wraps outside
		// it, so the records the recorder sees are raw. Passing the categories
		// here is what stops a value suppressed from OTLP being published to
		// Grafana anyway (#518).
		PIIFilter: piiCategories(cfg.PIIFilter),
		Logger:    withComponent(a.logger, compAnnotations),
		Version:   version,
		Tailnets:  names,
		StartedAt: a.startTime,
	})
	if err != nil {
		return fmt.Errorf("grafana annotations: %w", err)
	}
	a.annotator = writer
	a.logger.Info("grafana annotation writer started",
		"url", gc.URL,
		"dashboard_uid", gc.DashboardUID,
		"max_per_minute", gc.MaxPerMinute,
		"state_file", annotationStateFile(gc.StateFile, a.checkpointPath))
	return nil
}

// annotationStateFile resolves where the dedupe set lives. An explicit
// state_file wins; otherwise it lands next to the checkpoint file, so a
// deployment that already mounted a volume for checkpoints gets restart-safe
// dedupe with no extra configuration.
//
// An empty checkpoint path means checkpoints themselves are in memory (no
// writable location was found), so there is nowhere honest to put this either:
// returning "" selects the memory-only degraded mode rather than inventing a
// path in the working directory that a container would silently lose anyway.
func annotationStateFile(configured, checkpointPath string) string {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		return trimmed
	}
	if checkpointPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(checkpointPath), defaultAnnotationStateFile)
}
