package stream_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/audit"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/stream"
)

// reasonTooManyConnections is the rejection reason added by GHSA-7rg3-xj9w-2gm8.
const reasonTooManyConnections = "too_many_connections"

// maxConnectionsPerRequest mirrors the unexported constant of the same name in
// the package under test (same arrangement as maxRecordsPerRequest above:
// TestFrozenLimits inside the package asserts the two agree).
const maxConnectionsPerRequest = 500_000

// flowWithConnections builds a classifiable flow record whose named traffic
// arrays carry the given element counts. An empty `{}` connection is a valid
// ConnectionCounts (every field is optional), so this makes width cheap in bytes
// — which is exactly the amplification the connection budget has to bound.
func flowWithConnections(counts map[string]int) string {
	var b strings.Builder
	b.WriteString(`{"nodeId":"n1"`)
	for _, field := range []string{"virtualTraffic", "subnetTraffic", "exitTraffic", "physicalTraffic"} {
		n, ok := counts[field]
		if !ok {
			continue
		}
		b.WriteString(`,"`)
		b.WriteString(field)
		b.WriteString(`":[`)
		for i := range n {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("{}")
		}
		b.WriteString(`]`)
	}
	b.WriteString(`}`)
	return b.String()
}

// -----------------------------------------------------------------------------
// GHSA-7rg3-xj9w-2gm8 — per-request nested-connection budget
// -----------------------------------------------------------------------------

// TestConnectionBudget_SingleRecordOverBudgetRejected is the control. One
// accepted flow object consumes exactly one top-level record slot no matter how
// wide its connection arrays are, so the record cap does not bound it — yet every
// connection inside drives metric, dedup, enrichment and optional log work. The
// request must be refused before the []ConnectionCounts slice is ever grown, and
// nothing may be emitted.
func TestConnectionBudget_SingleRecordOverBudgetRejected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	body := flowWithConnections(map[string]int{"virtualTraffic": maxConnectionsPerRequest + 1})
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyConnections}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_connections} = %v, want 1", metricRejected, p.Value)
	}
	// Atomic rejection: a refused request contributes zero records AND zero
	// downstream flow metrics.
	if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
		t.Fatalf("records emitted for an over-budget body: %+v", pts)
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
		t.Fatalf("flow metrics emitted for an over-budget body: %+v", pts)
	}
}

// TestConnectionBudget_SummedAcrossTrafficArrays proves the budget is per
// REQUEST, not per array: a record can spread its width across all four traffic
// arrays and each one alone stays modest.
func TestConnectionBudget_SummedAcrossTrafficArrays(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	quarter := maxConnectionsPerRequest/4 + 1
	body := flowWithConnections(map[string]int{
		"virtualTraffic":  quarter,
		"subnetTraffic":   quarter,
		"exitTraffic":     quarter,
		"physicalTraffic": quarter,
	})
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyConnections}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_connections} = %v, want 1", metricRejected, p.Value)
	}
}

// TestConnectionBudget_SummedAcrossRecords proves the budget accumulates across
// the records of one batch, so splitting the width over many under-budget records
// does not evade it.
func TestConnectionBudget_SummedAcrossRecords(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	half := maxConnectionsPerRequest/2 + 1
	rec1 := flowWithConnections(map[string]int{"virtualTraffic": half})
	rec2 := flowWithConnections(map[string]int{"virtualTraffic": half})
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader(rec1+"\n"+rec2+"\n"))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyConnections}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_connections} = %v, want 1", metricRejected, p.Value)
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
		t.Fatalf("flow metrics emitted for an over-budget batch: %+v", pts)
	}
}

// TestConnectionBudget_NormalBatchUnaffected guards against the budget regressing
// ordinary ingestion. The live-captured wire format carries a handful of
// connections per record — four orders of magnitude under the budget.
func TestConnectionBudget_NormalBatchUnaffected(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader(realHECStreamBody))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeAudit}); p.Value != 1 {
		t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, p.Value)
	}
	if p := findPoint(t, rec.MetricPoints(audit.MetricAuditEvents), map[string]string{}); p.Value != 1 {
		t.Fatalf("%s = %v, want 1", audit.MetricAuditEvents, p.Value)
	}
}

