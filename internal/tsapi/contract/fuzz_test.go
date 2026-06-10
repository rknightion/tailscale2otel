package contract_test

import (
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/oas"
	"github.com/rknightion/tailscale2otel/internal/tsapi/contract"
)

// loadSpec reads and parses the vendored Tailscale OpenAPI spec.
// The package directory is internal/tsapi/contract, so ../../../spec reaches
// the committed baseline spec/tailscale-api.json.
func loadSpec(t *testing.T) *oas.Spec {
	t.Helper()
	b, err := os.ReadFile("../../../spec/tailscale-api.json")
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	s, err := oas.ParseSpec(b)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	return s
}

// oasMistypedOps lists operations whose OAS response schemas are inaccurate
// relative to the actual wire format and Go decoder. Schema-synthesis cannot
// produce a valid body for these ops because the OAS type differs from the wire
// shape our decoders expect. Each op is covered by a hand-written quirk payload
// in the tests below; the reason for the gap is logged per op.
//
//	listNetworkFlowLogs: start/end/logged typed string in OAS (no date-time
//	  format) but time.Time in Go; proto typed string in OAS but int in Go.
//	listConfigurationAuditLogs: eventTime typed string in OAS (no date-time
//	  format) but time.Time in Go.
//	getLogStreamingStatus: lastActivity typed string in OAS (no date-time
//	  format) but time.Time in Go.
//	getPostureIntegrations: status.lastSync typed string in OAS (no date-time
//	  format) but time.Time in Go.
//	listUserInvites: tailnetId/inviterId typed integer (int64) in OAS but
//	  string on the wire and in the Go decoder.
//	listDeviceInvites: same tailnetId/deviceId/sharerId integer-in-OAS vs
//	  string-on-wire discrepancy.
var oasMistypedOps = map[string]string{
	"listNetworkFlowLogs":        "OAS types start/end/logged as string (no date-time format) and proto as string; wire uses RFC3339 timestamps and integer proto; covered by TestFuzz_FlowLogProtoNumeric",
	"listConfigurationAuditLogs": "OAS types eventTime as string (no date-time format); wire uses RFC3339 timestamp; covered by TestFuzz_AuditOldNewPolymorphic",
	"getLogStreamingStatus":      "OAS types lastActivity as string (no date-time format); wire and Go decoder use time.Time (RFC3339); covered by TestFuzz_LogStreamStatusHandWritten",
	"getPostureIntegrations":     "OAS types status.lastSync as string (no date-time format); wire and Go decoder use time.Time (RFC3339); covered by TestFuzz_PostureIntegrationsHandWritten",
	"listUserInvites":            "OAS types tailnetId/inviterId as integer/int64; wire and Go decoder use string; covered by TestFuzz_UserInvitesHandWritten",
	"listDeviceInvites":          "OAS types tailnetId/deviceId/sharerId as integer/int64; wire and Go decoder use string; covered by TestFuzz_DeviceInvitesHandWritten",
}

// TestFuzz_SchemaSynthDecodes proves that schema-synthesized bodies decode
// without error through the real tsapi.Client methods for all ops whose OAS
// schema accurately reflects the wire format. Ops with OAS inaccuracies are
// excluded with a logged reason and covered by hand-written quirk tests.
func TestFuzz_SchemaSynthDecodes(t *testing.T) {
	spec := loadSpec(t)

	for _, op := range contract.Manifest {
		if op.FuzzSkip {
			continue
		}
		if reason, skip := oasMistypedOps[op.ID]; skip {
			t.Logf("%s: skipped schema-synth — %s", op.ID, reason)
			continue
		}

		o, ok := spec.Ops[op.ID]
		if !ok {
			t.Errorf("%s: not in OAS", op.ID)
			continue
		}

		body := oas.SynthesizeBody(o.Response, 8)
		rep := contract.Decode(op, body)
		if rep.Err != nil {
			t.Errorf("%s: synth body decode err: %v\nbody=%s", op.ID, rep.Err, body)
		}
	}
}

// TestFuzz_EdgeVariants checks that empty, null, and unknown-key payloads do
// not cause decode errors.
func TestFuzz_EdgeVariants(t *testing.T) {
	op, _ := contract.ByID("listTailnetDevices")
	for _, body := range []string{
		`{"devices":[]}`,                     // empty array
		`{"devices":null}`,                   // null array
		`{"devices":[{}]}`,                   // empty element
		`{"devices":[],"unexpectedField":1}`, // unknown top-level key (must not error)
	} {
		if rep := contract.Decode(op, []byte(body)); rep.Err != nil {
			t.Errorf("edge %s: decode err %v", body, rep.Err)
		}
	}
}

