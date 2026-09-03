package app

import (
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

// flowRedactor builds the PII gate the persistent flow store applies before a
// row reaches disk, or nil when nothing is filtered.
//
// # Why this exists only for the persistent backend
//
// internal/flowlog deliberately hands the flow store UNFILTERED identity
// (#241), and its stated reason is that "the store is in memory, never leaves
// the process, and is readable only through the admin-authenticated /flows
// surface". That reasoning is sound and stays intact for the in-memory ring.
//
// It stops being true the moment rows are written to a file. A database on disk
// outlives the process, gets copied into backups, and is readable by anything
// that can read the path — none of which is true of a map in RAM. pii_filter is
// the operator's statement of which identifiers may leave this process, and a
// durable file is leaving the process. So the persistent path filters and the
// memory path does not, and that asymmetry is the point rather than an
// inconsistency.
//
// This reuses telemetry's own Redactor rather than reimplementing the rules, so
// a field is classified here exactly as the same value would be on the OTLP
// path — including the IP-class distinction between tailscale, internal and
// external addresses, which depends on the VALUE and not just the key.
func flowRedactor(cats pii.Categories) func(*flowstore.Observation) {
	// Nothing disabled means every field survives, so skip the work entirely
	// rather than round-tripping a map per observation on the write path. The
	// Redactor keeps that same "any category off" flag internally but does not
	// export it, and an absent key counts as enabled — so the test is an
	// explicit false, matching pii.New's own construction.
	anyOff := false
	for _, on := range cats {
		if !on {
			anyOff = true
			break
		}
	}
	if !anyOff {
		return nil
	}

	r := pii.New(cats)

	return func(o *flowstore.Observation) {
		// Only the identity-bearing fields go through the redactor. Counts,
		// timestamps, transport, traffic type, verdict, rule and path carry no
		// identifier and must survive redaction — losing them would leave a row
		// that is neither useful nor honest, rather than a redacted one.
		attrs := map[string]any{}
		put := func(key, val string) {
			if val != "" {
				attrs[key] = val
			}
		}
		put(semconv.SourceAddress, o.SrcAddr)
		put(semconv.DestinationAddress, o.DstAddr)
		put(semconv.AttrSrcNode, o.SrcNode)
		put(semconv.AttrDstNode, o.DstNode)
		put(semconv.AttrDstService, o.DstService)
		put(semconv.AttrSrcUser, o.SrcUser)
		put(semconv.AttrDstUser, o.DstUser)
		put(semconv.AttrSrcTags, o.SrcTags)
		put(semconv.AttrDstTags, o.DstTags)
		put(semconv.AttrSrcOS, o.SrcOS)
		put(semconv.AttrDstOS, o.DstOS)
		put(semconv.AttrNodeID, o.ReporterNodeID)

		kept := r.Merge(attrs)

		// A key the redactor dropped is one the operator has said must not leave
		// the process, so it is blanked rather than written. Blank already means
		// "the record did not carry it" everywhere downstream, so a redacted row
		// renders as an incomplete one instead of a broken one.
		clear := func(key string, dst *string) {
			if _, ok := kept[key]; !ok {
				*dst = ""
			}
		}
		clear(semconv.SourceAddress, &o.SrcAddr)
		clear(semconv.DestinationAddress, &o.DstAddr)
		clear(semconv.AttrSrcNode, &o.SrcNode)
		clear(semconv.AttrDstNode, &o.DstNode)
		clear(semconv.AttrDstService, &o.DstService)
		clear(semconv.AttrSrcUser, &o.SrcUser)
		clear(semconv.AttrDstUser, &o.DstUser)
		clear(semconv.AttrSrcTags, &o.SrcTags)
		clear(semconv.AttrDstTags, &o.DstTags)
		clear(semconv.AttrSrcOS, &o.SrcOS)
		clear(semconv.AttrDstOS, &o.DstOS)
		clear(semconv.AttrNodeID, &o.ReporterNodeID)
	}
}
