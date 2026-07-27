package objectstore_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func TestCollect_ZstdSubKiBLimitAcceptsValidTinyFrame(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("tiny", at) + "\n"
	if len(raw) >= zstd.MinWindowSize {
		t.Fatalf("fixture bytes = %d, want less than zstd minimum window %d", len(raw), zstd.MinWindowSize)
	}
	h := newHarness(t, func(o *objectstore.Options) {
		o.MaxObjectDecompressedBytes = int64(len(raw))
		o.MaxObjectRecords = 1
		o.MaxCycleDecompressedBytes = int64(len(raw))
		o.MaxCycleRecords = 1
	})
	h.store.put(
		officialKeyAt(at, ".json.zst"),
		zstdStreamBytes(t, raw, zstd.MinWindowSize),
	)

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want one valid sub-1KiB zstd object", got)
	}
	if got := metricTotal(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
	); got != 0 {
		t.Fatalf("expansion limit failures = %v, want 0", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("gaps = %v, want 0", got)
	}
}

func TestCollect_ZstdFrameSizeExceededIsReadFailureNotExpansionLimit(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("corrupt-fcs", at) + "\n"
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		o.MaxObjectDecompressedBytes = int64(len(raw) * 2)
		o.MaxObjectRecords = 2
		o.MaxCycleDecompressedBytes = int64(len(raw) * 2)
		o.MaxCycleRecords = 2
	})
	h.store.put(
		officialKeyAt(at, ".json.zst"),
		zstdFrameSizeExceededBytes(t, raw),
	)

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want 0 from a corrupt zstd frame", got)
	}
	if got := metricTotal(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
	); got != 0 {
		t.Fatalf("expansion limit failures = %v, want 0 for corrupt frame data", got)
	}
	if got := skippedByReason(h.rec)["read_error"]; got != 1 {
		t.Fatalf("read-error skips = %v, want 1", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gaps = %v, want one corrupt-object gap", got)
	}
	if !strings.Contains(logs.String(), "stage=read") {
		t.Fatalf("corrupt frame diagnostic = %q, want stage=read", logs.String())
	}
}

func TestCollect_ZstdDecoderLimitDiagnosticReportsLowerBound(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := strings.Repeat(record("window", at)+"\n", 10)
	const limit = int64(zstd.MinWindowSize)
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		o.MaxObjectDecompressedBytes = limit
		o.MaxObjectRecords = 20
		o.MaxCycleDecompressedBytes = 2 * limit
		o.MaxCycleRecords = 20
	})
	h.store.put(
		officialKeyAt(at, ".json.zst"),
		zstdStreamBytes(t, raw, 8<<10),
	)

	h.collect(t)

	if got := metricTotalWithAttr(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
		"limit",
		"object_bytes",
	); got != 1 {
		t.Fatalf("object byte limit failures = %v, want 1", got)
	}
	for _, want := range []string{
		"measured_at_least=1025",
		"configured_limit=1024",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("decoder limit diagnostic = %q, want %q", logs.String(), want)
		}
	}
}

func TestCollect_DecompressedBytesLimitQuarantinesObject(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("first", at) + "\n" + record("second", at) + "\n"
	firstRowBytes := int64(len(record("first", at)) + 1)

	tests := map[string]struct {
		extension string
		encode    func(*testing.T, string) []byte
	}{
		"gzip": {extension: ".json.gz", encode: gzipBytes},
		"zstd": {extension: ".json.zst", encode: zstdBytes},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			clock := now
			var ingestCalls int
			var logs bytes.Buffer
			h := newHarness(t, func(o *objectstore.Options) {
				o.Now = func() time.Time { return clock }
				o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
				o.MaxObjectDecompressedBytes = firstRowBytes
				o.MaxObjectRecords = 10
				o.MaxCycleDecompressedBytes = int64(len(raw) * 2)
				o.MaxCycleRecords = 20
				o.OnIngest = func(_, _ string, _, _ int) { ingestCalls++ }
			})
			key := officialKeyAt(at, tc.extension)
			h.store.put(key, tc.encode(t, raw))

			h.collect(t)

			if got := h.flowRecords(); got != 0 {
				t.Fatalf("flow records = %d, want 0 from an over-limit object", got)
			}
			if ingestCalls != 0 {
				t.Fatalf("OnIngest calls = %d, want 0", ingestCalls)
			}
			if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 0 {
				t.Fatalf("successful objects = %v, want 0", got)
			}
			if got := metricTotalWithAttr(
				h.rec,
				"tailscale2otel.objectstore.expansion.limit_failures",
				"limit",
				"object_bytes",
			); got != 1 {
				t.Fatalf("object byte limit failures = %v, want 1", got)
			}
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
				t.Fatalf("gaps = %v, want one quarantined gap", got)
			}
			for _, want := range []string{
				"stage=decompressed_bytes_limit",
				"measured=",
				"configured_limit=",
			} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("limit diagnostic = %q, want %q", logs.String(), want)
				}
			}
			assertQuarantineDisposition(t, h.cp.Keys(), key)

			clock = now.Add(24 * time.Hour)
			h.collect(t)
			if got := countString(h.store.getCalls, key); got != 1 {
				t.Fatalf("GET calls after quarantine = %d, want 1", got)
			}
		})
	}
}

