package main

import (
	"strings"
	"testing"
)

func TestSubstituteDashboard(t *testing.T) {
	declared := map[string]bool{"topn": true, "host_name": true, "log_filter": true}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "builtin rate interval in a range selector",
			in:   "rate(x_total[$__rate_interval])",
			want: "rate(x_total[5m])",
		},
		{
			name: "longest builtin wins over its own prefix",
			in:   "x[$__interval] + $__interval_ms",
			want: "x[1m] + 60000",
		},
		{
			name: "variable inside a label matcher becomes a regex-safe string",
			in:   `up{host_name=~"$host_name"}`,
			want: `up{host_name=~".*"}`,
		},
		{
			name: "variable inside a backquoted string",
			in:   "up{host_name=~`$host_name`}",
			want: "up{host_name=~`.*`}",
		},
		{
			name: "variable in a numeric position becomes a number",
			in:   "topk($topn, up)",
			want: "topk(1, up)",
		},
		{
			name: "braced form",
			in:   "topk(${topn}, up)",
			want: "topk(1, up)",
		},
		{
			name: "braced form with a format modifier",
			in:   `up{host_name=~"${host_name:regex}"}`,
			want: `up{host_name=~".*"}`,
		},
		{
			name: "legacy double-bracket form",
			in:   `up{host_name=~"[[host_name]]"}`,
			want: `up{host_name=~".*"}`,
		},
		{
			name: "variable directly in a duration position becomes a duration",
			in:   "rate(x_total[$topn])",
			want: "rate(x_total[5m])",
		},
		{
			name: "a regex end-anchor is not a template token",
			in:   `up{host_name=~"foo$"}`,
			want: `up{host_name=~"foo$"}`,
		},
		{
			name: "no tokens is a pass-through",
			in:   "sum(rate(x_total[5m]))",
			want: "sum(rate(x_total[5m]))",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := substitute(tc.in, dashboardInterpolation(declared))
			if err != nil {
				t.Fatalf("substitute(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("substitute(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSubstituteErrors(t *testing.T) {
	declared := map[string]bool{"topn": true}

	tests := []struct {
		name    string
		in      string
		ctx     interpolation
		wantSub string
	}{
		{
			name:    "undeclared dashboard variable",
			in:      `up{host_name=~"$hostname"}`,
			ctx:     dashboardInterpolation(declared),
			wantSub: `not declared`,
		},
		{
			name:    "unknown grafana builtin",
			in:      "rate(x_total[$__nonsense])",
			ctx:     dashboardInterpolation(declared),
			wantSub: "unknown Grafana built-in",
		},
		{
			name:    "stray dollar outside a string is a malformed token",
			in:      "topk($, up)",
			ctx:     dashboardInterpolation(declared),
			wantSub: "unsubstituted",
		},
		{
			name:    "unclosed braced token inside a string",
			in:      `up{host_name=~"${topn"}`,
			ctx:     dashboardInterpolation(declared),
			wantSub: "unsubstituted",
		},
		{
			name:    "rule files interpolate nothing, so a builtin is a bug",
			in:      "rate(x_total[$__rate_interval])",
			ctx:     ruleInterpolation(),
			wantSub: "no templating",
		},
		{
			name:    "rule files reject dashboard variables too",
			in:      "up > $topn",
			ctx:     ruleInterpolation(),
			wantSub: "no templating",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := substitute(tc.in, tc.ctx)
			if err == nil {
				t.Fatalf("substitute(%q) = %q, want an error", tc.in, got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("substitute(%q) error = %q, want it to contain %q", tc.in, err, tc.wantSub)
			}
		})
	}
}
