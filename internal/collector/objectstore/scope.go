package objectstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// CheckpointScope isolates durable object-store state by tenant, provider,
// signal, and physical feed.
type CheckpointScope struct {
	Tailnet  string
	Provider string
	Signal   string
	Feed     string
}

// FeedID derives an opaque stable feed identity without persisting endpoint,
// bucket, prefix, or other operator-controlled values.
func FeedID(parts ...string) string {
	digest := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(part))
	}
	return hex.EncodeToString(digest.Sum(nil)[:12])
}

// Namespace returns the canonical v1 checkpoint namespace.
func (s CheckpointScope) Namespace() (string, error) {
	if s.Tailnet == "" {
		return "", fmt.Errorf("objectstore: checkpoint scope tailnet is required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "provider", value: s.Provider},
		{name: "signal", value: s.Signal},
		{name: "feed", value: s.Feed},
	} {
		name, value := field.name, field.value
		if value == "" {
			return "", fmt.Errorf("objectstore: checkpoint scope %s is required", name)
		}
		if strings.ContainsAny(value, "/\\") {
			return "", fmt.Errorf("objectstore: checkpoint scope %s contains a path separator", name)
		}
	}
	tailnet := base64.RawURLEncoding.EncodeToString([]byte(s.Tailnet))
	return "objectstore/v1/" + tailnet + "/" + s.Provider + "/" + s.Signal + "/" + s.Feed, nil
}
