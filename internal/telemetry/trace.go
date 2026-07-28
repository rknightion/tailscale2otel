package telemetry

import (
	"context"
	"slices"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
)

// newPIISpanExporter wraps next so every exported span is run through the
// configured PII policy — the same internal/telemetry/pii registry that governs
// metric and log attributes — before it leaves the process (#212).
//
// When no category is disabled it returns next unchanged, so the default
// configuration pays nothing: no wrapper, no per-span map allocation.
func newPIISpanExporter(next sdktrace.SpanExporter, cats pii.Categories) sdktrace.SpanExporter {
	if !anyCategoryDisabled(cats) {
		return next
	}
	return piiSpanExporter{next: next, red: pii.New(cats)}
}

// anyCategoryDisabled reports whether cats explicitly turns any PII category off.
// It mirrors the Redactor's own fast-path condition; having it here lets the
// wrapper be skipped entirely rather than constructed as a no-op.
func anyCategoryDisabled(cats pii.Categories) bool {
	for _, cat := range pii.AllCategories {
		if v, ok := cats[cat]; ok && !v {
			return true
		}
	}
	return false
}

// piiSpanExporter applies the PII policy to spans at the exporter boundary.
//
// WHY THE EXPORTER AND NOT A SpanProcessor (this is the non-obvious part):
// a SpanProcessor cannot do this job in either callback.
//
//   - OnStart runs before the instrumented code calls SetAttributes, so there is
//     nothing to filter yet (this is why constAttrSpanProcessor, which only ADDS
//     provider-scoped attributes, can live there).
//   - OnEnd is handed the value returned by (*recordingSpan).snapshot() —
//     go.opentelemetry.io/otel/sdk@v1.44.0/trace/span.go:610-613 — an immutable
//     `snapshot` struct that does NOT implement ReadWriteSpan, so a type assertion
//     back to a writable span fails (verified empirically). Even on a
//     ReadWriteSpan the trace API offers no attribute REMOVAL: SetAttributes can
//     only add or overwrite.
//
// The exporter boundary is therefore the only place the SDK lets an attribute be
// removed: it receives []ReadOnlySpan and nothing constrains what the wrapper
// passes on. Filtering at the SetAttributes call sites was rejected because it
// would push PII policy into every instrumented package (tsapi, stream, webhook,
// collector) and would not cover span attributes added later — the same reason
// the Emitter, not the collectors, owns metric/log redaction.
type piiSpanExporter struct {
	next sdktrace.SpanExporter
	red  *pii.Redactor
}

func (e piiSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := spans
	copied := false
	for i, s := range spans {
		filtered, changed := e.filterSpan(s)
		if !changed {
			continue
		}
		if !copied {
			// Copy-on-write: the SDK owns the batch slice, so only clone it once a
			// span actually needs replacing.
			out = make([]sdktrace.ReadOnlySpan, len(spans))
			copy(out, spans)
			copied = true
		}
		out[i] = filtered
	}
	return e.next.ExportSpans(ctx, out)
}

func (e piiSpanExporter) Shutdown(ctx context.Context) error { return e.next.Shutdown(ctx) }

// filterSpan returns a sanitized view of s, or s itself when the policy changes
// nothing. Four surfaces are covered:
//
//   - span attributes: keys resolving to a disabled category are dropped
//     (Redactor.Merge — the exact policy metrics and logs use);
//   - the status description, which is FREE TEXT and therefore governed by
//     free_text_details exactly like the exception.message carrying the same
//     string (#473). Redactor.RedactStatusDescription owns that decision: with
//     the category off, anything that is not a bounded code-defined class is
//     replaced wholesale; with it on, the description still gets the attr-value
//     scrub below. Sending it through RedactBody alone was the bypass — that
//     only removes values carried as attributes, so disabling free_text_details
//     dropped the exception event and left the identical text in the status;
//   - event / link attribute values: free text that can EMBED a redacted value
//     (internal/tsapi's sanitizeTransportError puts the request URL in both the
//     span status and, via RecordError, exception.message). Redactor.RedactBody
//     scrubs those values, the same #197 mechanism used for log bodies.
func (e piiSpanExporter) filterSpan(s sdktrace.ReadOnlySpan) (sdktrace.ReadOnlySpan, bool) {
	attrs := s.Attributes()
	all := attrMap(attrs)
	kept := e.red.Merge(all)

	f := piiFilteredSpan{ReadOnlySpan: s, attrs: attrs, events: s.Events(), links: s.Links(), status: s.Status()}
	changed := false
	if len(kept) != len(all) {
		f.attrs = keepPresent(attrs, kept)
		changed = true
	}
	if d := e.red.RedactStatusDescription(f.status.Description, all); d != f.status.Description {
		f.status.Description = d
		changed = true
	}
	if evs, ok := e.filterEvents(f.events, all); ok {
		f.events = evs
		changed = true
	}
	if ls, ok := e.filterLinks(f.links, all); ok {
		f.links = ls
		changed = true
	}
	if !changed {
		return s, false
	}
	return f, true
}

