package entityage_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/entityage"
)

func TestBucketsSecondsAreSortedAndPositive(t *testing.T) {
	b := entityage.BucketsSeconds()
	if len(b) == 0 {
		t.Fatal("BucketsSeconds() is empty")
	}
	for i, v := range b {
		if v <= 0 {
			t.Errorf("bucket %d is %v, want > 0", i, v)
		}
		if i > 0 && v <= b[i-1] {
			t.Errorf("bucket %d (%v) is not strictly greater than bucket %d (%v)", i, v, i-1, b[i-1])
		}
	}
}

func TestBucketsSecondsIsDefensivelyCopied(t *testing.T) {
	first := entityage.BucketsSeconds()
	first[0] = -1
	second := entityage.BucketsSeconds()
	if second[0] == -1 {
		t.Fatal("BucketsSeconds() returns a shared slice; a caller mutated the package-level bounds")
	}
}

func TestSeconds(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		created time.Time
		want    float64
		wantOK  bool
	}{
		{"one day old", now.Add(-24 * time.Hour), 86400, true},
		{"zero time is unknown", time.Time{}, 0, false},
		{"future timestamp clamps to zero", now.Add(time.Hour), 0, true},
		{"same instant", now, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := entityage.Seconds(tc.created, now)
			if ok != tc.wantOK {
				t.Fatalf("Seconds(%v) ok = %v, want %v", tc.created, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("Seconds(%v) = %v, want %v", tc.created, got, tc.want)
			}
		})
	}
}
