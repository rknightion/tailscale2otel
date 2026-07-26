package flowlog

import "time"

// EventTimestamp returns the best completed-data timestamp carried by a flow
// record. End is preferred because a flow window is not complete until then;
// older/partial records fall back to start and finally the capture timestamp.
func EventTimestamp(log FlowLog) time.Time {
	if !log.End.IsZero() {
		return log.End
	}
	if !log.Start.IsZero() {
		return log.Start
	}
	return log.Logged
}

// CaptureTimestamp returns the control-plane capture timestamp carried by a
// flow record, when present.
func CaptureTimestamp(log FlowLog) time.Time {
	return log.Logged
}
