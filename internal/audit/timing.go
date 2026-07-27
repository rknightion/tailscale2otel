package audit

import "time"

// EventTimestamp returns the best event timestamp carried by a configuration
// audit event.
//
// EventTime is when the change happened and is what every ingestion path
// timestamps its log record with. Logged (the publisher's delivery timestamp,
// present only on the object-store export) is the fallback purely so a record
// that somehow arrives without an event time is still placed in time rather than
// at the zero instant, which would sort before every real record.
func EventTimestamp(ev Event) time.Time {
	if !ev.EventTime.IsZero() {
		return ev.EventTime
	}
	return ev.Logged
}

// CaptureTimestamp returns the control-plane capture timestamp carried by an
// audit event, when present. Only the object-store export carries one.
func CaptureTimestamp(ev Event) time.Time { return ev.Logged }
