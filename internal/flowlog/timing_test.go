package flowlog

import (
	"testing"
	"time"
)

func TestEventTimestampPrecedence(t *testing.T) {
	start := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	logged := end.Add(2 * time.Second)

	for _, tc := range []struct {
		name string
		log  FlowLog
		want time.Time
	}{
		{"completed window", FlowLog{Start: start, End: end, Logged: logged}, end},
		{"start fallback", FlowLog{Start: start, Logged: logged}, start},
		{"capture fallback", FlowLog{Logged: logged}, logged},
		{"unknown", FlowLog{}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EventTimestamp(tc.log); !got.Equal(tc.want) {
				t.Fatalf("EventTimestamp() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCaptureTimestampUsesLogged(t *testing.T) {
	logged := time.Date(2026, 7, 26, 10, 0, 7, 0, time.UTC)
	if got := CaptureTimestamp(FlowLog{Logged: logged}); !got.Equal(logged) {
		t.Fatalf("CaptureTimestamp() = %v, want %v", got, logged)
	}
}
