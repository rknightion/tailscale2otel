package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// The logger was hardcoded to slog.NewTextHandler, so a container or systemd
// deployment could not route or query its operational logs without parsing
// text (#312).

func TestNewLogHandlerEmitsOneJSONRecordPerLine(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newLogHandler(&buf, "json", slog.LevelInfo))
	logger.Info("collector finished", "component", "devices", "tailscale.tailnet", "example.com")
	logger.Error("collector failed", "component", "flowlogs", "error", "boom")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines for 2 records, want one record per line: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i, err, line)
		}
		for _, key := range []string{"time", "level", "msg", "component"} {
			if _, ok := rec[key]; !ok {
				t.Errorf("line %d has no %q field: %s", i, key, line)
			}
		}
	}
}

func TestNewLogHandlerDefaultsToText(t *testing.T) {
	for _, format := range []string{"", "text", "TEXT", "nonsense"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(newLogHandler(&buf, format, slog.LevelInfo)).Info("hello", "k", "v")
			out := buf.String()
			if strings.HasPrefix(strings.TrimSpace(out), "{") {
				t.Fatalf("format %q produced JSON; text is the default and an unrecognized "+
					"value must not silently change the log format", format)
			}
			if !strings.Contains(out, "msg=hello") {
				t.Errorf("format %q produced %q, want the text handler's output", format, out)
			}
		})
	}
}

// Case and surrounding whitespace come from a YAML file or an env var and must
// not decide whether logs are parseable.
func TestNewLogHandlerAcceptsJSONInAnyCase(t *testing.T) {
	for _, format := range []string{"json", "JSON", " json ", "Json"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(newLogHandler(&buf, format, slog.LevelInfo)).Info("hello")
			if !strings.HasPrefix(strings.TrimSpace(buf.String()), "{") {
				t.Errorf("format %q did not produce JSON: %q", format, buf.String())
			}
		})
	}
}

func TestNewLogHandlerHonoursTheLevel(t *testing.T) {
	var buf bytes.Buffer
	slog.New(newLogHandler(&buf, "json", slog.LevelWarn)).Info("should not appear")
	if buf.Len() != 0 {
		t.Errorf("an INFO record was emitted at WARN level: %q", buf.String())
	}
}
