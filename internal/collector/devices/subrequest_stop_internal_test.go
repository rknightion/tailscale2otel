package devices

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

func TestSystemicSubrequestStopLatchesAfterThreshold(t *testing.T) {
	var stop systemicSubrequestStop
	denial := &tsapi.StatusError{Code: 403}
	for range systemicStopAfter {
		stop.observe(denial)
	}
	if stop.allowed(context.Background()) {
		t.Fatal("threshold did not stop new requests")
	}
	// Requests already in flight can complete after the threshold trips. Their
	// result cannot restart a kind that was stopped for the rest of this tick.
	stop.observe(nil)
	if stop.allowed(context.Background()) {
		t.Fatal("late success reopened the stopped request kind")
	}
}
