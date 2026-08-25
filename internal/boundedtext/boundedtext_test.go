package boundedtext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStringRespectsByteAndUTF8Boundary(t *testing.T) {
	in := strings.Repeat("é", 10)
	got := String(in, 9)
	if len(got) > 9 {
		t.Fatalf("len(String) = %d, want <= 9", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("String split UTF-8: %q", got)
	}
}

func TestStringsBudgetBoundsAggregate(t *testing.T) {
	values := []string{strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100)}
	StringsBudget(values, 80, 150)
	total := 0
	for _, value := range values {
		total += len(value)
	}
	if total > 150 {
		t.Fatalf("aggregate bytes = %d, want <= 150", total)
	}
}