func TestCollect_RecordsLimitCountsEveryNonBlankRow(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("first", at) + "\n\n \t\n" +
		record("second", at) + "\n" +
		record("third", at) + "\n"
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		o.MaxObjectDecompressedBytes = int64(len(raw) + 1)
		o.MaxObjectRecords = 2
		o.MaxCycleDecompressedBytes = int64(len(raw) * 2)
		o.MaxCycleRecords = 10
	})
	key := officialKeyAt(at, ".json")
	h.store.put(key, []byte(raw))

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want 0 from an over-limit object", got)
	}
	if got := metricTotalWithAttr(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
		"limit",
		"object_records",
	); got != 1 {
		t.Fatalf("object record limit failures = %v, want 1", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gaps = %v, want one quarantined gap", got)
	}
	for _, want := range []string{
		"stage=records_limit",
		"measured=3",
		"configured_limit=2",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("limit diagnostic = %q, want %q", logs.String(), want)
		}
	}
	assertQuarantineDisposition(t, h.cp.Keys(), key)
}

func TestCollect_LimitsIgnoreBlankRowsButCountTheirBytes(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("first", at) + "\n\n \t\n" + record("second", at) + "\n"
	h := newHarness(t, func(o *objectstore.Options) {
		o.MaxObjectDecompressedBytes = int64(len(raw))
		o.MaxObjectRecords = 2
		o.MaxCycleDecompressedBytes = int64(len(raw))
		o.MaxCycleRecords = 2
	})
	h.store.put(officialKeyAt(at, ".json"), []byte(raw))

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Fatalf("flow records = %d, want 2; blank rows must not consume record budget", got)
	}
	if got := metricTotal(
		h.rec,
		"tailscale2otel.objectstore.decompressed.bytes",
	); got != float64(len(raw)) {
		t.Fatalf("decompressed bytes = %v, want %d including blank rows", got, len(raw))
	}
	if got := metricTotal(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
	); got != 0 {
		t.Fatalf("limit failures = %v, want 0", got)
	}
}

func TestCollect_LimitAccountingUsesActualCompressedBytes(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("wire-count", at) + "\n"
	compressed := gzipBytes(t, raw)
	var gotIngestBytes int
	h := newHarness(t, func(o *objectstore.Options) {
		o.MaxObjectDecompressedBytes = int64(len(raw))
		o.MaxObjectRecords = 1
		o.MaxCycleDecompressedBytes = int64(len(raw))
		o.MaxCycleRecords = 1
		o.OnIngest = func(_, _ string, _ int, bytes int) {
			gotIngestBytes = bytes
		}
	})
	key := officialKeyAt(at, ".json.gz")
	h.store.put(key, compressed)
	h.store.sizes[key] = 1 // The listing is advisory and deliberately wrong.

	h.collect(t)

	// tailscale2otel.objectstore.bytes counts actual compressed wire bytes read
	// from the GET, never the (deliberately wrong) advisory listing size.
	if got := metricTotal(h.rec, "tailscale2otel.objectstore.bytes"); got != float64(len(compressed)) {
		t.Fatalf("compressed bytes metric = %v, want actual GET bytes %d", got, len(compressed))
	}
	// OnIngest's bytes mean decoded/decompressed payload size (see
	// TestCollect_OnIngestReportsDecompressedNotWireBytes) — not the compressed
	// wire size asserted above, and not the wrong advisory listing size either.
	if gotIngestBytes != len(raw) {
		t.Fatalf("OnIngest bytes = %d, want the decompressed size %d", gotIngestBytes, len(raw))
	}
}

