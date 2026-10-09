package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
	"github.com/rknightion/tailscale2otel/v5/internal/webhook"
)

// ---------------------------------------------------------------------------
// Real runtime fixture. The provider is the production telemetry.Provider with
// its scheduled reader, writing through the real stdout exporters into one
// serialized sink shared by metrics and logs (testIngressSink). The receivers,
// the coordinator, the WAL store and the flow/audit/webhook processors are the
// production ones. Every assertion below reads ACTUAL exported datapoints and
// log records from that sink; request counts, ForceFlush calls and SDK reader
// internals are never cadence proof.
// ---------------------------------------------------------------------------

type cadenceRuntimeDevices struct{}

func (cadenceRuntimeDevices) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	return []tsapi.RichDevice{{ID: "synthetic-1", NodeID: "node-1", OS: "linux", Authorized: true}, {ID: "synthetic-2", NodeID: "node-2", OS: "linux", Authorized: true}, {ID: "synthetic-3", NodeID: "node-3", OS: "linux", Authorized: true}}, nil
}
func (cadenceRuntimeDevices) DevicePostureAttributes(context.Context, string) (tsapi.DeviceAttributes, error) {
	return tsapi.DeviceAttributes{}, nil
}
func (cadenceRuntimeDevices) DeviceInvites(context.Context, string) ([]tsapi.DeviceInvite, error) {
	return nil, nil
}

type cadenceOptions struct {
	interval    time.Duration
	temporality string
	mode        string
	batch       int    // MetricExportBatchSize; 0 keeps the unsplit direct-package behavior
	dir         string // WAL directory; "" uses a fresh temp directory
	noWorker    bool
	observe     bool // record every actual SDK collection via the borrowed observer
	producers   []sdkmetric.Producer
	config      func(*config.Config)
	telemetry   func(*telemetry.Options)
}

type cadenceRuntimeFixture struct {
	app      *App
	provider *telemetry.Provider
	devices  *devices.Collector
	sink     *testIngressSink
	epoch    time.Time
	cancel   context.CancelFunc
	done     chan error
	closed   bool

	obsMu        sync.Mutex
	observations []cadenceObservation
}

type cadenceObservation struct {
	at        time.Duration
	discarded bool
	failed    bool
}

func newCadenceFixture(t *testing.T, o cadenceOptions) *cadenceRuntimeFixture {
	t.Helper()
	if o.mode == "" {
		o.mode = "both"
	}
	sink := &testIngressSink{}
	opts := telemetry.Options{Protocol: "stdout", StdoutWriter: sink, ServiceName: "synthetic-cadence", MetricInterval: o.interval, MetricTemporality: o.temporality, MetricExportBatchSize: o.batch, MetricProducers: o.producers}
	if o.telemetry != nil {
		o.telemetry(&opts)
	}
	epoch := time.Now()
	p, err := telemetry.NewProvider(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Token = "synthetic-token"
	cfg.Webhook.Enabled = true
	cfg.Webhook.Secret = "synthetic-secret"
	cfg.Cardinality.Flow.MetricsMode = o.mode
	cfg.Collectors.Flowlogs.LogMode = "per_connection"
	cfg.IngressWAL.Enabled = true
	cfg.IngressWAL.Directory = o.dir
	if o.dir == "" {
		cfg.IngressWAL.Directory = t.TempDir()
	}
	if o.config != nil {
		o.config(cfg)
	}
	// The process emitter and the tailnet runtime share ONE provider here: the
	// same topology as a Headscale deployment, where the runtime shares the
	// process provider and unrelated process self-observability rides along.
	a := newAppShell(cfg, "cadence", slog.New(slog.NewTextHandler(io.Discard, nil)), p.Emitter(), p.Tracer(), p.Shutdown, collector.NewMemoryStore())
	a.buildProcessDeps()
	client, err := tsapi.NewClient(tsapi.Options{Tailnet: "example.com", BaseURL: "http://127.0.0.1:0", APIKey: "synthetic-local"})
	if err != nil {
		t.Fatal(err)
	}
	a.addRuntimeConfigured("example.com", "example.com", p.Emitter(), nil, nil, p, provider.Tailscale(client), false)
	if err := a.buildIngressWAL(a.buildReceivers()); err != nil {
		t.Fatal(err)
	}
	f := &cadenceRuntimeFixture{app: a, provider: p, sink: sink, epoch: epoch, devices: devices.New(cadenceRuntimeDevices{}, enrich.NewDeviceCache(), o.interval, false, false, devices.WithPerEntity(false))}
	if o.observe {
		if err := p.SetCollectionObserver(func(c telemetry.CollectionObservation) {
			f.obsMu.Lock()
			f.observations = append(f.observations, cadenceObservation{at: time.Since(epoch), discarded: c.Discarded, failed: c.Err != nil})
			f.obsMu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !o.noWorker {
		f.startWorker()
		// Return only once the worker's first pass has finished and it is parked
		// on its wake signal, as production opens receivers only after the
		// startup drain. Otherwise that first pass can be scheduled between a
		// caller's first append and the appender's post-write re-check, apply the
		// just-written original there, and turn the admission into a 503 when
		// that application fails the coordinator (TSO-0159: the poisoned-original
		// stage). Waiting advances no fake time.
		synctest.Wait()
	}
	t.Cleanup(func() {
		if err := f.close(); err != nil {
			t.Errorf("terminal cleanup: %v", err)
		}
	})
	return f
}

func (f *cadenceRuntimeFixture) startWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	go func() { f.done <- f.app.ingressWAL.Run(ctx) }()
}

func (f *cadenceRuntimeFixture) stopWorker() error {
	if f.cancel == nil {
		return nil
	}
	f.cancel()
	err := <-f.done
	f.cancel = nil
	return err
}

// close is the terminal cleanup. It runs only AFTER every assertion; nothing
// it exports is used as normal-cadence or completion evidence.
func (f *cadenceRuntimeFixture) close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	f.sink.mu.Lock()
	f.sink.fail, f.sink.failIf = false, nil
	if f.sink.block != nil {
		close(f.sink.block)
		f.sink.block = nil
	}
	f.sink.mu.Unlock()
	workerErr := f.stopWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(workerErr, f.app.Close(ctx))
}

// abandon tears down a fixture whose WAL was deliberately closed (a simulated
// process death) or that deliberately failed closed. Its terminal errors are
// the expected consequence of that setup, never evidence either way.
func (f *cadenceRuntimeFixture) abandon() {
	if f.closed {
		return
	}
	_ = f.close()
}

func cadenceFlowBody(i int) string {
	return fmt.Sprintf(`{"event":{"nodeId":"synthetic-%d","start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::%x]:443","txBytes":10,"rxBytes":20,"txPkts":1,"rxPkts":2}]}}`, i, i+1)
}

func cadenceWebhookBody(i int) string {
	return fmt.Sprintf(`[{"timestamp":"2000-01-01T00:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"synthetic-%d","data":{"nodeID":"synthetic-%d"}}]`, i, i)
}

func (f *cadenceRuntimeFixture) post(isWebhook bool, body string) int {
	return f.postResponse(isWebhook, body).Code
}

func (f *cadenceRuntimeFixture) postResponse(isWebhook bool, body string) *httptest.ResponseRecorder {
	handler, path := f.app.streamSrv.Handler(), f.app.cfg.Streaming.Path
	if isWebhook {
		handler, path = f.app.webhookSrv.Handler(), f.app.cfg.Webhook.Path
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if isWebhook {
		stamp := fmt.Sprint(time.Now().Unix())
		mac := hmac.New(sha256.New, []byte("synthetic-secret"))
		_, _ = mac.Write([]byte(stamp + "." + body))
		req.Header.Set("Tailscale-Webhook-Signature", fmt.Sprintf("t=%s,v1=%x", stamp, mac.Sum(nil)))
	} else {
		req.SetBasicAuth("", "synthetic-token")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func (f *cadenceRuntimeFixture) admitBody(t *testing.T, isWebhook bool, body string) {
	t.Helper()
	if response := f.postResponse(isWebhook, body); response.Code != http.StatusOK {
		t.Fatalf("original admission: HTTP %d %q; coordinator %+v; delivery admission %v",
			response.Code, strings.TrimSpace(response.Body.String()), f.app.ingressWAL.Health(), f.provider.IngressFailure())
	}
}

func (f *cadenceRuntimeFixture) admit(t *testing.T, i int, isWebhook bool) {
	t.Helper()
	body := cadenceFlowBody(i)
	if isWebhook {
		body = cadenceWebhookBody(i)
	}
	f.admitBody(t, isWebhook, body)
}

func (f *cadenceRuntimeFixture) refreshDevices(t *testing.T) {
	t.Helper()
	if err := f.devices.Collect(context.Background(), f.provider.Emitter()); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Parsing actual exported output.
// ---------------------------------------------------------------------------

type cadenceRuntimePoint struct {
	Time, StartTime time.Time
	Value           float64
	Attributes      json.RawMessage
}
type cadenceRuntimeMetric struct {
	Name string
	Data struct {
		DataPoints  []cadenceRuntimePoint
		Temporality string
		IsMonotonic bool
	}
}
type cadenceRuntimeSignal struct {
	Resource     json.RawMessage
	ScopeMetrics []struct {
		Scope   json.RawMessage
		Metrics []cadenceRuntimeMetric
	}
	EventName  string
	Body       json.RawMessage
	Attributes json.RawMessage
}

func cadenceRuntimeSignals(t *testing.T, raw []byte) []cadenceRuntimeSignal {
	t.Helper()
	var result []cadenceRuntimeSignal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var signal cadenceRuntimeSignal
		err := decoder.Decode(&signal)
		if err == io.EOF {
			return result
		}
		if err != nil {
			t.Fatalf("actual stdout decode: %v", err)
		}
		result = append(result, signal)
	}
}

func cadenceRuntimePoints(signals []cadenceRuntimeSignal, name string) []cadenceRuntimePoint {
	var result []cadenceRuntimePoint
	for _, signal := range signals {
		for _, scope := range signal.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name == name {
					result = append(result, metric.Data.DataPoints...)
				}
			}
		}
	}
	return result
}

func cadenceRuntimeMetrics(signals []cadenceRuntimeSignal, name string) []cadenceRuntimeMetric {
	var result []cadenceRuntimeMetric
	for _, signal := range signals {
		for _, scope := range signal.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name == name {
					result = append(result, metric)
				}
			}
		}
	}
	return result
}

// cadenceRuntimeLatestTotal sums the LATEST cumulative value of every series:
// repeated cumulative snapshots replace, never add.
func cadenceRuntimeLatestTotal(signals []cadenceRuntimeSignal, name string) float64 {
	return cadenceLatestTotalWhere(signals, name, func(map[string]string) bool { return true })
}

func cadenceLatestTotalWhere(signals []cadenceRuntimeSignal, name string, keep func(map[string]string) bool) float64 {
	latest := make(map[string]cadenceRuntimePoint)
	for _, point := range cadenceRuntimePoints(signals, name) {
		key := string(point.Attributes)
		previous, ok := latest[key]
		if !ok || !point.Time.Before(previous.Time) {
			latest[key] = point
		}
	}
	var total float64
	for _, point := range latest {
		if keep(cadenceAttrs(point.Attributes)) {
			total += point.Value
		}
	}
	return total
}

func cadenceAttrs(raw json.RawMessage) map[string]string {
	var kvs []struct {
		Key   string
		Value struct{ Value any }
	}
	_ = json.Unmarshal(raw, &kvs)
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.Key] = fmt.Sprint(kv.Value.Value)
	}
	return out
}

