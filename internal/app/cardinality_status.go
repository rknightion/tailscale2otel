package app

import (
	"math"
	"sort"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

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
