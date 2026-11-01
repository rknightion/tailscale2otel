package annotations

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterPolicy(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, header string
		want         time.Duration
	}{
		{"absent", "", 0}, {"invalid", "invalid", 0},
		{"seconds", "42", 42 * time.Second}, {"whitespace", " 42 \t", 42 * time.Second},
		{"zero", "0", time.Nanosecond}, {"negative", "-1", time.Nanosecond},
		{"negative overflow", "-9223372036854775808", time.Nanosecond},
		{"negative wrapped", "-9223372037", time.Nanosecond},
		{"positive overflow", "9223372036854775807", 15 * time.Minute},
		{"future", now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{"past", now.Add(-time.Minute).Format(http.TimeFormat), time.Nanosecond},
		{"present", now.Format(http.TimeFormat), time.Nanosecond},
		{"seconds cap", "86400", 15 * time.Minute},
		{"date cap", now.Add(24 * time.Hour).Format(http.TimeFormat), 15 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.header, now); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}