func cadenceAttrKeys(raw json.RawMessage) string {
	keys := make([]string, 0)
	for key := range cadenceAttrs(raw) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

type cadenceLog struct{ EventName, Body string }

func cadenceLogs(signals []cadenceRuntimeSignal) []cadenceLog {
	var result []cadenceLog
	for _, signal := range signals {
		if signal.EventName == "" {
			continue
		}
		var body struct{ Value any }
		_ = json.Unmarshal(signal.Body, &body)
		result = append(result, cadenceLog{EventName: signal.EventName, Body: fmt.Sprint(body.Value)})
	}
	return result
}

func (f *cadenceRuntimeFixture) signals(t *testing.T) []cadenceRuntimeSignal {
	t.Helper()
	return cadenceRuntimeSignals(t, f.sink.Bytes())
}

// devicesTimes returns the actual exported timestamps of the stable unrelated
// collector series relative to the provider epoch, failing on any value != 3.
func (f *cadenceRuntimeFixture) devicesTimes(t *testing.T) []time.Duration {
	t.Helper()
	var got []time.Duration
	for _, point := range cadenceRuntimePoints(f.signals(t), "tailscale.devices.count") {
		if point.Value != 3 {
			t.Fatalf("stable devices sample value=%v at %v, want 3", point.Value, point.Time.Sub(f.epoch))
		}
		got = append(got, point.Time.Sub(f.epoch))
	}
	return got
}

func cadenceSlots(interval, through time.Duration) []time.Duration {
	var want []time.Duration
	for at := interval; at <= through; at += interval {
		want = append(want, at)
	}
	return want
}

// assertNormalCadence pins the unrelated collector series to exactly one
// actual exported sample per elapsed ORIGINAL slot, and the provider to exactly
// one normal collection attempt per elapsed slot with no lifecycle exception.
func (f *cadenceRuntimeFixture) assertNormalCadence(t *testing.T, interval time.Duration) {
	t.Helper()
	elapsed := time.Since(f.epoch)
	want := cadenceSlots(interval, elapsed)
	if got := f.devicesTimes(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("actual exported devices timestamps=%v, want original slots %v", got, want)
	}
	stats := f.provider.CollectionStats()
	if stats.ScheduledAttempts != uint64(len(want)) || stats.TerminalAttempts != 0 || stats.TerminalSkippedFull != 0 {
		t.Fatalf("collection attempts=%+v, want exactly %d normal slots and no terminal exception", stats, len(want))
	}
}

// assertDeliveredCadence is assertNormalCadence for runs that may lose
// best-effort deliveries under an exporter fault: collection still happens at
// every original slot, and the exported samples are exactly the collections
// that were not explicitly discarded, at their original timestamps.
func (f *cadenceRuntimeFixture) assertDeliveredCadence(t *testing.T, interval time.Duration, allowDiscards bool) {
	t.Helper()
	want := cadenceSlots(interval, time.Since(f.epoch))
	f.obsMu.Lock()
	var collected, delivered []time.Duration
	discarded := 0
	for _, o := range f.observations {
		if o.failed {
			t.Fatalf("collection at %v failed", o.at)
		}
		collected = append(collected, o.at)
		if o.discarded {
			discarded++
			continue
		}
		delivered = append(delivered, o.at)
	}
	f.obsMu.Unlock()
	if !reflect.DeepEqual(collected, want) {
		t.Fatalf("actual SDK collections at %v, want every original slot %v", collected, want)
	}
	if discarded > 0 && !allowDiscards {
		t.Fatalf("%d best-effort collections discarded on a healthy exporter", discarded)
	}
	if stats := f.provider.CollectionStats(); stats.BestEffortDiscardedFull != uint64(discarded) || stats.ScheduledAttempts != uint64(len(want)) || stats.TerminalAttempts != 0 {
		t.Fatalf("collection accounting %+v, want %d discards over %d normal slots", stats, discarded, len(want))
	}
	if got := f.devicesTimes(t); !reflect.DeepEqual(got, delivered) {
		t.Fatalf("exported devices timestamps=%v, want exactly the retained original slots %v", got, delivered)
	}
}

// assertAccounting requires every admitted ORIGINAL to have completed on normal
// scheduled slots BEFORE cleanup: generation commits, released window quota,
// exact raw and rollup cumulative totals and every required log record.
func (f *cadenceRuntimeFixture) assertAccounting(t *testing.T, flows, hooks int) {
	t.Helper()
	health := f.app.ingressWAL.Health().WAL
	if health.PendingEntries != 0 || health.PendingBytes != 0 || health.CompletionMarkers != 0 {
		t.Fatalf("originals incomplete BEFORE cleanup: %+v", health)
	}
	if len(f.app.ingressWAL.orderedWindow()) != 0 {
		t.Fatal("retired original receipt/body quotas were not released")
	}
	signals := f.signals(t)
	mode := f.app.cfg.Cardinality.Flow.MetricsMode
	for _, family := range []struct {
		name    string
		want    float64
		enabled bool
	}{{flowlog.MetricIO, float64(flows * 30), mode != "rollup"}, {flowlog.MetricIORollup, float64(flows * 30), mode != "all"}, {flowlog.MetricPackets, float64(flows * 3), mode != "rollup"}, {flowlog.MetricPacketsRollup, float64(flows * 3), mode != "all"}, {webhook.MetricEvents, float64(hooks), true}} {
		want := family.want
		if !family.enabled {
			want = 0
		}
		if got := cadenceRuntimeLatestTotal(signals, family.name); got != want {
			t.Fatalf("normal cumulative %s=%v, want %v", family.name, got, want)
		}
	}
	actualFlows, actualHooks := 0, 0
	for _, record := range cadenceLogs(signals) {
		if record.EventName == "tailscale.network.flow" {
			actualFlows++
		}
		if record.EventName == "tailscale.webhook.nodeCreated" {
			actualHooks++
		}
	}
	if actualFlows != flows || actualHooks != hooks {
		t.Fatalf("required flow/webhook logs=%d/%d, want %d/%d BEFORE cleanup", actualFlows, actualHooks, flows, hooks)
	}
}

// waitCompletion advances ORIGINAL slots only, up to a fixed allowance, until
// every admitted original is retired. Timing out is a failure, never success.
func (f *cadenceRuntimeFixture) waitCompletion(t *testing.T, interval time.Duration, slots int) {
	t.Helper()
	for range slots {
		synctest.Wait()
		if h := f.app.ingressWAL.Health().WAL; h.PendingEntries == 0 && h.CompletionMarkers == 0 && len(f.app.ingressWAL.orderedWindow()) == 0 {
			return
		}
		time.Sleep(interval)
	}
	synctest.Wait()
	if h := f.app.ingressWAL.Health().WAL; h.PendingEntries != 0 || len(f.app.ingressWAL.orderedWindow()) != 0 {
		t.Fatalf("admitted originals incomplete after %d normal slots: %+v", slots, h)
	}
}

// sleepToNextSlotEdge positions the bubble just after the next original slot.
func (f *cadenceRuntimeFixture) sleepPastSlot(interval time.Duration) {
	elapsed := time.Since(f.epoch)
	next := (elapsed/interval + 1) * interval
	time.Sleep(next - elapsed + time.Second)
	synctest.Wait()
}

func isMetricPayload(b []byte) bool { return bytes.Contains(b, []byte(`"ScopeMetrics"`)) }

// ---------------------------------------------------------------------------
// The base-compatible cadence prefix. Its arrivals, timing and assertions are
// byte-for-byte the prefix executed at the failing-before base (see
// codex/loop17/build-evidence/base-cadence-test.go.fixture); only the
// construction adapter below differs, because provider wiring changed. Each
// named criterion test runs this prefix and then its own suffix.
// ---------------------------------------------------------------------------

type cadencePrefixFixture struct {
	provider *telemetry.Provider
	devices  *devices.Collector
	admit    func(*testing.T, int, bool)
	replay   func(context.Context) error
	close    func(context.Context) error
	rt       *cadenceRuntimeFixture // candidate adapter only: suffix access
}

type cadenceSample struct {
	At       time.Duration
	Start    time.Time
	Value    float64
	Resource json.RawMessage
	Scope    json.RawMessage
	Attrs    json.RawMessage
}

func cadencePrefixSamples(t *testing.T, raw []byte, epoch time.Time) []cadenceSample {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var samples []cadenceSample
	for {
		var obj struct {
			Resource     json.RawMessage
			ScopeMetrics []struct {
				Scope   json.RawMessage
				Metrics []struct {
					Name string
					Data struct {
						DataPoints []struct {
							Attributes json.RawMessage
							StartTime  time.Time
							Time       time.Time
							Value      float64
						}
					}
				}
			}
		}
		if err := dec.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("serialized stdout parse: %v", err)
		}
		for _, scope := range obj.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "tailscale.devices.count" {
					continue
				}
				for _, pt := range metric.Data.DataPoints {
					if pt.Time.IsZero() || len(pt.Attributes) == 0 || len(obj.Resource) == 0 || len(scope.Scope) == 0 {
						t.Fatalf("missing actual SDK point identity: %+v", pt)
					}
					samples = append(samples, cadenceSample{pt.Time.Sub(epoch), pt.StartTime, pt.Value, obj.Resource, scope.Scope, pt.Attributes})
				}
			}
		}
	}
	return samples
}