// TestConnectionBudget_MalformedTrafficStillDecodeError keeps the budget check
// from swallowing the #201 contract: a traffic array that is not an array at all
// is still a KNOWN record that failed typed decoding, so it must reject the batch
// as decode_error, not as a size problem.
func TestConnectionBudget_MalformedTrafficStillDecodeError(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	body := `{"nodeId":"n1","virtualTraffic":{"not":"an array"}}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonDecodeError}); p.Value != 1 {
		t.Fatalf("%s{reason=decode_error} = %v, want 1", metricRejected, p.Value)
	}
	if p := findPoint(t, rec.MetricPoints(metricDecodeErrors), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricDecodeErrors, p.Value)
	}
}

// TestConnectionBudget_NullTrafficIsNotAnError pins a wire-format quirk the
// counting pass must tolerate: "virtualTraffic": null classifies as a flow (the
// raw value is non-empty) and unmarshals cleanly to a nil slice, so it must count
// as zero connections rather than as a malformed array.
func TestConnectionBudget_NullTrafficIsNotAnError(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	body := `{"nodeId":"n1","virtualTraffic":null,"physicalTraffic":[{"src":"100.64.0.1:0","dst":"10.0.0.1:1","txBytes":5}]}`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
}

// -----------------------------------------------------------------------------
// GHSA-429c-x655-jwpx — bare arrays are visited incrementally
// -----------------------------------------------------------------------------

// TestBareArray_RecordBudgetRejectsOverCapArray is the end-to-end behavior
// guard for the bare-array branch: an over-cap top-level array is refused with
// 413 + too_many_records and contributes nothing.
//
// It is a behavior guard, NOT the proof of the incremental fix — the status is
// 413 either way. The allocation and stop-at-budget proofs are internal
// (TestUnwrap_BareArrayDoesNotMaterializeFullWidth,
// TestUnwrapArray_StopsAtBudgetPlusOne), because the difference the advisory is
// about is invisible from outside the process.
func TestBareArray_RecordBudgetRejectsOverCapArray(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	var b strings.Builder
	b.Grow(3*maxRecordsPerRequest + 8)
	b.WriteString("[")
	for i := range maxRecordsPerRequest + 1 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("{}")
	}
	b.WriteString("]")
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(b.String()))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonTooManyRecords}); p.Value != 1 {
		t.Fatalf("%s{reason=too_many_records} = %v, want 1", metricRejected, p.Value)
	}
	if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
		t.Fatalf("records emitted for an over-cap array: %+v", pts)
	}
}

// TestBareArray_MalformedUnderBudgetStillDropped pins that the incremental walk
// did NOT change how a genuinely malformed array is treated: it is still an
// unrecognized shape that yields no records, not a corruption verdict.
func TestBareArray_MalformedUnderBudgetStillDropped(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
		strings.NewReader(`[{},@]`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonUnparsable}); p.Value != 1 {
		t.Fatalf("%s{reason=unparsable} = %v, want 1", metricRejected, p.Value)
	}
}

// TestBareArray_DocumentedShapesStillDecode guards the documented bare-array
// envelope (a JSON array of records, optionally HEC-wrapped) through the
// incremental walk.
func TestBareArray_DocumentedShapesStillDecode(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: loopbackListen})

	body := `[` + captureFlowRecord + `,{"time":1780500887.356,"event":` + captureAuditRecord + `}]`
	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	assertFlowAndAuditOnce(t, rec)
}

// TestBareArray_EmptyArrayIsNotARecord pins the pre-existing edge: an empty
// top-level array carries no records, so the request has nothing to ingest.
func TestBareArray_EmptyArrayIsNotARecord(t *testing.T) {
	s, _ := newServer(t, stream.Options{Listen: loopbackListen})

	w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(`[]`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", w.Code, w.Body.String())
	}
}

// -----------------------------------------------------------------------------
// GHSA-6j2r-56pv-qrm7 — wrapper arrays are visited incrementally
// -----------------------------------------------------------------------------

// TestLogsWrapper_DocumentedShapesStillDecode guards the {"logs":[...]} wrapper
// through the incremental walk, including the empty-array edge, which must keep
// falling through to the plain-record path rather than being read as a batch.
func TestLogsWrapper_DocumentedShapesStillDecode(t *testing.T) {
	t.Run("batch", func(t *testing.T) {
		s, rec := newServer(t, stream.Options{Listen: loopbackListen})
		body := `{"logs":[` + captureFlowRecord + `,` + captureAuditRecord + `]}`
		w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(body))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
		}
		assertFlowAndAuditOnce(t, rec)
	})

	t.Run("empty-logs-is-a-plain-record", func(t *testing.T) {
		s, rec := newServer(t, stream.Options{Listen: loopbackListen})
		// {"logs":[]} has no batch children, so it is treated as one (unclassified)
		// record object — the behavior before the incremental rewrite.
		w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil, strings.NewReader(`{"logs":[]}`))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
		}
		if p := findPoint(t, rec.MetricPoints(metricSkipped), map[string]string{attrReason: reasonUnclassified}); p.Value != 1 {
			t.Fatalf("%s{reason=unclassified} = %v, want 1", metricSkipped, p.Value)
		}
	})

	t.Run("non-array-logs-is-a-plain-record", func(t *testing.T) {
		s, rec := newServer(t, stream.Options{Listen: loopbackListen})
		// A "logs" that is not an array is not a batch wrapper; the value is a
		// record object in its own right (and classifies as an audit event here).
		w := post(t, s.Handler(), http.MethodPost, "/services/collector/event", nil,
			strings.NewReader(`{"logs":"nope","actor":{"id":"u1"},"action":"CREATE"}`))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
		}
		if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeAudit}); p.Value != 1 {
			t.Fatalf("%s{type=audit} = %v, want 1", metricRecords, p.Value)
		}
	})
}
