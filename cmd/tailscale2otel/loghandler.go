package main

import (
	"io"
	"log/slog"
	"strings"
)

// newLogHandler builds the slog handler for the configured log_format.
//
// The logger was hardcoded to the text handler, so a container or systemd
// deployment could not reliably route or query the exporter's operational logs
// without parsing prose (#312).
//
// Text stays the default, and an UNRECOGNIZED value falls back to text rather
// than erroring: log_format is the setting that decides whether you can read the
// error telling you log_format is wrong, so it must never be the thing that
// stops the process from starting. config.Validate rejects an unknown value at
// the point where an operator can see the complaint; this function is the
// last-resort behavior if one reaches it anyway.
//
// The attribute set is whatever each call site passes — component, tailnet,
// collector, endpoint and error are already used consistently across the app —
// so JSON mode changes the encoding, not the fields. Secrets never reach a log
// line in either mode: config.Secret has no String method that reveals, and
// values are redacted at their source.
func newLogHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(strings.TrimSpace(format), logFormatJSON) {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// logFormatJSON is the one value that changes the handler; everything else,
// including the "text" default, takes the text branch. config.Validate owns the
// accepted set and rejects anything outside it at startup.
const logFormatJSON = "json"
