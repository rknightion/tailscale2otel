package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegularRejectsOversizeBeforeAllocationGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1025)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegular(path, 1024, AllowSymlink); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadRegular error = %v, want ErrTooLarge", err)
	}
}

func TestReadRegularProjectedSymlinkPolicy(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "..data-token")
	link := filepath.Join(dir, "token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRegular(link, 64, AllowSymlink); err != nil || string(got) != "secret" {
		t.Fatalf("projected symlink read = %q, %v", got, err)
	}
	if _, err := ReadRegular(link, 64, NoSymlink); err == nil {
		t.Fatal("NoSymlink accepted a symlink")
	}
}