// cadenceStep names one suffix stage in the log. Subtests cannot be started
// inside a synctest bubble, so stages run sequentially on the same t.
func cadenceStep(t *testing.T, name string, fn func()) {
	t.Helper()
	t.Logf("stage %s", name)
	fn()
}

type cadenceSuffix func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, temporality string)

func runCadencePrefix(t *testing.T, suffix cadenceSuffix) {
	for _, interval := range []time.Duration{60 * time.Second, 15 * time.Second} {
		for _, temporality := range []string{"", "cumulative"} {
			name := temporality
			if name == "" {
				name = "default_cumulative"
			}
			t.Run(fmt.Sprintf("%s/%s", interval, name), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx := context.Background()
					var writer cadencePrefixWriter
					epoch := time.Now()
					f := newCadencePrefixFixture(t, ctx, &writer, interval, temporality)
					defer func() {
						c, cancel := context.WithTimeout(ctx, 10*time.Second)
						defer cancel()
						if err := f.close(c); err != nil {
							t.Errorf("cleanup: %v", err)
						}
					}()
					if err := f.devices.Collect(ctx, f.provider.Emitter()); err != nil {
						t.Fatal(err)
					}
					synctest.Wait() // reader registered and clock durably waiting
					// Distinct original HEC bodies at the same 7s instant are not coalesced.
					arrivals := []int{2, 7, 7, 13, 31, 47}
					var now int
					for i, at := range arrivals {
						time.Sleep(time.Duration(at-now) * time.Second)
						now = at
						if err := f.devices.Collect(ctx, f.provider.Emitter()); err != nil {
							t.Fatal(err)
						}
						f.admit(t, i, false)
						if err := f.replay(ctx); err != nil {
							t.Fatalf("replay: %v", err)
						}
						synctest.Wait()
						// Independent real log construction shares the stdout serialization.
						f.provider.Emitter().LogEvent(telemetry.Event{Name: "synthetic.cadence.log", Body: fmt.Sprintf("log-%d", i)})
						if at == 13 {
							f.admit(t, i, true)
							if err := f.replay(ctx); err != nil {
								t.Fatalf("webhook replay: %v", err)
							}
						}
					}
					time.Sleep(time.Duration(120-now) * time.Second)
					synctest.Wait()
					// Capture BEFORE cleanup; terminal exceptions are not cadence evidence.
					raw := writer.Bytes()
					samples := cadencePrefixSamples(t, raw, epoch)
					var got, want []time.Duration
					for _, sample := range samples {
						got = append(got, sample.At)
						if sample.Value != 3 {
							t.Errorf("actual devices value = %g at %s, want 3", sample.Value, sample.At)
						}
						if !bytes.Equal(sample.Attrs, samples[0].Attrs) || !bytes.Equal(sample.Resource, samples[0].Resource) || !bytes.Equal(sample.Scope, samples[0].Scope) {
							t.Error("stable devices series identity changed")
						}
					}
					for at := interval; at <= 120*time.Second; at += interval {
						want = append(want, at)
					}
					if dir := os.Getenv("TSO0148_PROOF_DIR"); dir != "" {
						path := filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+".json")
						if err := os.WriteFile(path, raw, 0600); err != nil {
							t.Fatal(err)
						}
					}
					t.Logf("actual devices samples (count=%d, all values=3): %v; expected original slots: %v", len(got), got, want)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("exported devices timestamp mismatch: got %v, want %v (count samples, not requests)", got, want)
					}
					if t.Failed() {
						return
					}
					suffix(t, f.rt, interval, temporality)
				})
			})
		}
	}
}

// cadencePrefixWriter is the prefix's view of the serialized sink: the
// candidate adapter points it at the runtime fixture's actual stdout sink.
type cadencePrefixWriter struct {
	mu   sync.Mutex
	sink *testIngressSink
}

func (w *cadencePrefixWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sink.Bytes()
}

// Candidate construction adapter. The WAL worker runs continuously (the
// production arrangement) so replay is its wake signal, not a call that would
// itself wait for delivery.
func newCadencePrefixFixture(t *testing.T, _ context.Context, w *cadencePrefixWriter, interval time.Duration, temporality string) cadencePrefixFixture {
	t.Helper()
	f := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality})
	w.mu.Lock()
	w.sink = f.sink
	w.mu.Unlock()
	return cadencePrefixFixture{
		provider: f.provider,
		devices:  f.devices,
		admit:    f.admit,
		replay:   func(context.Context) error { f.app.ingressWAL.signalWake(); return nil },
		close:    func(context.Context) error { return f.close() },
		rt:       f,
	}
}

// The prefix admits six stream originals (one flow each) and one webhook.
const (
	cadencePrefixFlows = 6
	cadencePrefixHooks = 1
)

// ---------------------------------------------------------------------------
// Criterion 1.
// ---------------------------------------------------------------------------

func TestIngressWALScheduledCadence_IrregularBurstIdle(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, _ string) {
		// Burst: ten distinct originals at one instant, with independent logs.
		time.Sleep(time.Second)
		for i := range 10 {
			f.admit(t, 100+i, false)
			f.provider.Emitter().LogEvent(telemetry.Event{Name: "synthetic.cadence.log", Body: fmt.Sprintf("burst-%d", i)})
		}
		synctest.Wait()
		// Idle well past several slots, then irregular arrivals.
		time.Sleep(79 * time.Second) // t=200s
		at := 200 * time.Second
		for i, next := range []time.Duration{203 * time.Second, 217 * time.Second, 233 * time.Second} {
			time.Sleep(next - at)
			at = next
			f.refreshDevices(t)
			f.admit(t, 200+i, false)
			synctest.Wait()
		}
		// All originals must finish on normal slots within four intervals.
		time.Sleep(4 * interval)
		synctest.Wait()
		f.assertNormalCadence(t, interval)
		f.assertAccounting(t, cadencePrefixFlows+10+3, cadencePrefixHooks)
		if stats := f.provider.CollectionStats(); stats.BestEffortDiscardedFull != 0 || stats.CollectFailures != 0 {
			t.Fatalf("healthy run lost or failed collections: %+v", stats)
		}
	})
}

