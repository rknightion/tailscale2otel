package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// TestObjectStoreFeedSignalsMatchTheirAdapters guards a silent, expensive bug.
//
// objectStoreFeed.signal is baked into the durable checkpoint namespace, while
// the actual records are emitted by the SignalProcessor the feed builds. Nothing
// forces the two to agree, and the feeds here spell their signal as a literal
// while the adapters use the semconv constants. If they ever drift, the feed
// keeps its cursor under one name while the engine reports another: every
// deployment cold-starts from initial_lookback and re-emits its history.
func TestObjectStoreFeedSignalsMatchTheirAdapters(t *testing.T) {
	rt := &tailnetRuntime{}
	for _, feed := range []objectStoreFeed{
		objectStoreFlowFeed,
		objectStoreAuditFeed,
		objectStoreK8sAuditFeed,
	} {
		if got := feed.processor(rt).Signal(); got != feed.signal {
			t.Errorf("feed %q builds an adapter reporting signal %q: the checkpoint namespace and "+
				"the emitted signal would disagree", feed.signal, got)
		}
	}
}

// TestObjectStoreFeedSignalsAreTheFrozenConstants pins the wire values. Renaming
// one orphans every existing deployment's cursor and seen set.
func TestObjectStoreFeedSignalsAreTheFrozenConstants(t *testing.T) {
	want := map[string]string{
		objectStoreFlowFeed.signal:     semconv.IngestSignalFlow,
		objectStoreAuditFeed.signal:    semconv.IngestSignalAudit,
		objectStoreK8sAuditFeed.signal: semconv.IngestSignalK8sAudit,
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("feed signal %q != frozen constant %q", got, expected)
		}
	}
	if objectStoreK8sAuditFeed.signal != "k8s_audit" {
		t.Errorf("k8s audit signal = %q, want the frozen wire value \"k8s_audit\"", objectStoreK8sAuditFeed.signal)
	}
}
