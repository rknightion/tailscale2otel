//go:build linux || darwin

package ingresswal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNewRejectsNonRegularLockInode(t *testing.T) {
	t.Parallel()

	directory := filepath.Join(t.TempDir(), "wal")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(directory, lockName), 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	store, err := New(Options{Directory: directory, MaxBytes: 1 << 20, MaxEntries: 10})
	if store != nil {
		_ = store.Close()
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("New with FIFO lock error = %v, want ErrOwnership", err)
	}
}
