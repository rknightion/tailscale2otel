// Package audit defines the Tailscale configuration audit log record types and
// (in processor.go) the conversion to OTEL log records and counters. Both the
// polling collector and the streaming receiver decode into these types.
package audit

import (
	"encoding/json"
	"time"
)

// ConfigurationResponse is the GET /tailnet/{tailnet}/logging/configuration body.
type ConfigurationResponse struct {
	Version string  `json:"version"`
	Tailnet string  `json:"tailnet"`
	Logs    []Event `json:"logs"`
}

// Event is one configuration audit event.
type Event struct {
	EventTime    time.Time `json:"eventTime"`
	Type         string    `json:"type"`
	EventGroupID string    `json:"eventGroupID"`
	Origin       string    `json:"origin"`
	Actor        Actor     `json:"actor"`
	Target       Target    `json:"target"`
	Action       string    `json:"action"`
	// Old and New are polymorphic: the Tailscale API renders them as a JSON
	// string, object, array, number, bool, or null depending on the property.
	// They are kept raw and rendered to a string attribute at processing time.
	Old           json.RawMessage `json:"old"`
	New           json.RawMessage `json:"new"`
	ActionDetails string          `json:"actionDetails"`
	Error         string          `json:"error"`
}

// Actor identifies who performed the action.
type Actor struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	LoginName   string   `json:"loginName"`
	DisplayName string   `json:"displayName"`
	Tags        []string `json:"tags"`
}

// Target identifies what was modified.
type Target struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Property    string `json:"property"`
	IsEphemeral bool   `json:"isEphemeral"`
}