// ---------------------------------------------------------------------------
// Criterion 2.
// ---------------------------------------------------------------------------

// syncFlowAttrKeys runs one body through the WAL-disabled synchronous path and
// returns the attribute-key sets of each flow family it emits: the reference
// for "durable delivery changes no family or dimension".
func syncFlowAttrKeys(t *testing.T, mode, body string) map[string]map[string]bool {
	t.Helper()
	sink := &testIngressSink{}
	p, err := telemetry.NewProvider(context.Background(), telemetry.Options{Protocol: "stdout", StdoutWriter: sink, ServiceName: "synthetic-sync", MetricInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Token = "synthetic-token"
	cfg.Cardinality.Flow.MetricsMode = mode
	cfg.Collectors.Flowlogs.LogMode = "per_connection"
	a := newAppShell(cfg, "sync", slog.New(slog.NewTextHandler(io.Discard, nil)), p.Emitter(), p.Tracer(), p.Shutdown, collector.NewMemoryStore())
	a.buildProcessDeps()
	client, err := tsapi.NewClient(tsapi.Options{Tailnet: "example.com", BaseURL: "http://127.0.0.1:0", APIKey: "synthetic-local"})
	if err != nil {
		t.Fatal(err)
	}
	rt := a.addRuntime("example.com", p.Emitter(), nil, nil, provider.Tailscale(client), false)
	a.buildReceivers()
	req := httptest.NewRequest(http.MethodPost, cfg.Streaming.Path, strings.NewReader(body))
	req.SetBasicAuth("", "synthetic-token")
	response := httptest.NewRecorder()
	a.streamSrv.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("synchronous reference admission: HTTP %d", response.Code)
	}
	rt.flowProc.FlushRollup(rt.emitter)
	// The synchronous reference uses the documented shutdown exception once.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatalf("synchronous reference close: %v", err)
	}
	keys := map[string]map[string]bool{}
	for _, name := range []string{flowlog.MetricIO, flowlog.MetricPackets, flowlog.MetricIORollup, flowlog.MetricPacketsRollup, flowlog.MetricUniqueDstPorts, flowlog.MetricUniqueDstPeers} {
		for _, point := range cadenceRuntimePoints(cadenceRuntimeSignals(t, sink.Bytes()), name) {
			if keys[name] == nil {
				keys[name] = map[string]bool{}
			}
			keys[name][cadenceAttrKeys(point.Attributes)] = true
		}
	}
	return keys
}

func cadencePortBody(node string, port int, tx int) string {
	return fmt.Sprintf(`{"event":{"nodeId":%q,"start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::99]:%d","txBytes":%d,"rxBytes":0,"txPkts":1,"rxPkts":0}]}}`, node, port, tx)
}

func cadenceMultiPortBody(node string, ports ...int) string {
	var traffic []string
	for _, port := range ports {
		traffic = append(traffic, fmt.Sprintf(`{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::99]:%d","txBytes":1,"rxBytes":0,"txPkts":1,"rxPkts":0}`, port))
	}
	return fmt.Sprintf(`{"event":{"nodeId":%q,"start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[%s]}}`, node, strings.Join(traffic, ","))
}

// uniquePortValues returns the actual exported unique.dst_ports value for the
// prefix's source node, keyed by slot offset.
func (f *cadenceRuntimeFixture) uniquePortValues(t *testing.T) map[time.Duration]float64 {
	t.Helper()
	values := map[time.Duration]float64{}
	for _, point := range cadenceRuntimePoints(f.signals(t), flowlog.MetricUniqueDstPorts) {
		if cadenceAttrs(point.Attributes)["tailscale.src.node"] == "unknown" {
			values[point.Time.Sub(f.epoch)] = point.Value
		}
	}
	return values
}

func TestIngressWALScheduledCadence_CumulativeDetail(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, _ string) {
		f.waitCompletion(t, interval, 6)
		f.assertAccounting(t, cadencePrefixFlows, cadencePrefixHooks)
		signals := f.signals(t)
		// Cumulative temporality, monotonic Sums, one StartTime per series that
		// precedes every sample: the default and validated delivery mode.
		for _, name := range []string{flowlog.MetricIO, flowlog.MetricPackets, flowlog.MetricIORollup, flowlog.MetricPacketsRollup, webhook.MetricEvents} {
			metrics := cadenceRuntimeMetrics(signals, name)
			if len(metrics) == 0 {
				t.Fatalf("%s was never exported", name)
			}
			starts := map[string]time.Time{}
			for _, metric := range metrics {
				if metric.Data.Temporality != "CumulativeTemporality" || !metric.Data.IsMonotonic {
					t.Fatalf("%s temporality=%q monotonic=%v, want cumulative monotonic Sum", name, metric.Data.Temporality, metric.Data.IsMonotonic)
				}
				for _, point := range metric.Data.DataPoints {
					key := string(point.Attributes)
					if start, ok := starts[key]; ok && !start.Equal(point.StartTime) {
						t.Fatalf("%s series StartTime changed %v -> %v", name, start, point.StartTime)
					}
					starts[key] = point.StartTime
					if point.StartTime.After(point.Time) {
						t.Fatalf("%s StartTime after sample time", name)
					}
				}
			}
		}
		// Families and dimensions are exactly those of the synchronous path.
		reference := syncFlowAttrKeys(t, "both", cadenceFlowBody(0))
		for name, want := range reference {
			got := map[string]bool{}
			for _, point := range cadenceRuntimePoints(signals, name) {
				got[cadenceAttrKeys(point.Attributes)] = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s attribute sets durable=%v, synchronous=%v", name, got, want)
			}
		}

		// Synchronous unique gauge: a later original changes the SAME series
		// from 1 to 2, the next to 1, and idle slots retain the last value.
		before := f.uniquePortValues(t)
		if len(before) == 0 {
			t.Fatal("prefix exported no unique-port gauge")
		}
		for at, value := range before {
			if value != 1 {
				t.Fatalf("prefix unique ports=%v at %v, want 1", value, at)
			}
		}
		f.admitBody(t, false, cadenceMultiPortBody("synthetic-ports-a", 443, 22))
		f.sleepPastSlot(interval)
		slotA := time.Since(f.epoch).Truncate(interval)
		f.admitBody(t, false, cadenceMultiPortBody("synthetic-ports-b", 53))
		f.sleepPastSlot(interval)
		slotB := time.Since(f.epoch).Truncate(interval)
		time.Sleep(2 * interval) // idle
		synctest.Wait()
		values := f.uniquePortValues(t)
		if values[slotA] != 2 || values[slotB] != 1 {
			t.Fatalf("unique ports at %v=%v and %v=%v, want 2 then 1", slotA, values[slotA], slotB, values[slotB])
		}
		for at := slotB + interval; at <= time.Since(f.epoch); at += interval {
			if got, ok := values[at]; !ok || got != 1 {
				t.Fatalf("idle slot %v unique ports=%v (present=%v), want retained 1", at, got, ok)
			}
		}
		// Two originals materialized at one slot: the LATER original's value
		// wins. Both are admitted before the worker's next window pass so they
		// share one producer group.
		if err := f.stopWorker(); err != nil {
			t.Fatal(err)
		}
		f.admitBody(t, false, cadenceMultiPortBody("synthetic-ports-c", 1, 2, 3))
		f.admitBody(t, false, cadenceMultiPortBody("synthetic-ports-d", 4, 5))
		f.startWorker()
		f.sleepPastSlot(interval)
		slotCD := time.Since(f.epoch).Truncate(interval)
		if got := f.uniquePortValues(t)[slotCD]; got != 2 {
			t.Fatalf("same-slot unique ports=%v, want the later original's 2", got)
		}
		f.assertNormalCadence(t, interval)

		// Per-original top-N attribution (top_n=1): A has https 100B + ssh 10B,
		// B has ssh 100B. Per original, A folds its 10B ssh into __other__ and
		// B keeps ssh. A coalesced A+B body would instead keep ssh (110B) and
		// fold A's https 100B into __other__.
		topN := newCadenceFixture(t, cadenceOptions{interval: interval, mode: "rollup", config: func(c *config.Config) {
			c.Cardinality.Flow.RollupTopN = 1
		}})
		topN.admitBody(t, false, `{"event":{"nodeId":"synthetic-topn-a","start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::7]:443","txBytes":100,"rxBytes":0,"txPkts":1,"rxPkts":0},{"proto":6,"src":"100.64.0.1:1234","dst":"[2001:db8::7]:22","txBytes":10,"rxBytes":0,"txPkts":1,"rxPkts":0}]}}`)
		topN.admitBody(t, false, cadencePortBody("synthetic-topn-b", 22, 100))
		topN.waitCompletion(t, interval, 6)
		topSignals := topN.signals(t)
		other := cadenceLatestTotalWhere(topSignals, flowlog.MetricIORollup, func(a map[string]string) bool { return a["tailscale.dst.node"] == "__other__" })
		total := cadenceRuntimeLatestTotal(topSignals, flowlog.MetricIORollup)
		if other != 10 || total != 210 {
			t.Fatalf("rollup __other__=%v total=%v, want per-original 10 of 210", other, total)
		}

		// Every flow mode keeps exact raw and rollup totals.
		for _, mode := range []string{"all", "rollup", "both"} {
			m := newCadenceFixture(t, cadenceOptions{interval: interval, mode: mode})
			m.admit(t, 1, false)
			m.admit(t, 2, false)
			m.waitCompletion(t, interval, 6)
			m.assertAccounting(t, 2, 0)
		}
	})
}

