package ringbuf

import (
	"sync"
	"testing"
)

func TestRing_EmptyIsSafe(t *testing.T) {
	r := New[int](3)
	if got := r.Len(); got != 0 {
		t.Errorf("Len() on empty = %d, want 0", got)
	}
	if got := r.Cap(); got != 3 {
		t.Errorf("Cap() = %d, want 3", got)
	}
	if got := r.Values(); len(got) != 0 {
		t.Errorf("Values() on empty = %v, want empty", got)
	}
}

func TestRing_KeepsOrderUnderCapacity(t *testing.T) {
	r := New[int](5)
	r.Add(10)
	r.Add(20)
	r.Add(30)
	want := []int{10, 20, 30}
	got := r.Values()
	if !equalInts(got, want) {
		t.Errorf("Values() = %v, want %v", got, want)
	}
	if r.Len() != 3 {
		t.Errorf("Len() = %d, want 3", r.Len())
	}
}

func TestRing_EvictsOldestWhenFull(t *testing.T) {
	r := New[int](3)
	for _, v := range []int{1, 2, 3, 4, 5} {
		r.Add(v)
	}
	want := []int{3, 4, 5} // oldest (1,2) evicted, newest→oldest order preserved
	got := r.Values()
	if !equalInts(got, want) {
		t.Errorf("Values() = %v, want %v", got, want)
	}
	if r.Len() != 3 {
		t.Errorf("Len() = %d, want 3", r.Len())
	}
	if r.Cap() != 3 {
		t.Errorf("Cap() = %d, want 3", r.Cap())
	}
}

func TestRing_WrapsRepeatedly(t *testing.T) {
	r := New[int](2)
	for i := 1; i <= 100; i++ {
		r.Add(i)
	}
	want := []int{99, 100}
	if got := r.Values(); !equalInts(got, want) {
		t.Errorf("Values() = %v, want %v", got, want)
	}
}

func TestRing_NonPositiveCapacityGetsMinimum(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		r := New[int](c)
		if r.Cap() != 1 {
			t.Errorf("New(%d).Cap() = %d, want 1", c, r.Cap())
		}
		r.Add(7)
		r.Add(8)
		if got := r.Values(); !equalInts(got, []int{8}) {
			t.Errorf("New(%d) Values() = %v, want [8]", c, got)
		}
	}
}

func TestRing_NilReceiverIsSafe(t *testing.T) {
	var r *Ring[int]
	if got := r.Len(); got != 0 {
		t.Errorf("nil Len() = %d, want 0", got)
	}
	if got := r.Cap(); got != 0 {
		t.Errorf("nil Cap() = %d, want 0", got)
	}
	if got := r.Values(); got != nil {
		t.Errorf("nil Values() = %v, want nil", got)
	}
	r.Add(1) // must not panic
}

func TestRing_GenericOverFloatAndBool(t *testing.T) {
	rf := New[float64](2)
	rf.Add(1.5)
	rf.Add(2.5)
	rf.Add(3.5)
	if got := rf.Values(); len(got) != 2 || got[0] != 2.5 || got[1] != 3.5 {
		t.Errorf("float Values() = %v, want [2.5 3.5]", got)
	}

	rb := New[bool](3)
	rb.Add(true)
	rb.Add(false)
	rb.Add(true)
	if got := rb.Values(); len(got) != 3 || got[0] != true || got[1] != false || got[2] != true {
		t.Errorf("bool Values() = %v, want [true false true]", got)
	}
}

func TestRing_ValuesReturnsIndependentCopy(t *testing.T) {
	r := New[int](3)
	r.Add(1)
	r.Add(2)
	got := r.Values()
	got[0] = 999
	if again := r.Values(); again[0] != 1 {
		t.Errorf("mutating Values() result leaked into the ring: %v", again)
	}
}

func TestRing_ConcurrentAccessIsRaceFree(t *testing.T) {
	r := New[int](64)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for i := range 1000 {
				r.Add(i)
				_ = r.Values()
				_ = r.Len()
			}
		})
	}
	wg.Wait()
	if r.Len() != 64 {
		t.Errorf("Len() = %d, want 64 after saturation", r.Len())
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
