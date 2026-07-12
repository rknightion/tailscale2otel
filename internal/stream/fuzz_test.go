package stream

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
)

// The receiver's parse chain is the only place tailscale2otel decodes bytes it
// did not fetch itself: extractRecords/unwrap/classify run on the raw body of an
// inbound POST, before any authentication decision has narrowed what the payload
// may contain. They must not panic on any input.
//
// The seeds cover the envelope shapes documented on the package (bare record,
// HEC {"event":...} wrapper, Tailscale {"logs":[...]} wrapper, concatenated
// objects with no separator, NDJSON) plus the wire-format quirks that decode
// loosely: a numeric flow `proto` and a polymorphic audit `old`/`new`.
func FuzzExtractRecords(f *testing.F) {
	seeds := []string{
		`{"nodeId":"n1","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:80","txBytes":1}]}`,
		`{"event":{"nodeId":"n1","exitTraffic":[{"proto":17,"src":"a:1","dst":"b:2"}]},"time":1717171717.5}`,
		`{"event":{"actor":{"id":"u1"},"action":"UPDATE","old":null,"new":{"a":[1,2]}},"fields":{"recorded":"2026-01-01T00:00:00Z"}}`,
		`{"logs":[{"nodeId":"n1","subnetTraffic":[]},{"actor":{"id":"u"},"action":"CREATE","old":"x","new":["y"]}]}`,
		`{"nodeId":"n1","virtualTraffic":[]}{"nodeId":"n2","physicalTraffic":[]}`,
		"{\"actor\":{\"id\":\"u\"},\"action\":\"DELETE\"}\n{\"nodeId\":\"n\",\"exitTraffic\":[]}",
		`[{"nodeId":"n1","virtualTraffic":[]},{"event":{"actor":{},"action":"X"}}]`,
		`{"event":null,"time":"1717171717"}`,
		`{}`,
		``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		recs, _, err := extractRecords(body)
		if err != nil {
			return // nothing JSON-like in the body; not a decode path
		}
		for _, rec := range recs {
			// classify() probes the record shape, then the handler fully decodes
			// it into the record type classify picked. Mirror that here so the
			// fuzzer reaches the real flowlog/audit decoders, not just the
			// envelope walk.
			switch classify(rec.raw) {
			case kindFlow:
				var fl flowlog.FlowLog
				_ = json.Unmarshal(rec.raw, &fl)
			case kindAudit:
				var ev audit.Event
				_ = json.Unmarshal(rec.raw, &ev)
			case kindUnknown:
			}
			_ = rec.envTime
		}
	})
}

// FuzzParseHECTime exercises the Splunk-HEC "time" parse in isolation: it accepts
// a JSON number or a quoted string, and feeds time.Unix, so a value large enough
// to overflow an int64 second count must still produce a usable time rather than
// panicking or wrapping into a wild year.
func FuzzParseHECTime(f *testing.F) {
	for _, s := range []string{
		`1717171717`, `1717171717.5`, `"1717171717"`, `null`, `0`, `-1`,
		`1e308`, `"1e308"`, `9223372036854775807`, `"not-a-time"`, `{}`, ``,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		got := parseHECTime(json.RawMessage(raw))
		if got.IsZero() {
			return
		}
		// A non-zero result becomes an exported log record's timestamp, so it must
		// be representable as int64 nanoseconds (the OTLP timestamp width). Beyond
		// that bound time.Unix wraps instead of failing, which is how "time":1e308
		// used to produce a record stamped in the year 292277026596.
		if got.Before(time.Unix(0, 0)) || got.After(time.Unix(0, math.MaxInt64)) {
			t.Fatalf("parseHECTime(%q) = %v, not representable as an OTLP timestamp", raw, got)
		}
	})
}