// ---------------------------------------------------------------------------
// Criterion 3.
// ---------------------------------------------------------------------------

func (f *cadenceRuntimeFixture) failWrites(match func([]byte) bool) {
	f.sink.mu.Lock()
	f.sink.failIf = match
	f.sink.mu.Unlock()
}

func (f *cadenceRuntimeFixture) pending() int { return f.app.ingressWAL.Health().WAL.PendingEntries }

func TestIngressWALScheduledCadence_CommitRequiresMetricsAndLogs(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, temporality string) {
		f.waitCompletion(t, interval, 6)
		f.assertAccounting(t, cadencePrefixFlows, cadencePrefixHooks)
		flows, hooks := cadencePrefixFlows, cadencePrefixHooks

		// Order 1: the metric first cover is acknowledged while the required log
		// is still failing. No commit until the log is ACKed, then the commit
		// happens without any further collection.
		f.failWrites(func(b []byte) bool { return !isMetricPayload(b) })
		f.admit(t, 300, false)
		f.sleepPastSlot(interval)
		acked := f.provider.CollectionStats().RequiredSnapshotsAcknowledged
		if f.pending() != 1 {
			t.Fatalf("pending=%d with metrics ACKed but logs failing, want 1", f.pending())
		}
		f.failWrites(nil)
		time.Sleep(6 * time.Second) // log retry backoff, well inside the slot
		synctest.Wait()
		if f.pending() != 0 {
			t.Fatalf("pending=%d after the last log ACK, want 0", f.pending())
		}
		if got := f.provider.CollectionStats().RequiredSnapshotsAcknowledged; got != acked {
			t.Fatalf("log completion triggered a collection: required ACKs %d -> %d", acked, got)
		}
		flows++

		// Order 2: the required log is delivered while every metric part fails.
		f.failWrites(isMetricPayload)
		f.admit(t, 301, true)
		f.sleepPastSlot(interval)
		f.sleepPastSlot(interval)
		if f.pending() != 1 {
			t.Fatalf("pending=%d with logs ACKed but metrics failing, want 1", f.pending())
		}
		f.failWrites(nil)
		f.waitCompletion(t, interval, 6)
		hooks++

		// A canceled waiter keeps the receipt: stop the worker while the cover
		// is retrying, restart it, and the original is never reapplied.
		f.failWrites(isMetricPayload)
		f.admit(t, 302, false)
		f.sleepPastSlot(interval)
		if err := f.stopWorker(); err != nil {
			t.Fatalf("worker cancellation: %v", err)
		}
		if f.pending() != 1 || len(f.app.ingressWAL.orderedWindow()) != 1 || f.app.ingressWAL.orderedWindow()[0].receipt == nil {
			t.Fatal("canceled waiter discarded its receipt")
		}
		f.failWrites(nil)
		f.startWorker()
		f.app.ingressWAL.signalWake()
		f.waitCompletion(t, interval, 6)
		flows++
		f.assertNormalCadence(t, interval)
		f.assertAccounting(t, flows, hooks)

		// Two tailnet providers: only the matching provider's delivery can ACK.
		multi := newCadenceMultiTailnet(t, interval, temporality)
		multi.acme.failWrites(isMetricPayload)
		multi.admit(t, "acme", 1)
		multi.admit(t, "beta", 2)
		multi.beta.sleepPastSlot(interval)
		multi.beta.sleepPastSlot(interval)
		if got := multi.app.ingressWAL.Health().WAL.PendingEntries; got != 1 {
			t.Fatalf("pending=%d, want only acme's original pending behind its failing provider", got)
		}
		if multi.acme.provider.CollectionStats().RequiredSnapshotsAcknowledged != 0 || multi.beta.provider.CollectionStats().RequiredSnapshotsAcknowledged != 1 {
			t.Fatal("a provider acknowledged another provider's original")
		}
		multi.acme.failWrites(nil)
		multi.beta.waitCompletion(t, interval, 6)
		for _, rt := range []*cadenceRuntimeFixture{multi.acme, multi.beta} {
			if got := cadenceRuntimeLatestTotal(rt.signals(t), flowlog.MetricIO); got != 30 {
				t.Fatalf("per-provider raw io total=%v, want exactly its own original's 30", got)
			}
		}

		// Crash replay: the first process applies and covers but never ACKs;
		// a fresh process on the same WAL directory replays under a fresh epoch.
		dir := t.TempDir()
		first := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, dir: dir})
		first.failWrites(func([]byte) bool { return true })
		first.admit(t, 400, false)
		first.sleepPastSlot(interval)
		if err := first.stopWorker(); err != nil {
			t.Fatal(err)
		}
		// Process death: the WAL store closes without any completion; nothing of
		// the first process's provider state survives.
		if err := first.app.ingressWAL.wal.Close(); err != nil {
			t.Fatal(err)
		}
		first.abandon()
		second := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, dir: dir})
		second.waitCompletion(t, interval, 6)
		second.assertAccounting(t, 1, 0)
		firstStarts := cadenceRuntimePoints(second.signals(t), flowlog.MetricIO)
		if len(firstStarts) == 0 || !firstStarts[0].StartTime.After(first.epoch) {
			t.Fatal("restart did not export the replayed original under a fresh epoch")
		}

		// Signals disabled: an explicitly excluded signal is not an obligation,
		// while the enabled one still gates the commit.
		for _, disabled := range []string{"metrics", "logs"} {
			off := false
			x := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, telemetry: func(o *telemetry.Options) {
				if disabled == "metrics" {
					o.Signals.Metrics = &telemetry.SignalOverride{Enabled: &off}
				} else {
					o.Signals.Logs = &telemetry.SignalOverride{Enabled: &off}
				}
			}})
			if disabled == "metrics" {
				// Only the required log gates retirement: blocked logs pin it.
				x.failWrites(func([]byte) bool { return true })
				x.admit(t, 500, false)
				x.sleepPastSlot(interval)
				if x.pending() != 1 {
					t.Fatalf("metrics disabled: pending=%d while the required log fails, want 1", x.pending())
				}
				x.failWrites(nil)
			} else {
				x.failWrites(isMetricPayload)
				x.admit(t, 500, false)
				x.sleepPastSlot(interval)
				if x.pending() != 1 {
					t.Fatalf("logs disabled: pending=%d while the metric cover fails, want 1", x.pending())
				}
				x.failWrites(nil)
			}
			x.waitCompletion(t, interval, 6)
		}

		// Poison: an intentionally incorrect (too small) required-log bound is
		// detected after entry. The receipt never ACKs, the original stays on
		// disk, the coordinator fails closed and refuses further appends.
		p := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality})
		key := ingressWALRouteKey{tailnet: "example.com", source: ingressWALSourceStream, signal: ingressWALSignalHEC}
		route := p.app.ingressWAL.routes[key]
		prepare := route.prepare
		route.prepare = func(ctx context.Context, body []byte, accepted time.Time) (telemetry.IngressWork, error) {
			work, err := prepare(ctx, body, accepted)
			work.Bounds.Logs.Records = 0
			return work, err
		}
		p.app.ingressWAL.routes[key] = route
		p.admit(t, 600, false)
		time.Sleep(3 * interval)
		synctest.Wait()
		if p.pending() != 1 || p.app.ingressWAL.Health().State != ingressWALStateFailed {
			t.Fatalf("poisoned original pending=%d state=%s, want 1/failed", p.pending(), p.app.ingressWAL.Health().State)
		}
		if got := cadenceRuntimeLatestTotal(p.signals(t), flowlog.MetricIO); got != 0 {
			t.Fatalf("poisoned original exported %v io, want no partial program", got)
		}
		if code := p.post(false, cadenceFlowBody(601)); code == http.StatusOK {
			t.Fatal("fail-closed coordinator acknowledged a new append")
		}
		p.abandon()
	})
}

type cadenceMultiTailnet struct {
	app        *App
	acme, beta *cadenceRuntimeFixture
}

