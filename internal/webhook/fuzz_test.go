package webhook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Webhook bodies are the second place tailscale2otel decodes bytes it did not
// fetch itself, alongside the HEC receiver (#434). The decode chain —
// decodeAcceptedBatch → decodeEvent → canonicalDigest → typedDataAttrs — runs on
// an inbound POST body, and it runs a SECOND time on WAL replay via ApplyDurable,
// where the HMAC decision has already been made and forgotten. It must not panic
// on any input, and the two contracts that make it safe to export must hold
// whatever the body says.
//
// The seeds are the documented envelope plus the shapes that decode loosely: a
// polymorphic `data` (object, array, scalar, null, absent), an unknown `type`, an
// unknown `version`, duplicate keys, and the degenerate array forms the batch walk
// has to survive.

// webhookSeeds are shared by the fuzz targets below. Every one is a real shape:
// the official example event, the four `data` shapes upstream actually sends, and
// the malformed bodies a replayed WAL segment can hold after a truncated write.
var webhookSeeds = []string{
	`[{"timestamp":"2026-01-01T00:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"m","data":{"nodeID":"n-1","deviceName":"laptop","managedBy":"admin","actor":"a@example.com","url":"https://example.com"}}]`,
	`[{"timestamp":"2026-01-01T00:00:00Z","version":1,"type":"userRoleUpdated","tailnet":"e.com","data":{"user":"u@e.com","oldRoles":["member"],"newRoles":["admin"]}}]`,
	`[{"version":1,"type":"nodeKeyExpired","data":{"nodeID":"n","keyExpiryTime":"2026-01-01T00:00:00Z"}}]`,
	`[{"type":"policyUpdate","data":null}]`,
	`[{"type":"policyUpdate","data":[1,2,3]}]`,    // wrong-shaped data: deliberately omitted, not fatal
	`[{"type":"policyUpdate","data":"a string"}]`, // ditto
	`[{"type":"policyUpdate"}]`,                   // absent data
	`[{"type":"unknownFutureEvent","version":99,"data":{"anything":{"nested":true}}}]`,
	`[{"type":"a","data":{"nodeID":"1"}},{"type":"a","data":{"nodeID":"1"}}]`, // identical events: identical digests
	`[]`,
	`[{}]`,
	`[null]`,
	`[{"data":{}}`, // truncated
	`[{},{}]trailing`,
	`{"not":"an array"}`,
	``,
	// Key order and whitespace must not change a digest — that is the whole point
	// of canonicalizing before hashing.
	`[{"type":"a","data":{"x":1,"y":2}}]`,
	`[{ "data" : { "y" : 2 , "x" : 1 } , "type" : "a" }]`,
}

// knownAttrKeys is the bounded typed allowlist. An attribute key outside it is a
// cardinality and PII incident at once: webhook `data` carries free-text and
// user-identifying values, so a key derived FROM the payload would let an inbound
// request mint arbitrary metric dimensions.
var knownAttrKeys = map[string]bool{
	AttrNodeID: true, AttrDeviceName: true, AttrManagedBy: true, AttrActor: true,
	AttrURL: true, AttrKeyExpiration: true, AttrUser: true, AttrOldRoles: true,
	AttrNewRoles: true,
}

// FuzzDecodeAcceptedBatch drives the whole inbound decode chain and asserts the
// invariants that make the result safe to export.
func FuzzDecodeAcceptedBatch(f *testing.F) {
	for _, s := range webhookSeeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		batch, err := decodeAcceptedBatch(body)
		if err != nil {
			return // not a decodable batch; nothing downstream ever sees it
		}

		// Every accepted event must have a digest: the dedup failsafe keys on them,
		// and an event without one would be silently un-dedupable.
		if len(batch.events) != len(batch.digests) {
			t.Fatalf("%d events but %d digests", len(batch.events), len(batch.digests))
		}

		for i, ev := range batch.events {
			if batch.digests[i] == "" {
				t.Fatalf("event %d has an empty digest", i)
			}

			// Attribute keys must come from the bounded allowlist, whatever the
			// payload contains. Values are free — they are redacted by category
			// downstream — but a KEY derived from the body would be unbounded
			// cardinality that an unauthenticated POST controls.
			for key := range typedDataAttrs(ev) {
				if !knownAttrKeys[key] {
					t.Fatalf("event %d produced attribute key %q, outside the typed allowlist — "+
						"an inbound body must not be able to mint metric dimensions", i, key)
				}
			}
		}
	})
}

// FuzzCanonicalDigest exercises the digest in isolation. It is the dedup key, so
// two encodings of the SAME JSON value must hash identically and two different
// values must not collide by construction (differing bytes are not asserted to
// differ — that is SHA-256's job — but equal values are asserted to agree).
func FuzzCanonicalDigest(f *testing.F) {
	for _, s := range webhookSeeds {
		f.Add([]byte(s))
	}
	for _, s := range []string{
		`{"b":1,"a":2}`, `{"a":2,"b":1}`, `{"a":1e2}`, `{"a":100}`,
		`[]`, `{}`, `null`, `0`, `""`, `{"a":{"b":{"c":[1,{"d":null}]}}}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := canonicalDigest(raw)
		if err != nil {
			return
		}
		if len(got) != 64 {
			t.Fatalf("digest %q is not a hex SHA-256", got)
		}

		// Determinism: the same bytes must hash the same way every time.
		again, err := canonicalDigest(raw)
		if err != nil || again != got {
			t.Fatalf("digest is not deterministic: %q then %q (err=%v)", got, again, err)
		}

		// Canonicality: re-encoding the value through encoding/json reorders object
		// keys and normalizes whitespace, and must NOT change the digest. This is
		// the property the whole function exists for — a digest that moved with key
		// order would make the dedup failsafe useless against a re-delivered event.
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&value) != nil {
			return
		}
		reencoded, merr := json.Marshal(value)
		if merr != nil {
			return
		}
		requoted, rerr := canonicalDigest(reencoded)
		if rerr != nil {
			// A value that decodes but cannot be re-encoded and re-digested means the
			// canonical form is not closed over its own output.
			t.Fatalf("re-encoded %s failed to digest: %v", reencoded, rerr)
		}
		if requoted != got {
			t.Fatalf("digest changed under re-encoding:\n  original  %s -> %s\n  reencoded %s -> %s",
				raw, got, reencoded, requoted)
		}
	})
}

// Deep nesting must be REJECTED, not recursed into: writeCanonicalJSON walks the
// decoded value recursively, so an unbounded depth would be a stack overflow that
// an inbound POST body chooses. encoding/json's own decoder caps nesting, which is
// what makes the recursion bounded; this pins that the cap is doing the work,
// because a future change to how the value is decoded could silently remove it.
func TestCanonicalDigest_DeepNestingIsRejectedNotRecursed(t *testing.T) {
	const depth = 100_000
	deep := strings.Repeat(`[`, depth) + strings.Repeat(`]`, depth)

	if _, err := canonicalDigest([]byte(deep)); err == nil {
		t.Fatalf("a %d-deep body was digested rather than rejected — the recursion in "+
			"writeCanonicalJSON is only safe because the decode refuses to build a value "+
			"that deep", depth)
	}

	// And the same body must not take the whole batch decoder down either.
	if _, err := decodeAcceptedBatch([]byte(`[` + deep + `]`)); err == nil {
		t.Fatal("a deeply nested batch decoded without error")
	}
}
