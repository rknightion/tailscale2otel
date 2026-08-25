// Package safefile reads configured local files through one held descriptor
// with explicit type and byte bounds.
package safefile

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrTooLarge   = errors.New("configured file exceeds byte limit")
	ErrNotRegular = errors.New("configured path is not a regular file")
)

type SymlinkPolicy bool

const (
	MaxSecretBytes     int64 = 64 << 10
	MaxPEMBytes        int64 = 1 << 20
	MaxConfigBytes     int64 = 4 << 20
	MaxCheckpointBytes int64 = 16 << 20
	MaxDatabaseBytes   int64 = 256 << 20
)

const (
	NoSymlink    SymlinkPolicy = false
	AllowSymlink SymlinkPolicy = true
)

// ReadRegular opens path once, validates the held descriptor, and reads at
// most max+1 bytes. AllowSymlink supports Kubernetes/Docker projected files;
// the resolved object is still required to be a regular file.
func ReadRegular(path string, max int64, policy SymlinkPolicy) ([]byte, error) {
	data, _, err := ReadRegularInfo(path, max, policy)
	return data, err
}

// ReadRegularInfo is ReadRegular plus metadata from the same held descriptor.
func ReadRegularInfo(path string, max int64, policy SymlinkPolicy) ([]byte, os.FileInfo, error) {
	if max <= 0 {
		return nil, nil, fmt.Errorf("safefile: invalid byte limit %d", max)
	}
	f, err := platformOpen(path, policy == AllowSymlink)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("safefile: inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("safefile: %s: %w", path, ErrNotRegular)
	}
	if info.Size() > max {
		return nil, nil, fmt.Errorf("safefile: %s (%d bytes, limit %d): %w", path, info.Size(), max, ErrTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, nil, fmt.Errorf("safefile: read %s: %w", path, err)
	}
	if int64(len(data)) > max {
		return nil, nil, fmt.Errorf("safefile: %s grew beyond limit %d: %w", path, max, ErrTooLarge)
	}
	return data, info, nil
}

// LoadX509KeyPair is the bounded-file equivalent of tls.LoadX509KeyPair.
func LoadX509KeyPair(certFile, keyFile string, max int64) (tls.Certificate, error) {
	certPEM, err := ReadRegular(certFile, max, AllowSymlink)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := ReadRegular(keyFile, max, AllowSymlink)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}