// TestFuzz_FlowLogProtoNumeric covers the documented wire quirk: the flow-log
// proto field is a number on the wire (e.g. 6 for TCP), not the string the OAS
// schema shows. This is a hand-written payload that exercises the real decoder
// path and asserts it is defensive.
func TestFuzz_FlowLogProtoNumeric(t *testing.T) {
	op, _ := contract.ByID("listNetworkFlowLogs")
	raw := []byte(`{"logs":[{"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:01:00Z",
		"virtualTraffic":[{"proto":6,"src":"100.64.0.1:0","dst":"100.64.0.2:443"}]}]}`)
	if rep := contract.Decode(op, raw); rep.Err != nil {
		t.Fatalf("numeric proto: %v", rep.Err)
	}
}

// TestFuzz_AuditOldNewPolymorphic covers the documented polymorphic old/new
// fields on audit log events: the API renders them as a JSON string, object,
// array, or null depending on the property. Our decoder uses json.RawMessage
// and must accept all shapes without error.
func TestFuzz_AuditOldNewPolymorphic(t *testing.T) {
	op, _ := contract.ByID("listConfigurationAuditLogs")
	for _, body := range []string{
		`{"logs":[{"action":"UPDATE","old":"s","new":null}]}`,
		`{"logs":[{"action":"UPDATE","old":{"k":"v"},"new":["x"]}]}`,
		`{"logs":[{"action":"UPDATE","old":null,"new":null}]}`,
	} {
		if rep := contract.Decode(op, []byte(body)); rep.Err != nil {
			t.Fatalf("polymorphic old/new (%s): %v", body, rep.Err)
		}
	}
}

// TestFuzz_LogStreamStatusHandWritten covers getLogStreamingStatus with a
// representative body using RFC3339 for lastActivity (OAS types it as string
// without date-time format, but the wire and Go decoder use time.Time).
func TestFuzz_LogStreamStatusHandWritten(t *testing.T) {
	op, _ := contract.ByID("getLogStreamingStatus")
	body := []byte(`{"lastActivity":"2026-01-01T00:00:00Z","lastError":"","maxBodySize":1024,"numBytesSent":100,"numEntriesSent":10,"numFailedRequests":0,"numTotalRequests":5,"numSpoofedEntries":0}`)
	if rep := contract.Decode(op, body); rep.Err != nil {
		t.Fatalf("logstream status: %v", rep.Err)
	}
}

// TestFuzz_PostureIntegrationsHandWritten covers getPostureIntegrations with a
// representative body using RFC3339 for status.lastSync (OAS types it as string
// without date-time format, but the Go decoder uses time.Time).
func TestFuzz_PostureIntegrationsHandWritten(t *testing.T) {
	op, _ := contract.ByID("getPostureIntegrations")
	body := []byte(`{"integrations":[{"id":"pi-1","provider":"falcon","status":{"lastSync":"2026-01-01T00:00:00Z","matchedCount":5,"possibleMatchedCount":10,"providerHostCount":15}}]}`)
	if rep := contract.Decode(op, body); rep.Err != nil {
		t.Fatalf("posture integrations: %v", rep.Err)
	}
}

// TestFuzz_UserInvitesHandWritten covers listUserInvites with a representative
// body using string values for tailnetId/inviterId (OAS types them as int64, but
// the actual wire format and Go decoder use strings).
func TestFuzz_UserInvitesHandWritten(t *testing.T) {
	op, _ := contract.ByID("listUserInvites")
	body := []byte(`[{"id":"inv-1","role":"member","tailnetId":"123","inviterId":"u-1","email":"a@b.com","inviteUrl":"https://example.com/invite/x","created":"2026-01-01T00:00:00Z","accepted":false}]`)
	if rep := contract.Decode(op, body); rep.Err != nil {
		t.Fatalf("user invites: %v", rep.Err)
	}
}

// TestFuzz_DeviceInvitesHandWritten covers listDeviceInvites with a
// representative bare-array body. OAS types tailnetId/deviceId/sharerId as
// int64, but the wire and Go decoder use string fields (or omit them entirely
// since our decoder only reads accepted/multiUse/allowExitNode/email).
func TestFuzz_DeviceInvitesHandWritten(t *testing.T) {
	op, _ := contract.ByID("listDeviceInvites")
	body := []byte(`[{"accepted":false,"multiUse":false,"allowExitNode":false,"email":"a@b.com"}]`)
	if rep := contract.Decode(op, body); rep.Err != nil {
		t.Fatalf("device invites: %v", rep.Err)
	}
}
