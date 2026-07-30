package flowlog_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestValidate(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	opts := flowlog.ValidationOptions{
		Now:           func() time.Time { return now },
		MaxFutureSkew: 5 * time.Minute,
	}
	validConnection := flowlog.ConnectionCounts{
		Proto:   6,
		Src:     "100.64.0.1:443",
		Dst:     "[fd7a:115c:a1e0::1]:51820",
		TxPkts:  1,
		TxBytes: 2,
		RxPkts:  3,
		RxBytes: 4,
	}

	tests := []struct {
		name string
		log  flowlog.FlowLog
		want []flowlog.ViolationKind
	}{
		{
			name: "accepts optional fields and documented endpoint shapes",
			log: flowlog.FlowLog{
				VirtualTraffic: []flowlog.ConnectionCounts{validConnection},
				SubnetTraffic:  []flowlog.ConnectionCounts{{Proto: 255, Src: "host.example:0", Dst: "example-host:65535"}},
				ExitTraffic:    []flowlog.ConnectionCounts{{Proto: 0}},
				PhysicalTraffic: []flowlog.ConnectionCounts{{
					Proto: 253,
					Src:   "192.0.2.10:80",
					Dst:   "physical-host:1",
				}},
			},
		},
		{
			name: "rejects negative counters from every traffic class once",
			log: flowlog.FlowLog{
				VirtualTraffic:  []flowlog.ConnectionCounts{{TxPkts: -1}},
				SubnetTraffic:   []flowlog.ConnectionCounts{{TxBytes: -1}},
				ExitTraffic:     []flowlog.ConnectionCounts{{RxPkts: -1}},
				PhysicalTraffic: []flowlog.ConnectionCounts{{RxBytes: -1}},
			},
			want: []flowlog.ViolationKind{flowlog.ViolationNegativeCounters},
		},
		{
			name: "rejects inverted nonzero window",
			log: flowlog.FlowLog{
				Start: now.Add(time.Minute),
				End:   now,
			},
			want: []flowlog.ViolationKind{flowlog.ViolationInvertedWindow},
		},
		{
			name: "rejects malformed endpoint shapes",
			log: flowlog.FlowLog{VirtualTraffic: []flowlog.ConnectionCounts{
				{Src: "100.64.0.1", Dst: "host:abc"},
				{Src: "[fd7a:115c:a1e0::1:80", Dst: "host:65536"},
			}},
			want: []flowlog.ViolationKind{flowlog.ViolationInvalidEndpoint},
		},
		{
			name: "accepts endpoint port boundaries",
			log: flowlog.FlowLog{VirtualTraffic: []flowlog.ConnectionCounts{
				{Src: "host:0", Dst: "host:65535"},
			}},
		},
		{
			name: "rejects future timestamps strictly beyond skew",
			log: flowlog.FlowLog{
				Start:  now.Add(5*time.Minute + time.Nanosecond),
				End:    now.Add(5*time.Minute + time.Second),
				Logged: now.Add(6 * time.Minute),
			},
			want: []flowlog.ViolationKind{flowlog.ViolationFutureTimestamp},
		},
		{
			name: "accepts future timestamp at skew boundary",
			log:  flowlog.FlowLog{Logged: now.Add(5 * time.Minute)},
		},
		{
			name: "rejects protocols outside IANA byte range",
			log: flowlog.FlowLog{VirtualTraffic: []flowlog.ConnectionCounts{
				{Proto: -1},
				{Proto: 256},
			}},
			want: []flowlog.ViolationKind{flowlog.ViolationInvalidProtocol},
		},
		{
			name: "orders and deduplicates violations without carrying source data",
			log: flowlog.FlowLog{
				Start:  now.Add(6 * time.Minute),
				End:    now,
				Logged: now.Add(7 * time.Minute),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: -1, Src: "bad", TxPkts: -1},
				},
				SubnetTraffic: []flowlog.ConnectionCounts{
					{Proto: 256, Dst: "host:65536", RxBytes: -1},
				},
			},
			want: []flowlog.ViolationKind{
				flowlog.ViolationNegativeCounters,
				flowlog.ViolationInvertedWindow,
				flowlog.ViolationInvalidEndpoint,
				flowlog.ViolationFutureTimestamp,
				flowlog.ViolationInvalidProtocol,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flowlog.Validate(tt.log, opts)
			var gotKinds []flowlog.ViolationKind
			for _, violation := range got {
				gotKinds = append(gotKinds, violation.Kind)
				if reflect.TypeOf(violation).NumField() != 1 {
					t.Fatalf("Violation carries %d fields, want only safe Kind", reflect.TypeOf(violation).NumField())
				}
			}
			if !reflect.DeepEqual(gotKinds, tt.want) {
				t.Fatalf("Validate() kinds = %v, want %v", gotKinds, tt.want)
			}
		})
	}
}

func TestObserveDataQualityUsesOnlyClosedSourceAndReasonDimensions(t *testing.T) {
	rec := telemetrytest.New()
	flowlog.ObserveDataQuality(rec.Emitter(), "stream", []flowlog.Violation{
		{Kind: flowlog.ViolationInvalidEndpoint},
		{Kind: flowlog.ViolationFutureTimestamp},
	})

	points := rec.MetricPoints(flowlog.MetricDataQuality)
	if len(points) != 2 {
		t.Fatalf("data-quality points = %d, want 2", len(points))
	}
	for _, point := range points {
		if point.Attrs["source"] != "stream" {
			t.Errorf("source = %q, want stream", point.Attrs["source"])
		}
		if len(point.Attrs) != 2 {
			t.Errorf("attrs = %#v, want source and reason only", point.Attrs)
		}
	}

	rec = telemetrytest.New()
	flowlog.ObserveDataQuality(rec.Emitter(), "attacker-controlled", []flowlog.Violation{{Kind: "unbounded"}})
	point := rec.MetricPoints(flowlog.MetricDataQuality)[0]
	if point.Attrs["source"] != "other" || point.Attrs["reason"] != "other" {
		t.Fatalf("bounded attrs = %#v, want source=other reason=other", point.Attrs)
	}
}

func TestValidate_Defaults(t *testing.T) {
	now := time.Now()
	if got := flowlog.Validate(flowlog.FlowLog{Logged: now.Add(10 * time.Minute)}, flowlog.ValidationOptions{}); !reflect.DeepEqual(got, []flowlog.Violation{{Kind: flowlog.ViolationFutureTimestamp}}) {
		t.Fatalf("Validate() defaults = %v, want future timestamp violation", got)
	}
}
