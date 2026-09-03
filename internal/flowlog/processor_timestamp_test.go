package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// Flow records carry three times: start/end bound the window in which the
// traffic actually occurred, while logged is when the control plane captured
// the record. Against a live 3h capture, logged trailed start by a mean of 7.5s
// and a maximum of 852s, so timestamping the emitted log record with logged
// misplaces it on the time axis by an amount that varies per record and cannot
// be corrected downstream.
//
// The OTEL split is the fix: Timestamp is when the event happened (start),
// ObservedTimestamp is when it was seen (logged).
func TestProcess_LogTimestampPrefersWindowStart(t *testing.T) {
	var (
		start  = time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC)
		end    = time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC)
		logged = time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC)
	)

	tests := []struct {
		name             string
		start, end, logg time.Time
		wantTimestamp    time.Time
		wantObserved     time.Time
		// sdkStampsObserved marks the case where the record carries no capture
		// time: the emitter leaves ObservedTimestamp unset and the log SDK stamps
		// it at emit time, so the assertion is "populated by the SDK", not a value.
		sdkStampsObserved bool
	}{
		{
			name:  "all three present: start wins, logged observed",
			start: start, end: end, logg: logged,
			wantTimestamp: start,
			wantObserved:  logged,
		},
		{
			name: "start absent: falls back to window end",
			end:  end, logg: logged,
			wantTimestamp: end,
			wantObserved:  logged,
		},
		{
			name:          "start and end absent: falls back to logged",
			logg:          logged,
			wantTimestamp: logged,
			wantObserved:  logged,
		},
		{
			name:              "all absent: timestamp unset, SDK stamps observed",
			wantTimestamp:     time.Time{},
			sdkStampsObserved: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
			rec := telemetrytest.New()

			p.Process(flowlog.FlowLog{
				Logged: tc.logg,
				Start:  tc.start,
				End:    tc.end,
				NodeID: "nLaptop",
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxBytes: 1000},
				},
			}, rec.Emitter())

			recs := rec.LogRecords()
			if len(recs) != 1 {
				t.Fatalf("got %d log records, want 1", len(recs))
			}
			if got := recs[0].Timestamp.UTC(); !got.Equal(tc.wantTimestamp) {
				t.Errorf("Timestamp = %v, want %v", got, tc.wantTimestamp)
			}
			got := recs[0].ObservedTimestamp.UTC()
			switch {
			case tc.sdkStampsObserved:
				if got.IsZero() {
					t.Error("ObservedTimestamp is zero, want the SDK's emit-time stamp")
				}
			case !got.Equal(tc.wantObserved):
				t.Errorf("ObservedTimestamp = %v, want %v", got, tc.wantObserved)
			}
		})
	}
}

// The window a flow belongs to is load-bearing for anyone bucketing flow logs,
// and it is not recoverable from the record timestamp alone once start and end
// differ. Carry both explicitly.
func TestProcess_LogCarriesFlowWindow(t *testing.T) {
	var (
		start = time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC)
		end   = time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC)
	)

	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		Logged: end.Add(time.Minute),
		Start:  start,
		End:    end,
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxBytes: 1000},
		},
	}, rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	if got := recs[0].Attrs[semconv.AttrFlowWindowStart]; got != start.Format(time.RFC3339Nano) {
		t.Errorf("%s = %q, want %q", semconv.AttrFlowWindowStart, got, start.Format(time.RFC3339Nano))
	}
	if got := recs[0].Attrs[semconv.AttrFlowWindowEnd]; got != end.Format(time.RFC3339Nano) {
		t.Errorf("%s = %q, want %q", semconv.AttrFlowWindowEnd, got, end.Format(time.RFC3339Nano))
	}
}

// A record with no window must not emit empty window attributes — an absent
// attribute is honest, an empty one is noise.
func TestProcess_LogOmitsAbsentFlowWindow(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxBytes: 1000},
		},
	}, rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	for _, k := range []string{semconv.AttrFlowWindowStart, semconv.AttrFlowWindowEnd} {
		if _, ok := recs[0].Attrs[k]; ok {
			t.Errorf("attribute %s present on a record with no window", k)
		}
	}
}