func TestCollect_WireBytesLimitQuarantinesObjectWithoutPartialEmission(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	raw := record("wire-limit", at) + "\n"
	compressed := gzipBytes(t, raw)
	const limit = int64(16)
	if int64(len(compressed)) <= limit {
		t.Fatalf("compressed fixture bytes = %d, want more than limit %d", len(compressed), limit)
	}
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		o.MaxObjectWireBytes = limit
		o.MaxObjectDecompressedBytes = int64(len(raw) * 2)
		o.MaxObjectRecords = 2
		o.MaxCycleWireBytes = int64(len(compressed) * 2)
		o.MaxCycleDecompressedBytes = int64(len(raw) * 2)
		o.MaxCycleRecords = 2
	})
	key := officialKeyAt(at, ".json.gz")
	h.store.put(key, compressed)

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want 0 from an over-wire-limit object", got)
	}
	if got := metricTotalWithAttr(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
		"limit",
		"object_wire_bytes",
	); got != 1 {
		t.Fatalf("object wire-byte limit failures = %v, want 1", got)
	}
	for _, want := range []string{
		"stage=wire_bytes_limit",
		"measured=17",
		"configured_limit=16",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("wire-limit diagnostic = %q, want %q", logs.String(), want)
		}
	}
	assertQuarantineDisposition(t, h.cp.Keys(), key)
}

func TestCollect_CycleWireLimitDefersObjectWithoutGapAndRetries(t *testing.T) {
	at1 := now.Add(-12 * time.Minute)
	at2 := now.Add(-11 * time.Minute)
	first := record("first-wire", at1) + "\n"
	second := record("second-wire", at2) + "\n"
	firstCompressed := gzipBytes(t, first)
	secondCompressed := gzipBytes(t, second)
	cycleLimit := int64(len(firstCompressed) + len(secondCompressed) - 1)

	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		o.MaxObjectWireBytes = int64(max(len(firstCompressed), len(secondCompressed)))
		o.MaxObjectDecompressedBytes = int64(max(len(first), len(second)))
		o.MaxObjectRecords = 1
		o.MaxCycleWireBytes = cycleLimit
		o.MaxCycleDecompressedBytes = int64(len(first) + len(second))
		o.MaxCycleRecords = 2
	})
	key1 := officialKeyAt(at1, ".json.gz")
	key2 := officialKeyAt(at2, ".json.gz")
	h.store.put(key1, firstCompressed)
	h.store.put(key2, secondCompressed)

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("first-cycle flow records = %d, want only first object", got)
	}
	if got := countString(h.store.getCalls, key2); got != 1 {
		t.Fatalf("shortfall object GET calls = %d, want one bounded attempt", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("first-cycle gaps = %v, want 0", got)
	}
	if got := metricTotalWithAttr(
		h.rec,
		"tailscale2otel.objectstore.expansion.limit_failures",
		"limit",
		"cycle_wire_bytes",
	); got != 1 {
		t.Fatalf("cycle wire-byte limit failures = %v, want 1", got)
	}
	for _, want := range []string{
		"measured=",
		"configured_limit=",
		"object deferred",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("cycle wire diagnostic = %q, want %q", logs.String(), want)
		}
	}

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Fatalf("second-cycle flow records = %d, want deferred object retried", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("second-cycle gaps = %v, want 0", got)
	}
}

