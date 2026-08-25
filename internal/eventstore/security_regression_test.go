package eventstore

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecordBoundsEveryRetainedStringAndAggregateBytes(t *testing.T) {
	huge := strings.Repeat("界", 20_000)
	m := NewMemory(1)
	m.Record(Event{
		Source: Source(huge), Tailnet: huge, Action: huge, Type: huge, Origin: huge,
		ActorID: huge, ActorName: huge, ActorType: huge,
		TargetID: huge, TargetName: huge, TargetType: huge, TargetProperty: huge,
		Severity: huge, Error: huge, Summary: huge, Details: huge,
	})
	page := m.Page(Query{Limit: 1})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(page.Rows))
	}
	ev := page.Rows[0]
	values := []string{string(ev.Source), ev.Tailnet, ev.Action, ev.Type, ev.Origin,
		ev.ActorID, ev.ActorName, ev.ActorType, ev.TargetID, ev.TargetName, ev.TargetType,
		ev.TargetProperty, ev.Severity, ev.Error, ev.Summary, ev.Details}
	total := 0
	for i, value := range values {
		if len(value) > MaxFieldBytes {
			t.Errorf("field %d retained %d bytes, want <= %d", i, len(value), MaxFieldBytes)
		}
		if !utf8.ValidString(value) {
			t.Errorf("field %d is invalid UTF-8", i)
		}
		total += len(value)
	}
	if total > MaxEventBytes {
		t.Fatalf("event retained %d string bytes, want <= %d", total, MaxEventBytes)
	}
}
