package flowlog_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// Flow records reach the processor from two directions — the poller and the
// streaming receiver — and the wire format decodes loosely on purpose: `proto`
// is a number that transportName maps to a name, and src/dst are "addr:port"
// strings that splitHostPort/serviceName pick apart. A record that decodes but
// carries a malformed address, an unmapped proto, or a count that overflows the
// rollup accumulator must still process without panicking.
//
// Seeds cover the shapes the API actually emits: the four traffic buckets, a
// physical-traffic entry with `proto` omitted (decodes to zero), IPv6
// bracket-port addresses, and an address with no port at all.
func FuzzProcessorProcessAll(f *testing.F) {
	seeds := []string{
		`{"logs":[{"nodeId":"n1","virtualTraffic":[{"proto":6,"src":"100.64.0.1:443","dst":"100.64.0.2:80","txBytes":10,"rxBytes":20,"txPkts":1,"rxPkts":2}]}]}`,
		`{"logs":[{"nodeId":"n1","physicalTraffic":[{"src":"1.2.3.4:0","dst":"5.6.7.8:41641","txBytes":1}]}]}`,
		`{"logs":[{"nodeId":"n1","exitTraffic":[{"proto":17,"src":"[fd7a::1]:53","dst":"[2606:4700::1111]:53"}]}]}`,
		`{"logs":[{"nodeId":"n1","subnetTraffic":[{"proto":255,"src":"no-port","dst":""}]}]}`,
		`{"logs":[{"nodeId":"","virtualTraffic":[{"proto":-1,"src":":","dst":"::::","txBytes":-9223372036854775808}]}]}`,
		`{"logs":[]}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		var resp flowlog.NetworkResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return // not a flow-log response; the decoder rejecting it is correct
		}

		// A fresh processor per iteration: the rollup accumulator is stateful, and
		// carrying it across inputs would make a failure depend on execution order.
		rec := telemetrytest.New()
		p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
			LogMode:                   "per_connection",
			IncludeSourcePort:         true,
			IncludeDestinationPort:    true,
			IncludeDestinationService: true,
			NodeDims:                  true,
			FlowMetricsMode:           "both",
			ExitNodeAttribution:       true,
		})
		p.ProcessAll(resp, rec.Emitter())
		p.FlushRollup(rec.Emitter())
	})
}
