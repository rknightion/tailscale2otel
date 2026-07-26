// Package objectstore defines the provider-neutral read-only object-store
// contract used by durable ingestion collectors.
package objectstore

import (
	"context"
	"io"
	"time"
)

// Object is an object returned from a prefix listing.
//
// Identity is the stable opaque value used to retrieve the object and persist
// durable state. Key is the lexicographically ordered logical object name used
// for listing and scan positions.
type Object struct {
	Identity     string
	Key          string
	Size         int64
	LastModified time.Time
}

// ListResult is one bounded listing result. Truncated reports that the backend
// stopped at the requested local limit while more objects remain.
type ListResult struct {
	Objects   []Object
	Truncated bool
}

// Backend is a read-only object-store backend.
type Backend interface {
	List(ctx context.Context, prefix, startAfter string, limit int) (ListResult, error)
	Get(ctx context.Context, identity string) (io.ReadCloser, error)
}
