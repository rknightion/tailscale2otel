package nodemetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// Count the actual baseline entries, not a proxy: curation must use the same
// four full source-series keys as raw forwarding, even when raw is filtered.
func TestServiceIO_OneRawBaselinePerSourceSeries(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw allowed", true: "raw denied"}[deny], func(t *testing.T) {
			b, err := os.ReadFile("testdata/serve.prom")
			if err != nil {
				t.Fatal(err)
			}
			body := string(b)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			opts := Options{Targets: []Target{{URL: srv.URL, Instance: "node-a"}}}
			if deny {
				opts.MetricDeny = []string{"tailscaled_serve_.*"}
			}
			c := New(opts)
			rec := telemetrytest.New()
			for tick := 0; tick < 2; tick++ {
				if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
					t.Fatal(err)
				}
				if len(c.prev) != 4 {
					t.Fatalf("tick %d baselines=%d, want exactly four raw source keys", tick, len(c.prev))
				}
				for _, direction := range []string{"inbound", "outbound"} {
					for _, service := range []string{"svc:sample-api", "svc:sample-web"} {
						key := baselineKey(c.static[0].id, "tailscaled_serve_"+direction+"_bytes_total", map[string]string{"service": service})
						if _, ok := c.prev[key]; !ok {
							t.Errorf("missing raw baseline for %s %s", direction, service)
						}
					}
				}
				body = strings.NewReplacer(" 100\n", " 110\n", " 200\n", " 220\n", " 300\n", " 330\n", " 400\n", " 440\n").Replace(body)
			}
		})
	}
}
