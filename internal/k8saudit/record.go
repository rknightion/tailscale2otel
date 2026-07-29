// Package k8saudit decodes and processes Kubernetes API-audit events written by
// Tailscale's tsrecorder.
//
// The on-disk/S3 object is NOT upstream's sessionrecording.Event. It is a
// closed-source server-side wrapper, {stableID, event, timestamp}, whose schema
// is unversioned and undocumented; tsrecorder's server is not in the OSS repo.
// Proof it is a wrapper and not the upstream type: the OSS API-server proxy
// never populates Destination, yet every object in a live capture carries it.
// So this package declares its own types rather than importing upstream's, and
// decodes defensively.
package k8saudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventTypeKubernetesAPIRequest is the only discriminator value upstream can
// emit today. Anything else is schema drift, not a new record to guess at.
const EventTypeKubernetesAPIRequest = "kubernetes-api-request"

// ErrNotEvent marks a payload that is well-formed JSON but is not an audit
// event — most importantly an asciinema frame line, which shares an object with
// a .cast header and must not be counted as a decode failure.
var ErrNotEvent = errors.New("k8saudit: payload is not an audit event")

// Object is the wrapper tsrecorder writes, one per proxied API request.
type Object struct {
	StableID string `json:"stableID"`
	Event    Event  `json:"event"`
	// Timestamp is the wrapper's own human-readable stamp
	// ("2026-07-29 11:45:08 +0000 UTC"). It duplicates Event.Timestamp at
	// second resolution and is NOT RFC3339, so it is kept as a string and never
	// parsed; Event.Timestamp is the machine-readable one.
	Timestamp string `json:"timestamp"`
}

// Event mirrors upstream sessionrecording.Event as observed on the wire.
type Event struct {
	Type        string         `json:"type"`
	ID          string         `json:"id"`
	Timestamp   int64          `json:"timestamp"`
	UserAgent   string         `json:"userAgent"`
	Request     RequestInfo    `json:"request"`
	Kubernetes  KubernetesInfo `json:"kubernetes"`
	Source      Source         `json:"source"`
	Destination Destination    `json:"destination"`
}

// RequestInfo holds the raw HTTP request shape.
//
// Path carries the FULL query string, including URL-encoded exec command text
// (verified: ".../exec?command=sh&command=-c&command=clear%3B+%28bash..."). It
// must NEVER be emitted — KubernetesInfo.Path is the query-free equivalent and
// is the only path field safe to export.
type RequestInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Body is base64 in JSON because upstream types it []byte. It is read with
	// io.ReadAll and has no size cap upstream, so it is never emitted raw and is
	// only inspected under an explicit size limit.
	Body            []byte              `json:"body"`
	QueryParameters map[string][]string `json:"queryParameters"`
}

// KubernetesInfo is a copy of k8s.io/apiserver .../request.RequestInfo that
// upstream pasted WITHOUT json tags, so every field serializes under its bare
// PascalCase Go name while its camelCase siblings do not.
//
// The tags below spell those names out. They are NOT strictly required —
// encoding/json falls back to a case-insensitive match, so an untagged field or
// a camelCase tag decodes "APIGroup" just as well (verified 2026-07-29). What
// DOES break, silently and with a zero value, is a tag whose LETTERS differ:
// json:"api_group" never matches "APIGroup". That is the trap worth guarding
// against here, because snake_case is the spelling a Go developer reaches for
// by reflex and is what the rest of this repo's config types use. The tags are
// kept verbatim so the wire names are readable without consulting upstream.
type KubernetesInfo struct {
	IsResourceRequest bool     `json:"IsResourceRequest"`
	Path              string   `json:"Path"`
	Verb              string   `json:"Verb"`
	APIPrefix         string   `json:"APIPrefix"`
	APIGroup          string   `json:"APIGroup"`
	APIVersion        string   `json:"APIVersion"`
	Namespace         string   `json:"Namespace"`
	Resource          string   `json:"Resource"`
	Subresource       string   `json:"Subresource"`
	Name              string   `json:"Name"`
	Parts             []string `json:"Parts"`
	FieldSelector     string   `json:"FieldSelector"`
	LabelSelector     string   `json:"LabelSelector"`
}

type Source struct {
	Node       string   `json:"node"`
	NodeID     string   `json:"nodeID"`
	NodeTags   []string `json:"nodeTags,omitempty"`
	NodeUserID int64    `json:"nodeUserID,omitempty"`
	NodeUser   string   `json:"nodeUser,omitempty"`
}

type Destination struct {
	Node   string `json:"node"`
	NodeID string `json:"nodeID"`
}

// DecodeObject decodes one wrapper object, rejecting padding and non-event
// payloads without treating them as corruption.
func DecodeObject(line []byte) (Object, error) {
	var obj Object
	if err := json.Unmarshal(line, &obj); err != nil {
		return Object{}, fmt.Errorf("k8saudit: decode object: %w", err)
	}
	// "null", "{}" and "[]" all unmarshal into a zero Object without error. A
	// zero Object would emit a phantom log record with no verb and the zero
	// time, so require one load-bearing field.
	if obj.Event.Type == "" && obj.Event.ID == "" && obj.Event.Timestamp == 0 {
		return Object{}, fmt.Errorf("%w: no type, id or timestamp", ErrNotEvent)
	}
	return obj, nil
}

// EventTimestamp is when the request happened, at the 1s resolution upstream
// provides. There is no sub-second field anywhere in the schema.
func EventTimestamp(o Object) time.Time {
	if o.Event.Timestamp == 0 {
		return time.Time{}
	}
	return time.Unix(o.Event.Timestamp, 0).UTC()
}

// CaptureTimestamp is the same instant: tsrecorder records no separate ingest
// time, so freshness against capture is identical to freshness against the
// event. Returning the event time keeps the engine's freshness observer honest
// rather than reporting a zero capture lag.
func CaptureTimestamp(o Object) time.Time { return EventTimestamp(o) }
