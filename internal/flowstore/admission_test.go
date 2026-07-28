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
	// Recent orders by event time (#301), not by which Record() call happened
	// last. "future skew limit" carries the latest admitted event time
	// (now+5m) and must rank newest even though the zero-timestamp record —
	// stamped to `now` — was ingested after it. This is the future-skew case
	// #301's acceptance criteria call out as composing with A12: admission
	// already decided the future-stamped row is legitimate, so ordering must
	// respect its stamped time rather than silently falling back to
	// ingestion order.
	recent := m.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("Recent(3) = %d entries, want 3", len(recent))
	}
	if want := now.Add(5 * time.Minute); !recent[0].Time.Equal(want) {
		t.Errorf("newest by event time = %v, want %v (future skew limit)", recent[0].Time, want)
	}
	// The zero-timestamp record must still have been stamped to `now`,
	// findable among the three regardless of its rank.
	var sawStamped bool
	for _, r := range recent {
		if r.Time.Equal(now) {
			sawStamped = true
		}
	}
	if !sawStamped {
		t.Errorf("no recent row carries the zero-timestamp record's stamped time %v: %+v", now, recent)
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
