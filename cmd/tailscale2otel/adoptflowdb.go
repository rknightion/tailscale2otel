package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore/sqlitestore"
)

// adoptFlowDBTimeout bounds the whole adoption. It is generous because the
// schema migration on a large multi-day database is the slow part, and this is
// a one-shot operator command rather than anything on a serving path.
const adoptFlowDBTimeout = 5 * time.Minute

// runAdoptFlowDB claims a pre-hardening flow database for one configured
// tailnet and moves it to the name the store reads.
//
// The tailnet is named on the command line rather than inferred, and that is
// the entire point of the mode. The legacy `flows-<slug>.db` filename is
// attacker-influenceable and carries no proof of which tailnet its user
// identities belong to, so the service refuses to adopt one on its own; the
// operator naming the tailnet IS the ownership assertion the filename cannot
// make. Requiring the name to match a configured tailnet catches the typo that
// would otherwise silently adopt nothing and look like success.
func runAdoptFlowDB(configPath, tailnet string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if cfg == nil {
		fmt.Fprintf(stderr, "adopt-flow-db: config invalid: %v\n", err)
		return 1
	}

	dir := cfg.Flows.Store.Directory
	if dir == "" {
		fmt.Fprintln(stderr, "adopt-flow-db: flows.store.directory is not configured, so there is no persistent flow database to adopt")
		return 1
	}

	names := configuredTailnetNames(cfg)
	if !containsString(names, tailnet) {
		fmt.Fprintf(stderr, "adopt-flow-db: %q is not a configured tailnet; this config has: %s\n", tailnet, strings.Join(names, ", "))
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), adoptFlowDBTimeout)
	defer cancel()

	res, err := sqlitestore.AdoptLegacyDatabase(ctx, dir, tailnet)
	if err != nil {
		fmt.Fprintf(stderr, "adopt-flow-db: %v\n", err)
		return 1
	}
	if !res.Adopted {
		fmt.Fprintf(stdout, "adopt-flow-db: nothing to adopt for tailnet %q; no legacy database at %s\n", tailnet, res.Legacy)
		return 0
	}
	fmt.Fprintf(stdout, "adopt-flow-db: adopted %s as %s for tailnet %q, keeping %d flow rows\n", res.Legacy, res.Path, tailnet, res.Rows)
	return 0
}

// configuredTailnetNames is the set of names the app would build runtimes for,
// which is what the flow store derives its filename from.
func configuredTailnetNames(cfg *config.Config) []string {
	resolved := cfg.ResolvedTailnets()
	out := make([]string, 0, len(resolved))
	for _, rt := range resolved {
		out = append(out, rt.Name)
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