func newCadenceMultiTailnet(t *testing.T, interval time.Duration, temporality string) *cadenceMultiTailnet {
	t.Helper()
	newProvider := func() (*telemetry.Provider, *testIngressSink) {
		sink := &testIngressSink{}
		p, err := telemetry.NewProvider(context.Background(), telemetry.Options{Protocol: "stdout", StdoutWriter: sink, ServiceName: "synthetic-multi", MetricInterval: interval, MetricTemporality: temporality})
		if err != nil {
			t.Fatal(err)
		}
		return p, sink
	}
	epoch := time.Now()
	acmeP, acmeSink := newProvider()
	betaP, betaSink := newProvider()
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "both"
	cfg.Streaming.Enabled = true
	cfg.Streaming.Routes = []config.StreamingRoute{
		{Tailnet: "acme.example.com", Path: "/hec/acme", Token: "token-a"},
		{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "token-b"},
	}
	cfg.IngressWAL.Enabled = true
	cfg.IngressWAL.Directory = t.TempDir()
	process, _ := newProvider()
	a := newAppShell(cfg, "multi", slog.New(slog.NewTextHandler(io.Discard, nil)), process.Emitter(), process.Tracer(), process.Shutdown, collector.NewMemoryStore())
	a.buildProcessDeps()
	for _, rt := range []struct {
		name string
		p    *telemetry.Provider
	}{{"acme.example.com", acmeP}, {"beta.example.com", betaP}} {
		client, err := tsapi.NewClient(tsapi.Options{Tailnet: rt.name, BaseURL: "http://127.0.0.1:0", APIKey: "synthetic-local"})
		if err != nil {
			t.Fatal(err)
		}
		a.addRuntimeConfigured(rt.name, rt.name, rt.p.Emitter(), nil, nil, rt.p, provider.Tailscale(client), true)
	}
	if err := a.buildIngressWAL(a.buildReceivers()); err != nil {
		t.Fatal(err)
	}
	m := &cadenceMultiTailnet{app: a}
	m.acme = &cadenceRuntimeFixture{app: a, provider: acmeP, sink: acmeSink, epoch: epoch}
	m.beta = &cadenceRuntimeFixture{app: a, provider: betaP, sink: betaSink, epoch: epoch}
	m.beta.startWorker()
	synctest.Wait() // worker parked before the first admission, as in newCadenceFixture
	t.Cleanup(func() {
		for _, s := range []*testIngressSink{acmeSink, betaSink} {
			s.mu.Lock()
			s.fail, s.failIf = false, nil
			s.mu.Unlock()
		}
		if err := m.beta.stopWorker(); err != nil {
			t.Errorf("worker: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.Close(ctx)
		_ = acmeP.Shutdown(ctx)
		_ = betaP.Shutdown(ctx)
	})
	return m
}

func (m *cadenceMultiTailnet) admit(t *testing.T, tailnet string, i int) {
	t.Helper()
	token := map[string]string{"acme": "token-a", "beta": "token-b"}[tailnet]
	req := httptest.NewRequest(http.MethodPost, "/hec/"+tailnet, strings.NewReader(cadenceFlowBody(i)))
	req.SetBasicAuth("", token)
	response := httptest.NewRecorder()
	m.app.streamSrv.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("%s admission: HTTP %d", tailnet, response.Code)
	}
	synctest.Wait()
}

// ---------------------------------------------------------------------------
// Criterion 4.
// ---------------------------------------------------------------------------

// failingProducer is an additive SDK Producer: it lets a REAL SDK Collect fail
// once on demand after normal production, without replacing the reader.
type failingProducer struct {
	mu   sync.Mutex
	fail bool
}

func (p *failingProducer) Produce(context.Context) ([]metricdata.ScopeMetrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		p.fail = false
		return nil, errors.New("synthetic one-shot collect failure")
	}
	return nil, nil
}

func (p *failingProducer) failNext() { p.mu.Lock(); p.fail = true; p.mu.Unlock() }

// metricAttempts returns every metric payload the sink was handed, accepted or
// not, in order.
func (s *testIngressSink) metricAttempts() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for _, attempt := range s.attempts {
		if isMetricPayload(attempt) {
			out = append(out, attempt)
		}
	}
	return out
}

func TestIngressWALScheduledCadence_SplitRetryKeepsSnapshot(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, temporality string) {
		f.waitCompletion(t, interval, 6)
		f.assertAccounting(t, cadencePrefixFlows, cadencePrefixHooks)

		// "second" fails an interior part of a split collection; "devices" fails the
		// part carrying the stable unrelated series.
		for _, failAt := range []string{"second", "devices"} {
			cadenceStep(t, "part_failure_"+failAt, func() {
				s := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, batch: 2})
				s.refreshDevices(t)
				s.admitBody(t, false, cadenceMultiPortBody("synthetic-split", 443))
				// Fail exactly one part of the first collection, once.
				var calls int
				var failedPart []byte
				s.failWrites(func(b []byte) bool {
					if !isMetricPayload(b) {
						return false
					}
					calls++
					if failedPart == nil && ((failAt == "second" && calls == 2) || (failAt == "devices" && bytes.Contains(b, []byte(`"tailscale.devices.count"`)))) {
						failedPart = bytes.Clone(b)
						return true
					}
					return false
				})
				s.sleepPastSlot(interval)
				if failedPart == nil {
					t.Fatal("no metric part failed")
				}
				// A later same-attribute value while the original is still pinned.
				s.admitBody(t, false, cadenceMultiPortBody("synthetic-split-later", 1, 2))
				s.waitCompletion(t, interval, 6)
				// The retried part is byte-identical to the failed attempt: the same
				// Time/StartTime/values/buckets/attributes, never re-collected.
				retries := 0
				for _, attempt := range s.sink.metricAttempts() {
					if bytes.Equal(attempt, failedPart) {
						retries++
					}
				}
				if retries < 2 {
					t.Fatalf("failed part attempts=%d, want the unchanged part retried", retries)
				}
				// Successful parts are never resent: every accepted payload is distinct.
				seen := map[string]bool{}
				for _, signal := range bytes.Split(s.sink.Bytes(), []byte("\n")) {
					if len(signal) == 0 || !isMetricPayload(signal) {
						continue
					}
					if seen[string(signal)] {
						t.Fatal("an acknowledged part was delivered twice")
					}
					seen[string(signal)] = true
				}
				values := s.uniquePortValues(t)
				first := interval
				if values[first] != 1 {
					t.Fatalf("pinned original unique ports=%v at %v, want unchanged 1", values[first], first)
				}
				later := false
				for at, v := range values {
					if at > first && v == 2 {
						later = true
					}
				}
				if !later {
					t.Fatalf("later same-attribute value 2 never exported: %v", values)
				}
				s.assertNormalCadence(t, interval)
			})
		}

		cadenceStep(t, "failed_collect_before_first_WAL", func() {
			producer := &failingProducer{}
			s := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, mode: "rollup", producers: []sdkmetric.Producer{producer}})
			rt := s.app.runtimes[0]
			// Ordinary polled stage, then a REAL SDK Collect failure at slot I.
			flow := rollupFlowRecord()
			rt.flowProc.ProcessCtx(context.Background(), flow, rt.emitter)
			producer.failNext()
			time.Sleep(interval)
			synctest.Wait()
			if got := s.provider.CollectionStats().CollectFailures; got != 1 {
				t.Fatalf("collect failures=%d, want 1", got)
			}
			// R builds after the failure, before slot 2I.
			time.Sleep(time.Second)
			s.admitBody(t, false, cadencePortBody("synthetic-r", 443, 7))
			synctest.Wait()
			time.Sleep(interval - time.Second) // slot 2I: old stage collects
			synctest.Wait()
			if s.pending() != 1 {
				t.Fatal("the old stage's successful collect acknowledged R")
			}
			rTotal := func() float64 {
				return cadenceLatestTotalWhere(s.signals(t), flowlog.MetricIORollup, func(a map[string]string) bool {
					return a["tailscale.dst.service"] == "https" && a["network.io.direction"] == "transmit" && a["tailscale.dst.node"] == "external"
				})
			}
			if got := rTotal(); got != 0 {
				t.Fatalf("R's rollup appeared at 2I (%v); it must first be covered at 3I", got)
			}
			time.Sleep(interval) // slot 3I: R's first cover
			synctest.Wait()
			s.waitCompletion(t, interval, 2)
			if got := rTotal(); got != 7 {
				t.Fatalf("R's rollup total=%v after its 3I first cover, want exactly 7", got)
			}
			// The old polled stage was collected once at 2I with its counters added
			// exactly once, never re-added on the retry.
			if got := cadenceLatestTotalWhere(s.signals(t), flowlog.MetricIORollup, func(a map[string]string) bool {
				return a["tailscale.dst.node"] == "unknown" && a["network.io.direction"] == "transmit"
			}); got != float64(flow.VirtualTraffic[0].TxBytes) {
				t.Fatalf("old polled stage transmit total=%v, want %d added exactly once", got, flow.VirtualTraffic[0].TxBytes)
			}
			if got := s.provider.CollectionStats().ScheduledAttempts; got < 3 {
				t.Fatalf("scheduled attempts=%d, want every original slot attempted", got)
			}
		})

		cadenceStep(t, "saturation", func() {
			runCadenceSaturation(t, interval, temporality)
		})
	})
}