// filterEvents returns a sanitized copy of events and true when anything changed.
func (e piiSpanExporter) filterEvents(events []sdktrace.Event, spanAttrs map[string]any) ([]sdktrace.Event, bool) {
	var out []sdktrace.Event
	for i, ev := range events {
		kvs, ok := e.filterKVs(ev.Attributes, spanAttrs)
		if !ok {
			continue
		}
		if out == nil {
			out = make([]sdktrace.Event, len(events))
			copy(out, events)
		}
		out[i].Attributes = kvs
	}
	return out, out != nil
}

// filterLinks returns a sanitized copy of links and true when anything changed.
func (e piiSpanExporter) filterLinks(links []sdktrace.Link, spanAttrs map[string]any) ([]sdktrace.Link, bool) {
	var out []sdktrace.Link
	for i, l := range links {
		kvs, ok := e.filterKVs(l.Attributes, spanAttrs)
		if !ok {
			continue
		}
		if out == nil {
			out = make([]sdktrace.Link, len(links))
			copy(out, links)
		}
		out[i].Attributes = kvs
	}
	return out, out != nil
}

// filterKVs drops redacted keys from kvs and scrubs any redacted span-attribute
// value out of the surviving string values. It returns ok=false when nothing
// changed, so callers can avoid allocating.
func (e piiSpanExporter) filterKVs(kvs []attribute.KeyValue, spanAttrs map[string]any) ([]attribute.KeyValue, bool) {
	if len(kvs) == 0 {
		return nil, false
	}
	own := attrMap(kvs)
	kept := e.red.Merge(own)
	out := kvs
	changed := false
	if len(kept) != len(own) {
		out = keepPresent(kvs, kept)
		changed = true
	}
	for i, kv := range out {
		if kv.Value.Type() != attribute.STRING {
			continue
		}
		scrubbed := e.red.RedactBody(kv.Value.AsString(), nil, spanAttrs)
		if scrubbed == kv.Value.AsString() {
			continue
		}
		if !changed {
			out = slices.Clone(out)
			changed = true
		}
		out[i] = kv.Key.String(scrubbed)
	}
	if !changed {
		return nil, false
	}
	return out, true
}

// attrMap flattens attribute key-values into the map[string]any shape the pii
// Redactor operates on. AsInterface preserves the concrete type, so IP-valued
// keys still classify by value.
func attrMap(kvs []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.AsInterface()
	}
	return m
}

// keepPresent returns the subset of kvs whose keys survived redaction, preserving
// the original order.
func keepPresent(kvs []attribute.KeyValue, kept map[string]any) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(kept))
	for _, kv := range kvs {
		if _, ok := kept[string(kv.Key)]; ok {
			out = append(out, kv)
		}
	}
	return out
}

// piiFilteredSpan is a read-only view over a span with sanitized attributes,
// events, links and status. Embedding the ReadOnlySpan interface (rather than
// implementing it) is required: the interface has an unexported method, so only
// promotion can satisfy it outside the SDK package. Everything not overridden —
// name, span context, timings, resource, scope, dropped counts — passes straight
// through to the wrapped span.
type piiFilteredSpan struct {
	sdktrace.ReadOnlySpan
	attrs  []attribute.KeyValue
	events []sdktrace.Event
	links  []sdktrace.Link
	status sdktrace.Status
}

func (s piiFilteredSpan) Attributes() []attribute.KeyValue { return s.attrs }
func (s piiFilteredSpan) Events() []sdktrace.Event         { return s.events }
func (s piiFilteredSpan) Links() []sdktrace.Link           { return s.links }
func (s piiFilteredSpan) Status() sdktrace.Status          { return s.status }
