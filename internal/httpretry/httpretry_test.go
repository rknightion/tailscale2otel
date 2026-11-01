package httpretry

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			header string
			want   time.Duration
		}{
			{"", 0}, {"garbage", 0}, {"0", 0}, {"-1", 0}, {"42", 42 * time.Second},
			{"-9223372036854775808", 0},
			{"-9223372037", 0},
			{"9223372036854775807", time.Duration(1<<63 - 1)},
			{"9223372036", 9223372036 * time.Second},
			{"9223372037", time.Duration(1<<63 - 1)},
			{time.Now().Add(2 * time.Minute).Format(http.TimeFormat), 2 * time.Minute},
			{time.Now().Add(-time.Minute).Format(http.TimeFormat), 0},
		} {
			if got := RetryAfter(tc.header); got != tc.want {
				t.Fatalf("RetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		}
	})
}

func TestComputeBackoff(t *testing.T) {
	for _, delay := range []time.Duration{2 * time.Second, 8 * time.Second, 10 * time.Second} {
		for _, rnd := range []float64{0, .5, math.Nextafter(1, 0)} {
			sleep, next := ComputeBackoff(delay, 10*time.Second, rnd)
			if sleep < delay/2 || sleep > delay {
				t.Fatalf("sleep = %v outside [%v, %v]", sleep, delay/2, delay)
			}
			if rnd == 0 && sleep != delay/2 {
				t.Fatalf("zero jitter sleep = %v, want %v", sleep, delay/2)
			}
			if rnd == .5 && sleep != delay*3/4 {
				t.Fatalf("midpoint jitter sleep = %v, want %v", sleep, delay*3/4)
			}
			want := min(delay*2, 10*time.Second)
			if next != want {
				t.Fatalf("next = %v, want %v", next, want)
			}
		}
	}
}

func TestLimiterWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		waiter := NewWaiter(2)
		start := time.Now()
		for i := range 3 {
			if err := waiter.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got, want := time.Since(start), time.Duration(i)*500*time.Millisecond; got != want {
				t.Fatalf("wait %d elapsed = %v, want %v", i, got, want)
			}
		}
		// A long idle refills only the single-token burst, not an unbounded balance.
		time.Sleep(10 * time.Second)
		start = time.Now()
		for i := range 2 {
			if err := waiter.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got, want := time.Since(start), time.Duration(i)*500*time.Millisecond; got != want {
				t.Fatalf("after idle wait %d elapsed = %v, want %v", i, got, want)
			}
		}
	})
}

func TestLimiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		waiter := NewWaiter(1)
		if err := waiter.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := waiter.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(start); elapsed != 100*time.Millisecond {
			t.Fatalf("cancellation took %v", elapsed)
		}
		// An already-canceled context must not consume the freshly refilled token.
		time.Sleep(time.Second)
		canceled, stop := context.WithCancel(context.Background())
		stop()
		if err := waiter.Wait(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait = %v, want canceled", err)
		}
		start = time.Now()
		if err := waiter.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("canceled waiter consumed token: %v", elapsed)
		}
	})
}

func TestNewWaiterUnlimited(t *testing.T) {
	for _, rate := range []float64{0, -1} {
		if got := NewWaiter(rate); got != nil {
			t.Fatalf("NewWaiter(%v) = %v, want nil", rate, got)
		}
	}
}