// runCadenceSaturation is the mandatory saturation timeline (criteria 4/6).
func runCadenceSaturation(t *testing.T, interval time.Duration, temporality string) {
	t.Helper()
	type observed struct {
		at        time.Duration
		discarded bool
		required  bool
		devices   float64
		failed    bool
	}
	var mu sync.Mutex
	var observations []observed
	s := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, config: func(c *config.Config) {
		c.IngressWAL.MaxEntries = 6
	}})
	if err := s.provider.SetCollectionObserver(func(o telemetry.CollectionObservation) {
		value := -1.0
		if o.Data != nil {
			for _, scope := range o.Data.ScopeMetrics {
				for _, m := range scope.Metrics {
					if m.Name == "tailscale.devices.count" {
						if g, ok := m.Data.(metricdata.Gauge[int64]); ok && len(g.DataPoints) > 0 {
							value = float64(g.DataPoints[0].Value)
						}
						if g, ok := m.Data.(metricdata.Gauge[float64]); ok && len(g.DataPoints) > 0 {
							value = g.DataPoints[0].Value
						}
					}
				}
			}
		}
		mu.Lock()
		observations = append(observations, observed{at: time.Since(s.epoch), discarded: o.Discarded, required: o.Required, devices: value, failed: o.Err != nil})
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	s.refreshDevices(t)
	// Every metric export fails; required logs stall too.
	s.failWrites(func([]byte) bool { return true })
	admitted := 0
	// Two required first covers pin both credits.
	s.admit(t, 700, false)
	admitted++
	s.sleepPastSlot(interval)
	s.admit(t, 701, false)
	admitted++
	s.sleepPastSlot(interval)
	if stats := s.provider.CollectionStats(); stats.RetainedCredits != 2 {
		t.Fatalf("retained credits=%d, want both pinned by required covers: %+v", stats.RetainedCredits, stats)
	}
	// Five more original slots under saturation, appending until the unchanged
	// WAL capacity refuses.
	refused := false
	for slot := range 5 {
		for !refused {
			if code := s.post(false, cadenceFlowBody(710+admitted+slot*10)); code != http.StatusOK {
				refused = true
				break
			}
			admitted++
		}
		s.sleepPastSlot(interval)
		stats := s.provider.CollectionStats()
		if stats.RetainedCredits+stats.ReservedCredits > 2 {
			t.Fatalf("retention exceeded two credits: %+v", stats)
		}
		if s.pending() != admitted {
			t.Fatalf("premature commit under saturation: pending=%d admitted=%d", s.pending(), admitted)
		}
	}
	if !refused {
		t.Fatal("the unchanged WAL capacity never refused under saturation")
	}
	stats := s.provider.CollectionStats()
	elapsed := uint64(time.Since(s.epoch) / interval)
	if stats.ScheduledAttempts != elapsed {
		t.Fatalf("scheduled attempts=%d, want every elapsed original slot %d (no paused Collect)", stats.ScheduledAttempts, elapsed)
	}
	mu.Lock()
	discarded := 0
	for i, o := range observations {
		if want := time.Duration(i+1) * interval; o.at != want {
			t.Fatalf("collection %d at %v, want original slot %v", i, o.at, want)
		}
		if o.failed || o.devices != 3 {
			t.Fatalf("collection %d failed=%v devices=%v, want a real SDK collection with value 3", i, o.failed, o.devices)
		}
		if o.discarded {
			discarded++
			if o.required {
				t.Fatal("a discarded collection carried required members")
			}
		}
	}
	mu.Unlock()
	if discarded == 0 || uint64(discarded) != stats.BestEffortDiscardedFull {
		t.Fatalf("discarded best-effort collections=%d, accounting=%d; want >0 and equal", discarded, stats.BestEffortDiscardedFull)
	}
	// Recover BETWEEN original slots: retries keep the pinned snapshots, and
	// recovery manufactures no immediate collection.
	time.Sleep(interval / 2)
	before := s.provider.CollectionStats().ScheduledAttempts
	s.failWrites(nil)
	time.Sleep(interval/2 - 2*time.Second) // still before the next original slot
	synctest.Wait()
	if got := s.provider.CollectionStats().ScheduledAttempts; got != before {
		t.Fatalf("recovery collected out of schedule: %d -> %d", before, got)
	}
	s.waitCompletion(t, interval, 12)
	// Collection cadence never changed: one attempt per elapsed original slot,
	// each a real SDK collection. Delivery is exactly the non-discarded subset,
	// at original timestamps; discarded slots are absent, never back-filled.
	elapsed = uint64(time.Since(s.epoch) / interval)
	stats = s.provider.CollectionStats()
	if stats.ScheduledAttempts != elapsed || stats.TerminalAttempts != 0 {
		t.Fatalf("attempts=%+v, want %d normal slots", stats, elapsed)
	}
	var delivered []time.Duration
	mu.Lock()
	for _, o := range observations {
		if !o.discarded {
			delivered = append(delivered, o.at)
		}
	}
	mu.Unlock()
	if got := s.devicesTimes(t); !reflect.DeepEqual(got, delivered) {
		t.Fatalf("exported devices timestamps=%v, want exactly the retained original slots %v", got, delivered)
	}
	s.assertAccounting(t, admitted, 0)
}

// ---------------------------------------------------------------------------
// Criterion 5.
// ---------------------------------------------------------------------------

func TestIngressWALScheduledCadence_StartupRestartLeadershipShutdown(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, temporality string) {
		f.waitCompletion(t, interval, 6)
		f.assertAccounting(t, cadencePrefixFlows, cadencePrefixHooks)

		// Startup replay of a preseeded backlog: applied before the first slot,
		// but no early sample and no epoch reset.
		dir := t.TempDir()
		seed := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, dir: dir, noWorker: true})
		for i := range 4 {
			seed.admit(t, 800+i, false)
		}
		if err := seed.app.ingressWAL.wal.Close(); err != nil {
			t.Fatal(err)
		}
		seed.abandon()
		restart := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, dir: dir})
		restart.refreshDevices(t)
		synctest.Wait()
		if stats := restart.provider.CollectionStats(); stats.ScheduledAttempts != 0 || stats.SnapshotsCollected != 0 {
			t.Fatalf("startup replay collected before the first slot: %+v", stats)
		}
		restart.waitCompletion(t, interval, 6)
		restart.assertNormalCadence(t, interval)
		restart.assertAccounting(t, 4, 0)
		// Fresh epoch: the restarted process's cumulative series start after the
		// original process started.
		if points := cadenceRuntimePoints(restart.signals(t), flowlog.MetricIO); len(points) == 0 || points[0].StartTime.Before(seed.epoch) {
			t.Fatal("restart did not start a fresh cumulative epoch")
		}

		for _, reason := range []telemetry.FlushReason{telemetry.FlushShutdown, telemetry.FlushLeadershipLost} {
			cadenceStep(t, fmt.Sprintf("terminal_%d_full", reason), func() {
				producer := &failingProducer{}
				s := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, producers: []sdkmetric.Producer{producer}})
				s.refreshDevices(t)
				s.failWrites(func([]byte) bool { return true })
				s.admit(t, 900, false)
				s.sleepPastSlot(interval)
				s.admit(t, 901, false)
				s.sleepPastSlot(interval)
				// A prior failed Collect with a third original waiting.
				s.admit(t, 902, false)
				producer.failNext()
				s.sleepPastSlot(interval)
				stats := s.provider.CollectionStats()
				if stats.RetainedCredits != 2 || stats.CollectFailures != 1 {
					t.Fatalf("setup: %+v, want two pinned covers and one failed collect", stats)
				}
				normal := len(s.devicesTimes(t))
				if err := s.stopWorker(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = s.provider.FlushLifecycle(ctx, reason)
				stats = s.provider.CollectionStats()
				if stats.TerminalAttempts != 0 || stats.TerminalSkippedFull != 1 || stats.RetainedCredits > 2 {
					t.Fatalf("terminal with both credits pinned: %+v, want skipped with no third snapshot", stats)
				}
				if err := s.app.ingressWAL.Drain(ctx); err == nil {
					t.Fatal("terminal drain reported success with uncovered originals")
				}
				if s.pending() != 3 {
					t.Fatalf("pending=%d after the terminal deadline, want all three originals preserved", s.pending())
				}
				if got := len(s.devicesTimes(t)); got != normal {
					t.Fatalf("terminal exception exported %d extra samples", got-normal)
				}
				// Teardown: no exporter call after shutdown.
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutdownCancel()
				_ = s.provider.Shutdown(shutdownCtx)
				writes := s.sink.Writes()
				time.Sleep(10 * interval)
				synctest.Wait()
				if got := s.sink.Writes(); got != writes {
					t.Fatalf("exporter called %d times after Shutdown", got-writes)
				}
				s.closed = true
				_ = s.app.ingressWAL.Close()
			})
			cadenceStep(t, fmt.Sprintf("terminal_%d_free_credit", reason), func() {
				s := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality})
				s.refreshDevices(t)
				s.sleepPastSlot(interval)
				s.admit(t, 950, false)
				synctest.Wait()
				normal := len(s.devicesTimes(t))
				if err := s.stopWorker(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.provider.FlushLifecycle(ctx, reason); err != nil {
					t.Fatalf("terminal flush: %v", err)
				}
				if err := s.provider.FlushLifecycle(ctx, reason); err != nil {
					t.Fatalf("idempotent terminal flush: %v", err)
				}
				stats := s.provider.CollectionStats()
				if stats.TerminalAttempts != 1 || stats.TerminalSkippedFull != 0 {
					t.Fatalf("terminal attempts=%+v, want exactly one", stats)
				}
				if err := s.app.ingressWAL.Drain(ctx); err != nil {
					t.Fatalf("terminal drain: %v", err)
				}
				if s.pending() != 0 {
					t.Fatalf("pending=%d, want the ready original committed by the one terminal cover", s.pending())
				}
				// The terminal sample is explicitly the lifecycle exception.
				if got := len(s.devicesTimes(t)); got != normal+1 {
					t.Fatalf("terminal samples=%d, want exactly one exception sample", got-normal)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Criterion 6.
// ---------------------------------------------------------------------------

func TestIngressWALScheduledCadence_ExportedSeriesMatrix(t *testing.T) {
	runCadencePrefix(t, func(t *testing.T, f *cadenceRuntimeFixture, interval time.Duration, temporality string) {
		f.waitCompletion(t, interval, 6)
		f.assertAccounting(t, cadencePrefixFlows, cadencePrefixHooks)
		type scenario struct {
			name string
			run  func(t *testing.T, s *cadenceRuntimeFixture) (flows, hooks int)
		}
		for _, sc := range []scenario{
			{"irregular_idle", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) {
				for i, at := range []time.Duration{3, 11, 41, 97, 98, 211} {
					time.Sleep(at*time.Second - time.Since(s.epoch))
					s.refreshDevices(t)
					s.admit(t, i, i%3 == 2)
				}
				return 4, 2
			}},
			{"burst_32_before_ack", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) {
				if err := s.stopWorker(); err != nil {
					t.Fatal(err)
				}
				for i := range 32 {
					s.admit(t, i, false)
				}
				s.startWorker()
				synctest.Wait()
				applied := 0
				for _, progress := range s.app.ingressWAL.orderedWindow() {
					if progress.receipt != nil {
						applied++
					}
				}
				if applied != 32 {
					t.Fatalf("originals applied before ACK=%d, want 32 in one window", applied)
				}
				if stats := s.provider.CollectionStats(); stats.ScheduledAttempts != 0 || stats.ReservedCredits != 1 {
					t.Fatalf("burst collected early or used one credit per original: %+v", stats)
				}
				return 32, 0
			}},
			{"preseeded_replay", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) { return 3, 0 }},
			// Logs are off here: a delayed writer holding the production stdout
			// mutex cannot be shared with a concurrent log export inside synctest.
			{"slow_exporter", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) {
				s.sink.mu.Lock()
				s.sink.delay = 5 * time.Second
				s.sink.mu.Unlock()
				for i := range 5 {
					s.admit(t, i, false)
					time.Sleep(7 * time.Second)
				}
				s.waitCompletion(t, interval, 8)
				s.sink.mu.Lock()
				s.sink.delay = 0
				s.sink.mu.Unlock()
				return 5, 0
			}},
			{"failing_exporter", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) {
				s.failWrites(func([]byte) bool { return true })
				for i := range 4 {
					s.admit(t, i, i == 3)
					time.Sleep(interval / 2)
				}
				time.Sleep(2 * interval)
				s.failWrites(nil)
				return 3, 1
			}},
			{"per_record_zero_connection_audit", func(t *testing.T, s *cadenceRuntimeFixture) (int, int) {
				s.admitBody(t, false, `{"event":{"nodeId":"synthetic-zero","start":"2000-01-01T00:00:00Z","end":"2000-01-01T00:00:01Z","virtualTraffic":[]}}{"event":{"actor":{"loginName":"synthetic@example.com"},"action":"CREATE","eventGroupID":"g1"}}{"event":{"actor":{"loginName":"synthetic@example.com"},"action":"UPDATE","eventGroupID":"g2"}}`)
				s.admitBody(t, false, cadenceMultiPortBody("synthetic-three", 443, 22, 53))
				s.admit(t, 1, false)
				return -1, 0 // logs asserted below
			}},
		} {
			cadenceStep(t, sc.name, func() {
				opts := cadenceOptions{interval: interval, temporality: temporality, observe: true}
				if sc.name == "preseeded_replay" {
					dir := t.TempDir()
					seed := newCadenceFixture(t, cadenceOptions{interval: interval, temporality: temporality, dir: dir, noWorker: true})
					for i := range 3 {
						seed.admit(t, i, false)
					}
					if err := seed.app.ingressWAL.wal.Close(); err != nil {
						t.Fatal(err)
					}
					seed.abandon()
					opts.dir = dir
				}
				if sc.name == "per_record_zero_connection_audit" {
					opts.config = func(c *config.Config) { c.Collectors.Flowlogs.LogMode = "per_record" }
				}
				if sc.name == "slow_exporter" {
					opts.config = func(c *config.Config) { c.Collectors.Flowlogs.LogMode = "off" }
				}
				s := newCadenceFixture(t, opts)
				s.refreshDevices(t)
				flows, hooks := sc.run(t, s)
				s.waitCompletion(t, interval, 8)
				s.assertDeliveredCadence(t, interval, sc.name == "failing_exporter")
				if sc.name == "slow_exporter" {
					if got := cadenceRuntimeLatestTotal(s.signals(t), flowlog.MetricIO); got != float64(flows*30) {
						t.Fatalf("slow exporter raw io total=%v, want %d", got, flows*30)
					}
					return
				}
				if flows >= 0 {
					s.assertAccounting(t, flows, hooks)
					return
				}
				counts := map[string]int{}
				for _, record := range cadenceLogs(s.signals(t)) {
					counts[record.EventName]++
				}
				// per_record: exactly one summary per classified flow record (three
				// connections still yield one), and one log per audit record. A
				// record with no traffic is not classified as a flow by the stream
				// receiver, so it yields no summary; the durable bound still counts
				// it conservatively.
				if counts["tailscale.network.flow"] != 2 || counts["tailscale.config.audit"] != 2 {
					t.Fatalf("per_record/audit logs=%v, want 2 flow summaries and 2 audit records", counts)
				}
				if got := cadenceRuntimeLatestTotal(s.signals(t), flowlog.MetricIO); got != 33 {
					t.Fatalf("raw io total=%v, want 33", got)
				}
			})
		}
		cadenceStep(t, "persistent_fault_saturation", func() {
			runCadenceSaturation(t, interval, temporality)
		})
	})
}

