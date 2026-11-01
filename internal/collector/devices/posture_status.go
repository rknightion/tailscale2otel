package devices

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sort"

	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// postureNameLimit caps the fleet-wide posture vocabulary each poll. Selection
// is lexical and shared by gauges, rollups and logs; assertions are never labels.
const postureNameLimit = 50

func (c *Collector) emitPostureStatus(e telemetry.Emitter, devs []tsapi.RichDevice) {
	names := map[string]bool{}
	for _, d := range devs {
		for name := range d.PostureStatus {
			names[name] = true
		}
	}
	selected := make([]string, 0, len(names))
	for name := range names {
		selected = append(selected, name)
	}
	sort.Strings(selected)
	dropped := max(0, len(selected)-postureNameLimit)
	selected = selected[:min(len(selected), postureNameLimit)]
	e.Gauge(docPostureNamesDropped.Name, docPostureNamesDropped.Unit, docPostureNamesDropped.Description, float64(dropped), nil)
	counts := make(map[string][2]int, len(selected))
	current := make(map[string][32]byte)
	for _, name := range selected {
		counts[name] = [2]int{}
	}
	for _, d := range devs {
		failures := map[string]tsapi.PostureStatus{}
		for _, name := range selected {
			verdict, ok := d.PostureStatus[name]
			if !ok {
				continue
			}
			if c.perEntity {
				attrs := telemetry.Attrs{semconv.HostID: d.ID, semconv.HostName: d.Hostname, semconv.AttrPostureName: name}
				c.gsb.Add(docPosturePassing.Name, docPosturePassing.Unit, docPosturePassing.Description, boolToFloat(verdict.Passing), attrs)
				c.gsb.Add(docPostureImpacting.Name, docPostureImpacting.Unit, docPostureImpacting.Description, boolToFloat(verdict.Impacting), attrs)
			}
			if !verdict.Passing {
				count := counts[name]
				i := 0
				if verdict.Impacting {
					i = 1
				}
				count[i]++
				counts[name] = count
				// Assertion ordering has no semantic meaning; do not emit a change just
				// because the server reordered its list. Never mutate the API result.
				verdict.FailingAssertions = slices.Clone(verdict.FailingAssertions)
				sort.Strings(verdict.FailingAssertions)
				failures[name] = verdict
			}
		}
		if len(failures) == 0 {
			continue
		}
		raw, err := json.Marshal(failures)
		if err != nil {
			continue
		}
		signature := sha256.Sum256(raw)
		current[d.ID] = signature
		previous, seen := c.lastPostureStatus[d.ID]
		if c.postureLogMode != postureLogOff && (!seen || previous != signature) {
			e.LogEvent(telemetry.Event{Name: docPostureStatusLog.Name, Severity: telemetry.SeverityWarn, Body: "device posture failing assertions changed", Attrs: telemetry.Attrs{semconv.HostID: d.ID, semconv.HostName: d.Hostname, attrPostureDetails: string(raw)}})
		}
	}
	c.lastPostureStatus = current // prune recovered, removed and no-longer-selected devices
	for _, name := range selected {
		for i, count := range counts[name] {
			c.gsb.Add(docPostureFailed.Name, docPostureFailed.Unit, docPostureFailed.Description, float64(count), telemetry.Attrs{semconv.AttrPostureName: name, semconv.AttrPostureImpacting: i == 1})
		}
	}
}
