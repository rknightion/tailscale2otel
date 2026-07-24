package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestModulePathMatchesReleaseVersion guards the one release failure this repo
// has already shipped twice over: a major version tagged against a module path
// that does not carry it.
//
// Go's semantic-import-versioning requires a module with a go.mod released at
// v2+ to have a module path ending /vN. release-please does NOT maintain that —
// `release-type: go` updates the manifest and the changelog and nothing else —
// so a major bump silently leaves go.mod behind. GoReleaser's `gomod.proxy: true`
// build then proxies the tagged module and Go rejects it.
//
// At v2.0.0 that lost the entire binaries job (#174): image, chart and notices
// published, while the archives, checksums, signatures and SBOMs did not, and
// re-running could not fix it because the tag itself carried the wrong path.
// #244 is the same cliff at v3.
//
// The timing is what makes this catchable. .release-please-manifest.json is
// bumped BY THE RELEASE PR, so on that branch the manifest already reads the new
// major while go.mod still reads the old one — this test fails there, on the one
// PR that must not merge unnoticed, rather than after the tag is cut and the
// damage is unfixable.
//
// Fix with: scripts/bump-module-major.sh
func TestModulePathMatchesReleaseVersion(t *testing.T) {
	root := filepath.Join("..", "..")

	raw, err := os.ReadFile(filepath.Join(root, ".release-please-manifest.json"))
	if err != nil {
		t.Fatalf("read release-please manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse release-please manifest: %v", err)
	}
	version, ok := manifest["."]
	if !ok {
		t.Fatalf("release-please manifest has no root (\".\") entry: %v", manifest)
	}
	releaseMajor, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if err != nil {
		t.Fatalf("parse major from manifest version %q: %v", version, err)
	}

	modPath := modulePath(t, filepath.Join(root, "go.mod"))
	moduleMajor := majorOfModulePath(modPath)

	// Three states are legitimate, and only the first is dangerous to miss.
	//
	//   module < release  — the #174 failure. The manifest has been bumped (this is
	//                       what the release PR does) and go.mod has not, so the tag
	//                       about to be cut will not match the path. FAIL.
	//   module == release — steady state on main between majors.
	//   module == release+1 — the pre-bump. The path has to move BEFORE the release
	//                       commit is tagged, so on main the module leads the last
	//                       released version by exactly one major until that release
	//                       lands. Legitimate, and the state this guard was written in.
	//
	// Anything further ahead is an overshoot: a path claiming a major nothing is
	// heading toward, which would break the eventual tag just as badly.
	switch {
	case moduleMajor < releaseMajor:
		t.Fatalf(`module path major is BEHIND the release version.

  go.mod module path: %s  (major v%d)
  release version:    %s  (major v%d)

Go requires a v2+ module's path to end in /vN, and release-please does not
maintain that. Tagging v%s against this path fails the GoReleaser binaries job —
exactly how v2.0.0 shipped with no archives, checksums or signatures (#174).

Fix it before the release PR merges:

    scripts/bump-module-major.sh %d`,
			modPath, moduleMajor, version, releaseMajor, version, releaseMajor)
	case moduleMajor > releaseMajor+1:
		t.Fatalf(`module path major is more than one ahead of the release version.

  go.mod module path: %s  (major v%d)
  release version:    %s  (major v%d)

One ahead is the legitimate pre-bump (the path must move before the major release
is tagged). More than one means the path claims a major nothing is heading toward,
which breaks the eventual tag the same way being behind does.`,
			modPath, moduleMajor, version, releaseMajor)
	}

	// The tool modules nest under the root path and pin it by version. A stale
	// suffix or major there fails the build only when that module is exercised,
	// which is a separate CI lane, so check them from here where it is cheap.
	for _, tool := range []string{"configcheck", "metricscatalog", "apidrift"} {
		gomod := filepath.Join(root, "tools", tool, "go.mod")
		if _, err := os.Stat(gomod); err != nil {
			continue
		}
		toolPath := modulePath(t, gomod)
		if want := modPath + "/tools/" + tool; toolPath != want {
			t.Errorf("tools/%s module path = %q, want %q", tool, toolPath, want)
		}
		body, err := os.ReadFile(gomod)
		if err != nil {
			t.Fatalf("read %s: %v", gomod, err)
		}
		// Both the require and the replace name the root module; neither may lag.
		// Checked against the MODULE's major, not the release's: during a pre-bump
		// the module leads the last release by one, and the tool modules must move
		// with the module path they point at, not with the manifest.
		if !strings.Contains(string(body), "require "+modPath+" v"+strconv.Itoa(moduleMajor)+".") {
			t.Errorf("tools/%s does not require %s at major v%d", tool, modPath, moduleMajor)
		}
		if !strings.Contains(string(body), "replace "+modPath+" => ../..") {
			t.Errorf("tools/%s does not replace %s with ../..", tool, modPath)
		}
	}
}

// modulePath returns the module path declared by a go.mod.
func modulePath(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("%s declares no module path", path)
	}
	return m[1]
}

// majorOfModulePath extracts the major version a module path encodes. A path
// with no /vN suffix is v1 (and v0, which shares the unsuffixed form — the
// distinction does not matter here, since neither may carry a suffix).
func majorOfModulePath(path string) int {
	m := regexp.MustCompile(`/v(\d+)$`).FindStringSubmatch(path)
	if m == nil {
		return 1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 1
	}
	return n
}
