package audit_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// Audit `old`/`new` are polymorphic on the wire — the API renders them as a
// string, object, array, number, bool or null depending on the property — so they
// stay json.RawMessage and are flattened to a string attribute by renderRaw at
// processing time. That flattening, plus the origin/action/actor-type
// normalisation, runs on whatever the API sends, so it must survive every JSON
// value shape rather than only the ones seen so far.
//
// Seeds cover each of those shapes, an event with no actor, and a deeply nested
// value (renderRaw walks it).
func FuzzProcessorProcessAll(f *testing.F) {
	seeds := []string{
		`{"logs":[{"eventTime":"2026-01-01T00:00:00Z","action":"UPDATE","origin":"admin console","actor":{"id":"u1","type":"user","loginName":"a@b.c"},"target":{"id":"d1","type":"device","property":"name"},"old":"old-name","new":"new-name"}]}`,
		`{"logs":[{"action":"CREATE","actor":{"id":"u","type":"tagged-device","tags":["tag:a"]},"old":null,"new":{"nested":{"deep":[1,2,{"x":true}]}}}]}`,
		`{"logs":[{"action":"DELETE","actor":{},"old":[1,2,3],"new":false}]}`,
		"{\"logs\":[{\"action\":\"X\",\"actor\":{\"id\":\"u\"},\"old\":1.5e300,\"new\":\"\\u0000\\ufffd\"}]}",
		`{"tailnetId":"example.com","logs":[{"action":"","actor":{"id":""}}]}`,
		`{"logs":[]}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		var resp audit.ConfigurationResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return // not an audit response; the decoder rejecting it is correct
		}

		rec := telemetrytest.New()
		p := audit.NewProcessor()
		p.ProcessAll(resp, rec.Emitter())
	})
}