// ---------------------------------------------------------------------------
// Supplemental focused runtime checks (not criterion proof by themselves).
// ---------------------------------------------------------------------------

func TestIngressWALWindow_ThirtyTwoOriginalsShareFirstCover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newCadenceFixture(t, cadenceOptions{interval: 15 * time.Second, temporality: "cumulative", noWorker: true})
		// Seed one complete runnable window before starting application. This is
		// the accepted replay/burst window case, not a throughput measurement;
		// the provider's registration epoch and production clock stay unchanged.
		for i := range 32 {
			f.admit(t, i, false)
		}
		f.startWorker()
		synctest.Wait()
		applied := 0
		for _, progress := range f.app.ingressWAL.orderedWindow() {
			if progress.receipt != nil {
				applied++
			}
		}
		if applied != 32 {
			t.Fatalf("applied originals before ACK=%d, want 32", applied)
		}
		if stats := f.provider.CollectionStats(); stats.ScheduledAttempts != 0 || stats.ReservedCredits != 1 {
			t.Fatalf("burst collected early or consumed one credit per original: %+v", stats)
		}
		time.Sleep(16 * time.Second)
		synctest.Wait()
		f.assertAccounting(t, 32, 0)
		if stats := f.provider.CollectionStats(); stats.ScheduledAttempts != 1 || stats.RequiredSnapshotsAcknowledged != 1 {
			t.Fatalf("burst did not finish on one normal first-cover snapshot: %+v", stats)
		}
	})
}
