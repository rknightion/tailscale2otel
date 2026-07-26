// Package ingest defines the leaf contracts shared by ingestion paths.
package ingest

import "time"

// AcceptedEvent carries only bounded routing dimensions and timestamps. It is
// emitted after source-level validation and de-duplication, immediately after
// the record is handed to its processor. CaptureTime is optional; AcceptedAt
// defaults to the observer's wall clock when omitted.
type AcceptedEvent struct {
	Source      string
	Signal      string
	EventTime   time.Time
	CaptureTime time.Time
	AcceptedAt  time.Time
}

// AcceptedObserver observes a source record accepted for processing.
type AcceptedObserver func(AcceptedEvent)
