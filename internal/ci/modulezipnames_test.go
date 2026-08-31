package ci_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoPathsAreValidInAModuleZip pins the defect that broke every release
// candidate's binaries job from 2026-08-30 until 2026-08-31.
//
// The Go module proxy rejects a module whose zip contains a path with a
// non-ASCII character, and it does so at sum.golang.org rather than at build
// time. The whole repository is inside the module, so a Backlog milestone
// titled with an em dash produced a filename the proxy refused:
//
//	not found: create zip: backlog/milestones/m-1 - wave-1-—-bugs-...md:
//	  malformed file path: invalid char '—'
//
// Nothing local failed. `just check` was green, the tag was cut, the image
// published, and only `goreleaser` — which rebuilds from proxy.golang.org
// because .goreleaser.yaml sets gomod.proxy: true — hit it, five minutes into
// wait-for-module-proxy.sh, looking exactly like the ordinary sumdb ingestion
// lag that script exists to absorb. The script's own advice ("re-run this job,
// no code change is needed") is wrong for this cause and sent every reader
// down the wrong path.
//
// Backlog derives filenames from titles the CLI accepts without complaint, so
// this is reachable by anyone naming a milestone, and it is silent until a
// release. Keep the check on the whole tree, not just backlog/.
func TestRepoPathsAreValidInAModuleZip(t *testing.T) {
	root := repoDir

	var bad []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			// .git holds packed refs and object names this rule does not govern,
			// and module zips never contain it.
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		for _, r := range rel {
			if r > 127 {
				bad = append(bad, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}

	if len(bad) > 0 {
		t.Errorf("%d repository path(s) carry a non-ASCII character, which makes the module "+
			"unzippable on proxy.golang.org and fails every release's goreleaser job:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}
