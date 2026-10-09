package config

import (
	"os"
	"path/filepath"
	"strings"
)

// readOnlyMount reports whether the filesystem mount holding path is mounted
// read-only. It is the only OS-edge seam in the kubelet-projection check, a
// package variable so tests can simulate a read-only mount without one.
// Implemented per platform (kubemount_linux.go / kubemount_other.go).
var readOnlyMount = platformReadOnlyMount

// kubeletDataLink is the symlink the kubelet's AtomicWriter swaps atomically
// to publish a new ConfigMap/Secret/projected-volume payload.
const kubeletDataLink = "..data"

// kubeletProjectedReadOnly reports whether path is a file the kubelet's
// AtomicWriter projected into a read-only volume (ConfigMap, Secret,
// downwardAPI or projected). Such a file's mode comes from the volume's
// defaultMode, and the mount is read-only, so the operator cannot chmod it
// and a non-root uid cannot read it at 0400/0600 unless fsGroup/runAsUser
// line up. The group/other-readable advisory has no remedy there.
//
// Deliberately conservative: every condition must hold, and any error means
// "no", so the advisory keeps firing for anything that is not unmistakably
// this layout.
//
//   - the directory holding path (or, if path names a file inside ..data
//     directly, the directory above that) has a "..data" SYMLINK;
//   - ..data resolves to a sibling directory whose name starts with ".."
//     (the AtomicWriter's timestamped "..2006_01_02_15_04_05.<n>" dir);
//   - path, fully resolved, lies inside that directory;
//   - the mount holding the resolved file is read-only.
func kubeletProjectedReadOnly(path string) bool {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == kubeletDataLink {
		dir = filepath.Dir(dir)
	}
	dataLink := filepath.Join(dir, kubeletDataLink)
	fi, err := os.Lstat(dataLink)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	linkTarget, err := os.Readlink(dataLink)
	if err != nil || !strings.HasPrefix(filepath.Base(linkTarget), "..") {
		return false
	}
	dataDir, err := filepath.EvalSymlinks(dataLink)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dataDir, target)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	ro, err := readOnlyMount(target)
	return err == nil && ro
}
