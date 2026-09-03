package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestProvider_MetricExportBatchSizeSplitsCollection(t *testing.T) {
	t.Setenv("OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE", "17")

	var (
		mu         sync.Mutex
		batchSizes []int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		var req collectormetricpb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("decode OTLP request: %v", err)
			http.Error(w, "decode", http.StatusBadRequest)
			return
		}
		n := metricRequestDataPoints(&req)
		mu.Lock()
		batchSizes = append(batchSizes, n)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:           "tailscale2otel",
		Protocol:              "http",
		Endpoint:              srv.URL,
		MetricInterval:        time.Hour,
		MetricExportBatchSize: 2,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if got := os.Getenv("OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE"); got != "17" {
		t.Fatalf("OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE = %q after NewProvider, want restored value 17", got)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := p.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{"id": id})
	}
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) < 3 {
		t.Fatalf("HTTP requests = %d (%v), want at least 3", len(batchSizes), batchSizes)
	}
	for i, n := range batchSizes {
		if n > 2 {
			t.Errorf("request %d contains %d datapoints, want at most 2", i, n)
		}
	}
}

func metricRequestDataPoints(req *collectormetricpb.ExportMetricsServiceRequest) int {
	var n int
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch data := m.Data.(type) {
				case *metricpb.Metric_Gauge:
					n += len(data.Gauge.DataPoints)
				case *metricpb.Metric_Sum:
					n += len(data.Sum.DataPoints)
				case *metricpb.Metric_Histogram:
					n += len(data.Histogram.DataPoints)
				case *metricpb.Metric_ExponentialHistogram:
					n += len(data.ExponentialHistogram.DataPoints)
				case *metricpb.Metric_Summary:
					n += len(data.Summary.DataPoints)
				}
			}
		}
	}
	return n
}