func TestCollect_WireLimitBoundsZeroOutputCompressionWork(t *testing.T) {
	at := now.Add(-10 * time.Minute)
	const limit = 64
	tests := map[string]struct {
		extension string
		body      func() []byte
	}{
		"concatenated empty gzip members": {
			extension: ".json.gz",
			body: func() []byte {
				member := gzipBytes(t, "")
				return bytes.Repeat(member, limit/len(member)+2)
			},
		},
		"zstd skippable frames": {
			extension: ".json.zst",
			body: func() []byte {
				var frame [9]byte
				binary.LittleEndian.PutUint32(frame[0:4], 0x184d2a50)
				binary.LittleEndian.PutUint32(frame[4:8], 1)
				return bytes.Repeat(frame[:], limit/len(frame)+2)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			body := tc.body()
			h := newHarness(t, func(o *objectstore.Options) {
				o.MaxObjectWireBytes = limit
				o.MaxObjectDecompressedBytes = 1024
				o.MaxObjectRecords = 1
				o.MaxCycleWireBytes = int64(len(body) * 2)
				o.MaxCycleDecompressedBytes = 2048
				o.MaxCycleRecords = 2
			})
			h.store.put(officialKeyAt(at, tc.extension), body)

			h.collect(t)

			if got := h.flowRecords(); got != 0 {
				t.Fatalf("flow records = %d, want 0", got)
			}
			if got := metricTotalWithAttr(
				h.rec,
				"tailscale2otel.objectstore.expansion.limit_failures",
				"limit",
				"object_wire_bytes",
			); got != 1 {
				t.Fatalf("wire limit failures = %v, want 1", got)
			}
			if got := h.store.readBytes; got != limit+1 {
				t.Fatalf("backend bytes read = %d, want exactly bounded probe %d", got, limit+1)
			}
		})
	}
}

func TestCollect_CycleLimitDefersUntouchedCandidatesWithoutGap(t *testing.T) {
	at1 := now.Add(-12 * time.Minute)
	at2 := now.Add(-11 * time.Minute)
	first := record("first1", at1) + "\n"
	second := record("second", at2) + "\n"

	tests := map[string]func(*objectstore.Options){
		"bytes": func(o *objectstore.Options) {
			o.MaxObjectDecompressedBytes = int64(max(len(first), len(second)))
			o.MaxObjectRecords = 2
			o.MaxCycleDecompressedBytes = int64(len(first))
			o.MaxCycleRecords = 2
		},
		"records": func(o *objectstore.Options) {
			o.MaxObjectDecompressedBytes = int64(len(first) + len(second))
			o.MaxObjectRecords = 1
			o.MaxCycleDecompressedBytes = int64(len(first) + len(second))
			o.MaxCycleRecords = 1
		},
	}
	for name, tune := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tune)
			key1 := officialKeyAt(at1, ".json")
			key2 := officialKeyAt(at2, ".json")
			h.store.put(key1, []byte(first))
			h.store.put(key2, []byte(second))

			h.collect(t)

			if got := h.flowRecords(); got != 1 {
				t.Fatalf("first-cycle flow records = %d, want 1", got)
			}
			if got := h.store.getCalls; len(got) != 1 || got[0] != key1 {
				t.Fatalf("first-cycle GET calls = %v, want only [%q]", got, key1)
			}
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
				t.Fatalf("first-cycle gaps = %v, want 0", got)
			}

			h.collect(t)

			if got := h.flowRecords(); got != 2 {
				t.Fatalf("second-cycle flow records = %d, want deferred object ingested", got)
			}
			if got := countString(h.store.getCalls, key2); got != 1 {
				t.Fatalf("deferred object GET calls = %d, want 1 on the next cycle", got)
			}
			if got := metricTotal(
				h.rec,
				"tailscale2otel.objectstore.expansion.limit_failures",
			); got != 0 {
				t.Fatalf("cycle boundary limit failures = %v, want 0", got)
			}
		})
	}
}

