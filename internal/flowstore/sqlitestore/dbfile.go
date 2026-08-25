package sqlitestore

import (
	"fmt"
	"os"
)

// verifyDatabaseGuard proves that path still names the same regular file held
// by guard. The platform opener supplies no-follow and race-safe create
// semantics; this comparison is repeated after SQLite establishes its first
// connection and again after migration before the guard is released.
func verifyDatabaseGuard(path string, guard *os.File) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("sqlitestore: inspect database path %s: %w", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("sqlitestore: %s is not a regular database file", path)
	}
	guardInfo, err := guard.Stat()
	if err != nil {
		return fmt.Errorf("sqlitestore: inspect held database %s: %w", path, err)
	}
	if !guardInfo.Mode().IsRegular() || !os.SameFile(pathInfo, guardInfo) {
		return fmt.Errorf("sqlitestore: database path %s changed while opening; refusing unsafe replacement", path)
	}
	return nil
}
