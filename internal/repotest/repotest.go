// Package repotest locates the repository root for tests that read files
// outside their own package directory.
//
// It walks up from the working directory rather than using runtime.Caller,
// because under go build -trimpath runtime.Caller reports a module-relative
// path that does not exist on disk. go test runs each test binary with the
// package directory as its working directory. Like telemetrytest, the package
// does not import testing; callers decide how to fail.
package repotest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Root returns the absolute path of the nearest ancestor of the working
// directory that contains a go.mod file. The repository root module is the
// first one reached from any package of the root module.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat go.mod in %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found in any parent of the working directory")
		}
		dir = parent
	}
}