func TestCollect_CycleLimitDefersObjectThatDoesNotFitRemainingBudget(t *testing.T) {
	at1 := now.Add(-13 * time.Minute)
	at2 := now.Add(-12 * time.Minute)
	at3 := now.Add(-11 * time.Minute)
	first := record("first", at1) + "\n"
	second := record("second-a", at2) + "\n" + record("second-b", at2) + "\n"
	third := record("third", at3) + "\n"

	tests := map[string]func(*objectstore.Options){
		"bytes": func(o *objectstore.Options) {
			o.MaxObjectDecompressedBytes = int64(len(second))
			o.MaxObjectRecords = 2
			o.MaxCycleDecompressedBytes = int64(len(first) + len(second) - 1)
			o.MaxCycleRecords = 10
		},
		"records": func(o *objectstore.Options) {
			o.MaxObjectDecompressedBytes = int64(len(first) + len(second))
			o.MaxObjectRecords = 2
			o.MaxCycleDecompressedBytes = int64(len(first) + len(second))
			o.MaxCycleRecords = 2
		},
	}
	for name, tune := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, tune)
			key1 := officialKeyAt(at1, ".json")
			key2 := officialKeyAt(at2, ".json")
			key3 := officialKeyAt(at3, ".json")
			h.store.put(key1, []byte(first))
			h.store.put(key2, []byte(second))
			h.store.put(key3, []byte(third))

			h.collect(t)

			if got := h.flowRecords(); got != 1 {
				t.Fatalf("first-cycle flow records = %d, want only the fitting first object", got)
			}
			if got := countString(h.store.getCalls, key2); got != 1 {
				t.Fatalf("shortfall object GET calls = %d, want one bounded admission attempt", got)
			}
			if got := countString(h.store.getCalls, key3); got != 0 {
				t.Fatalf("later untouched object GET calls = %d, want 0", got)
			}
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
				t.Fatalf("cycle shortfall gaps = %v, want 0", got)
			}
			if got := metricTotalWithAttr(
				h.rec,
				"tailscale2otel.objectstore.expansion.limit_failures",
				"limit",
				"cycle_"+name,
			); got != 1 {
				t.Fatalf("cycle %s limit failures = %v, want 1", name, got)
			}

			h.collect(t)

			if got := h.flowRecords(); got != 3 {
				t.Fatalf("second-cycle flow records = %d, want the deferred two-row object", got)
			}
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
				t.Fatalf("second-cycle gaps = %v, want 0", got)
			}
		})
	}
}

func metricTotal(rec *telemetrytest.Recorder, name string) float64 {
	var total float64
	for _, point := range rec.MetricPoints(name) {
		total += point.Value
	}
	return total
}

func metricTotalWithAttr(
	rec *telemetrytest.Recorder,
	name, attr, value string,
) float64 {
	var total float64
	for _, point := range rec.MetricPoints(name) {
		if point.Attrs[attr] == value {
			total += point.Value
		}
	}
	return total
}

func assertQuarantineDisposition(t *testing.T, keys []string, identity string) {
	t.Helper()
	var gap, seen bool
	for _, key := range keys {
		gap = gap || strings.Contains(key, "/gap/quarantined/")
		seen = seen || strings.Contains(key, "/seen/")
	}
	if !gap {
		t.Fatal("checkpoint has no quarantined durable gap")
	}
	if !seen {
		t.Fatalf("checkpoint has no paired seen disposition for %q", identity)
	}
}

func countString(values []string, want string) int {
	var count int
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func zstdStreamBytes(t *testing.T, raw string, window int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	zw, err := zstd.NewWriter(
		&encoded,
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(window),
		zstd.WithEncoderCRC(false),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(zw, raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func zstdFrameSizeExceededBytes(t *testing.T, raw string) []byte {
	t.Helper()
	zw, err := zstd.NewWriter(
		nil,
		zstd.WithSingleSegment(true),
		zstd.WithEncoderCRC(false),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer zw.Close()
	encoded := zw.EncodeAll([]byte(raw), nil)
	if len(raw) > 255 || len(encoded) < 6 || encoded[4]&0x20 == 0 {
		t.Fatalf(
			"fixture is not a one-byte-FCS single-segment frame: raw=%d encoded=%x",
			len(raw),
			encoded[:min(len(encoded), 8)],
		)
	}
	encoded[5]--

	zr, err := zstd.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("open corrupt-frame fixture: %v", err)
	}
	_, decodeErr := io.ReadAll(zr)
	zr.Close()
	if !errors.Is(decodeErr, zstd.ErrFrameSizeExceeded) {
		t.Fatalf(
			"corrupt-frame fixture error = %v, want %v",
			decodeErr,
			zstd.ErrFrameSizeExceeded,
		)
	}
	return encoded
}
