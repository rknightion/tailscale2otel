package geoip

import (
	"context"
	"log/slog"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// Fetcher is the narrow view of the MaxMind downloader the updater needs, so the
// schedule can be tested without an HTTP server. *Downloader satisfies it.
type Fetcher interface {
	Fetch(ctx context.Context, edition string) (DownloadResult, error)
}

// UpdaterOptions configures an Updater.
type UpdaterOptions struct {
	// DB is the database set to reload. Required.
	DB *DB
	// Fetcher downloads databases from MaxMind. Nil disables downloading, which
	// is the operator-managed path: something else (a geoipupdate cron, a
	// sidecar, a mounted volume) writes the files and the reload tick notices.
	Fetcher Fetcher
	// Editions are the MaxMind edition IDs to download. Ignored when Fetcher is nil.
	Editions []string
	// DownloadInterval is how often to ask MaxMind for a newer database. Zero
	// disables the repeat (the initial download at start still runs).
	DownloadInterval time.Duration
	// ReloadInterval is how often to re-stat the configured paths and hot-swap a
	// changed file. Zero disables it.
	ReloadInterval time.Duration
	// Logger receives the update loop's INFO/WARN lines. Nil uses slog.Default.
	Logger *slog.Logger
	// Emitter, when non-nil, receives self-observability metrics on each tick.
	// Nil disables emission; the admin status page reads Stats() either way.
	Emitter telemetry.Emitter
}

// Updater keeps a DB current: it downloads fresh databases from MaxMind (when
// configured) and hot-swaps any file that has changed on disk.
//
// The two schedules are deliberately independent, because they answer different
// questions. DownloadInterval is "has MaxMind published a newer build?" — a
// network question, answered with a conditional request, worth asking about
// daily since GeoLite2 rebuilds twice a week. ReloadInterval is "has the file on
// disk changed?" — a local stat, and the only thing that makes the
// operator-managed path work at all. Setting one does not imply the other.
type Updater struct {
	db               *DB
	fetcher          Fetcher
	editions         []string
	downloadInterval time.Duration
	reloadInterval   time.Duration
	logger           *slog.Logger
	emitter          telemetry.Emitter
}

// NewUpdater returns an Updater; call Run to start it.
func NewUpdater(opts UpdaterOptions) *Updater {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Updater{
		db:               opts.DB,
		fetcher:          opts.Fetcher,
		editions:         opts.Editions,
		downloadInterval: opts.DownloadInterval,
		reloadInterval:   opts.ReloadInterval,
		logger:           logger,
		emitter:          opts.Emitter,
	}
}

// Run drives the update loop until ctx is canceled. It never returns an error:
// a failed download or reload is logged and retried on the next tick, because
// degraded enrichment must never become a degraded exporter.
func (u *Updater) Run(ctx context.Context) {
	// Download at start rather than waiting a whole interval: a fresh container
	// would otherwise run a full day with no database at all.
	u.update(ctx)

	var downloadC, reloadC <-chan time.Time
	if u.downloadInterval > 0 && u.fetcher != nil {
		t := time.NewTicker(u.downloadInterval)
		defer t.Stop()
		downloadC = t.C
	}
	if u.reloadInterval > 0 {
		t := time.NewTicker(u.reloadInterval)
		defer t.Stop()
		reloadC = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-downloadC:
			u.update(ctx)
		case <-reloadC:
			u.reload()
			u.report()
		}
	}
}

// update runs one download pass (when a fetcher is configured) and then reloads
// whatever changed on disk.
func (u *Updater) update(ctx context.Context) {
	if u.fetcher != nil {
		for _, edition := range u.editions {
			res, err := u.fetcher.Fetch(ctx, edition)
			u.db.ObserveDownload(u.emitter, edition, res, err)
			switch {
			case err != nil:
				// WARN, not ERROR: the previously installed database keeps
				// serving, so this degrades freshness, not availability.
				u.logger.Warn("geoip database download failed; keeping the installed database",
					"edition", edition, "error", err)
			case res.Updated:
				u.logger.Info("geoip database updated",
					"edition", edition, "path", res.Path, "build_time", res.BuildTime)
			default:
				u.logger.Debug("geoip database is already current", "edition", edition)
			}
		}
	}
	u.reload()
	u.report()
}

func (u *Updater) reload() {
	changed, err := u.db.Reload()
	if err != nil {
		u.logger.Warn("geoip database reload failed; keeping the loaded database", "error", err)
		return
	}
	if changed {
		s := u.db.Stats()
		u.logger.Info("geoip databases reloaded",
			"country_build_time", s.CountryBuildTime, "asn_build_time", s.ASNBuildTime)
	}
}

func (u *Updater) report() {
	if u.emitter != nil {
		u.db.Report(u.emitter)
	}
}
