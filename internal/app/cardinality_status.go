package app

import (
	"math"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// enableCostEstimates forecasts the marginal work for high-cost toggles that
// are currently off. The fleet count comes from the live enrichment caches and
// flow counts from the current cardinality snapshot, so an empty/starting
// process truthfully reports zero rather than inventing a nominal deployment.
func enableCostEstimates(cfg *config.Config, devices int, seriesByMetric map[string]int) []statusdata.EnableCostEstimate {
	if devices < 0 {
		devices = 0
	}
	estimates := []statusdata.EnableCostEstimate{
		{
			Key:                  "collectors.devices.collect_posture",
			Enabled:              cfg.Collectors.Devices.CollectPosture,
			AddedAPICallsPerTick: disabledEstimate(cfg.Collectors.Devices.CollectPosture, devices),
			AddedSeries:          disabledEstimate(cfg.Collectors.Devices.CollectPosture, devices),
			Basis:                "one posture request and at least one posture series per currently cached device",
		},
		{
			Key:                  "collectors.devices.collect_device_invites",
			Enabled:              cfg.Collectors.Devices.CollectDeviceInvites,
			AddedAPICallsPerTick: disabledEstimate(cfg.Collectors.Devices.CollectDeviceInvites, devices),
			AddedSeries:          disabledEstimate(cfg.Collectors.Devices.CollectDeviceInvites, devices),
			Basis:                "one invite request and per-device invite series per currently cached device",
		},
	}

	flowSeries := 0
	for metric, count := range seriesByMetric {
		if strings.HasPrefix(metric, "tailscale.network.") {
			flowSeries += count
		}
	}
	identitySeries := 0
	identityBasis := "identity dimensions require cardinality.flow.node_dims"
	if cfg.Cardinality.Flow.NodeDims {
		identitySeries = flowSeries * max(devices-1, 0)
		identityBasis = "current active flow series × (currently cached devices − 1)"
	}
	if cfg.Cardinality.Flow.IdentityDims {
		identitySeries = 0
		identityBasis = "already enabled"
	}
	estimates = append(estimates, statusdata.EnableCostEstimate{
		Key:         "cardinality.flow.identity_dims",
		Enabled:     cfg.Cardinality.Flow.IdentityDims,
		AddedSeries: identitySeries,
		Basis:       identityBasis,
	})
	return estimates
}

func disabledEstimate(enabled bool, value int) int {
	if enabled {
		return 0
	}
	return value
}

// aggregateLabelSnapshot merges the per-tailnet label-value snapshots into a
// single list keyed by (metric,label): summing distinct counts, OR-ing capped,
// and unioning example values (bounded). Mirrors aggregateCardSnapshot so the
// combined cardinality section spans the process + every tailnet provider.
func (a *App) aggregateLabelSnapshot() []telemetry.LabelStat {
	type key struct{ metric, label string }
	merged := map[key]*telemetry.LabelStat{}
	seen := map[key]map[string]struct{}{}
	add := func(snaps []telemetry.LabelStat) {
		for _, ls := range snaps {
			k := key{ls.Metric, ls.Label}
			cur := merged[k]
			if cur == nil {
				cur = &telemetry.LabelStat{Metric: ls.Metric, Label: ls.Label}
				merged[k] = cur
				seen[k] = map[string]struct{}{}
			}
			cur.Distinct += ls.Distinct
			cur.Capped = cur.Capped || ls.Capped
			for _, ex := range ls.Examples {
				if _, ok := seen[k][ex]; ok {
					continue
				}
				// Bound the merged example set to the same cap a single tracker uses.
				if len(cur.Examples) >= defaultLabelExampleMerge {
					break
				}
				seen[k][ex] = struct{}{}
				cur.Examples = append(cur.Examples, ex)
			}
		}
	}
	add(a.procCard.LabelSnapshot())
	for _, rt := range a.runtimes {
		add(rt.card.LabelSnapshot())
	}
	out := make([]telemetry.LabelStat, 0, len(merged))
	for _, v := range merged {
		sort.Strings(v.Examples)
		out = append(out, *v)
	}
	return out
}

// defaultLabelExampleMerge bounds the example values retained per (metric,label)
// after merging across providers (matches the tracker's default value cap).
const defaultLabelExampleMerge = 100

// runtimeCardinalityInfo builds ONE tailnet runtime's own cardinality section
// (#325), from rt.card alone — never merged with any other runtime's tracker or
// the process provider's (self-obs, admin/metrics HTTP). That merge is exactly
// what aggregateCardSnapshot/aggregateLabelSnapshot do for the COMBINED
// top-level section; this is the per-tailnet counterpart the admin page's
// tailnet selector filters onto.
//
// Growth is always empty: the retained-history sampler backing
// Status.Cardinality.Growth runs once per process (a.runtimeHist), not once per
// tailnet runtime, so there is no per-tailnet trend to report. rt.card being nil
// (a runtime whose provider never got a tracker) degrades to the same
// Available=false shape a real tracker reports before its first Report.
func runtimeCardinalityInfo(rt *tailnetRuntime, selfObs bool, th statusdata.CardinalityThresholds, metricByName map[string]metricdoc.Metric) statusdata.CardinalityInfo {
	if rt == nil || rt.card == nil {
		return statusdata.CardinalityInfo{Available: false, Thresholds: th}
	}
	return cardinalityInfo(selfObs, rt.card.Snapshot(), rt.card.LabelSnapshot(), nil, th, metricByName)
}

// cardSeriesLevel classifies a metric's series count against the configured
// thresholds: "critical" (>= critical, when set), "warning" (>= warning, when
// set), or "" (below both / disabled).
func cardSeriesLevel(count int, th statusdata.CardinalityThresholds) string {
	if th.Critical > 0 && count >= th.Critical {
		return "critical"
	}
	if th.Warning > 0 && count >= th.Warning {
		return "warning"
	}
	return ""
}

// growthDeltaPct is the percentage change from the oldest retained sample to the
// most recent. It is 0 when there are fewer than two samples or the window
// started at zero (avoids a divide-by-zero and a meaningless "∞%").
func growthDeltaPct(series []int) float64 {
	if len(series) < 2 {
		return 0
	}
	first, last := series[0], series[len(series)-1]
	if first == 0 {
		return 0
	}
	return float64(last-first) / float64(first) * 100
}

// buildLabelRows aggregates per-(metric,label) stats by label key across metrics,
// sorted by total distinct desc. Each metric contribution carries its prom name
// and example values.
func buildLabelRows(labels []telemetry.LabelStat, metricByName map[string]metricdoc.Metric) []statusdata.LabelRow {
	byLabel := map[string]*statusdata.LabelRow{}
	order := []string{}
	for _, ls := range labels {
		row := byLabel[ls.Label]
		if row == nil {
			row = &statusdata.LabelRow{Label: ls.Label}
			byLabel[ls.Label] = row
			order = append(order, ls.Label)
		}
		row.TotalDistinct += ls.Distinct
		row.Capped = row.Capped || ls.Capped
		row.Metrics = append(row.Metrics, statusdata.LabelMetricRow{
			Metric:   ls.Metric,
			PromName: promNameOf(ls.Metric, metricByName),
			Distinct: ls.Distinct,
			Capped:   ls.Capped,
			Examples: ls.Examples,
		})
	}
	out := make([]statusdata.LabelRow, 0, len(order))
	for _, l := range order {
		row := byLabel[l]
		sort.Slice(row.Metrics, func(i, j int) bool {
			if row.Metrics[i].Distinct != row.Metrics[j].Distinct {
				return row.Metrics[i].Distinct > row.Metrics[j].Distinct
			}
			return row.Metrics[i].Metric < row.Metrics[j].Metric
		})
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalDistinct != out[j].TotalDistinct {
			return out[i].TotalDistinct > out[j].TotalDistinct
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// buildGrowthRows turns the per-metric series history into growth rows sorted by
// absolute percentage change desc (biggest movers first), then metric name.
func buildGrowthRows(perMetric map[string][]int, metricByName map[string]metricdoc.Metric) []statusdata.GrowthRow {
	out := make([]statusdata.GrowthRow, 0, len(perMetric))
	for metric, series := range perMetric {
		if len(series) == 0 {
			continue
		}
		out = append(out, statusdata.GrowthRow{
			Metric:   metric,
			PromName: promNameOf(metric, metricByName),
			Current:  series[len(series)-1],
			DeltaPct: growthDeltaPct(series),
			Series:   series,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := math.Abs(out[i].DeltaPct), math.Abs(out[j].DeltaPct)
		if ai != aj {
			return ai > aj
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// promNameOf resolves a source metric's Prometheus name via the catalog, or ""
// when the metric is not in the catalog.
func promNameOf(metric string, metricByName map[string]metricdoc.Metric) string {
	if m, ok := metricByName[metric]; ok {
		return m.PromName()
	}
	return ""
}
