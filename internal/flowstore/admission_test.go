package flowstore

import (
	"reflect"
	"testing"
	"time"
)

func TestMemory_RecordDoesNotMutateStateOutsideRetentionWindow(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 30, 0, time.UTC)

	for _, tc := range []struct {
		name      string
		at        time.Time
		admission Admission
	}{
		{name: "future", at: now.Add(6 * time.Minute), admission: AdmissionFuture},
		{name: "expired", at: now.Add(-4 * time.Minute), admission: AdmissionExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemory(3, WithClock(func() time.Time { return now }))
			m.Record(testObservation(now.Add(-2 * time.Minute)))
			m.Record(testObservation(now.Add(-time.Minute)))
			m.Record(testObservation(now))

			before := memorySnapshot(m, now)
			if got := m.RecordResult(testObservation(tc.at)); got != tc.admission {
				t.Fatalf("RecordResult() = %v, want %v", got, tc.admission)
			}
			after := memorySnapshot(m, now)

			if !reflect.DeepEqual(after, before) {
				t.Fatalf("state changed after %s observation (-want +got):\n- %#v\n+ %#v", tc.name, before, after)
			}

			m.Record(testObservation(tc.at))
			if afterLegacy := memorySnapshot(m, now); !reflect.DeepEqual(afterLegacy, before) {
				t.Fatalf("Record changed state after %s observation (-want +got):\n- %#v\n+ %#v", tc.name, before, afterLegacy)
			}
		})
	}
}

func TestMemory_RecordResultAcceptsRetentionAndFutureBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 30, 0, time.UTC)
	m := NewMemory(3, WithClock(func() time.Time { return now }))

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{name: "oldest retained", at: now.Add(-3 * time.Minute)},
		{name: "future skew limit", at: now.Add(5 * time.Minute)},
		{name: "zero timestamp", at: time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.RecordResult(testObservation(tc.at)); got != AdmissionAccepted {
				t.Fatalf("RecordResult() = %v, want %v", got, AdmissionAccepted)
			}
		})
	}

	if got, want := m.Stats().Observations, int64(3); got != want {
		t.Fatalf("observations = %d, want %d", got, want)
	}
	if got := m.Recent(1)[0].Time; !got.Equal(now) {
		t.Errorf("zero timestamp recorded at %v, want %v", got, now)
	}
}

func TestWithMaxFutureSkewNegativeNormalizesToZero(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	m := NewMemory(1,
		WithClock(func() time.Time { return now }),
		WithMaxFutureSkew(-time.Minute),
	)

	if got := m.RecordResult(testObservation(now.Add(time.Nanosecond))); got != AdmissionFuture {
		t.Fatalf("RecordResult() = %v, want %v", got, AdmissionFuture)
	}
}

type testMemorySnapshot struct {
	stats  Stats
	recent []Recent
	query  Result
}

func memorySnapshot(m *Memory, now time.Time) testMemorySnapshot {
	return testMemorySnapshot{
		stats:  m.Stats(),
		recent: m.Recent(MaxRecent),
		query:  m.Query(Query{Start: now.Add(-time.Hour), End: now.Add(time.Hour), TopN: -1}),
	}
}

func testObservation(at time.Time) Observation {
	return Observation{
		Time:        at,
		TrafficType: "virtual",
		Transport:   "tcp",
		SrcNode:     "source",
		DstNode:     "destination",
		Counts:      Counts{TxBytes: 1, Flows: 1},
	}
}
