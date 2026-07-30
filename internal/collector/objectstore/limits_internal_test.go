package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	storeapi "github.com/rknightion/tailscale2otel/v4/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func TestDecompress_ZstdWindowLimitTracksObjectLimit(t *testing.T) {
	var encoded bytes.Buffer
	zw, err := zstd.NewWriter(
		&encoded,
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(8<<10),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(bytes.Repeat([]byte("bounded-window-fixture"), 2_000)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	decoded, closeFn, err := decompress(bytes.NewReader(encoded.Bytes()), compZstd, 1<<10)
	if err == nil {
		defer closeFn()
		_, err = io.Copy(io.Discard, decoded)
	}
	if !errors.Is(err, zstd.ErrWindowSizeExceeded) &&
		!errors.Is(err, zstd.ErrDecoderSizeExceeded) {
		t.Fatalf("decode error = %v, want zstd window or memory limit rejection", err)
	}
}

type benchmarkBackend struct {
	body []byte
}

func (*benchmarkBackend) List(
	context.Context,
	string,
	string,
	int,
) (storeapi.ListResult, error) {
	return storeapi.ListResult{}, nil
}

func (b *benchmarkBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

type benchmarkSignal struct{}

func (benchmarkSignal) Signal() string { return "benchmark" }

func (benchmarkSignal) PrepareRecord(
	context.Context,
	[]byte,
	time.Time,
) (PreparedRecord, error) {
	return benchmarkPrepared{}, nil
}

type benchmarkPrepared struct{}

func (benchmarkPrepared) Commit(telemetry.Emitter) RecordTimestamps {
	return RecordTimestamps{}
}

func BenchmarkIngestExpansionLimits(b *testing.B) {
	const (
		objectBytes  = 32 << 20
		objectRows   = 100_000
		cycleBytes   = 256 << 20
		cycleRecords = 500_000
	)
	tests := map[string]struct {
		body []byte
		rows int
	}{
		"representative_13600_rows_at_approx_2.4_KiB": {
			body: bytes.Repeat(append(bytes.Repeat([]byte{'x'}, 2_456), '\n'), 13_600),
			rows: 13_600,
		},
		"record_ceiling_100000_small_rows": {
			body: bytes.Repeat([]byte("{}\n"), objectRows),
			rows: objectRows,
		},
	}
	for name, tc := range tests {
		b.Run(name, func(b *testing.B) {
			collector := &Collector{
				api:    &benchmarkBackend{body: tc.body},
				signal: benchmarkSignal{},
				opts: Options{
					MaxObjectWireBytes:         int64(len(tc.body)) + 1,
					MaxObjectDecompressedBytes: objectBytes,
					MaxObjectRecords:           objectRows,
				},
				now: time.Now,
			}
			emitter := telemetrytest.New().Emitter()
			b.SetBytes(int64(len(tc.body)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result, err := collector.ingest(
					context.Background(),
					"synthetic-benchmark",
					compNone,
					emitter,
					ingestLimits{
						wireBytes:         int64(len(tc.body)) + 1,
						decompressedBytes: cycleBytes,
						records:           cycleRecords,
					},
				)
				if err != nil {
					b.Fatal(err)
				}
				if result.rows != tc.rows || result.acceptedRecords != tc.rows {
					b.Fatalf(
						"rows/accepted = %d/%d, want %d/%d",
						result.rows,
						result.acceptedRecords,
						tc.rows,
						tc.rows,
					)
				}
			}
		})
	}
}
