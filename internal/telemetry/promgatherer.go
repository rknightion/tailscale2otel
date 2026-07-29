package telemetry

import (
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// targetInfoMetric is the info series the OTEL Prometheus exporter derives from
// each provider's Resource — one series per provider, value always 1, every
// resource attribute a label.
const targetInfoMetric = "target_info"

// mergedGatherer merges the per-provider Prometheus registries exactly the way
// prometheus.Gatherers does, except that a target_info series identical to one
// an earlier provider already contributed is collapsed rather than reported as
// a duplicate (#519).
//
// Two providers emit an identical target_info precisely when they were built
// from an identical Resource, and that is the DEFAULT single-tailnet shape:
// instanceFor deliberately keeps the bare base service.instance.id for the one
// tailnet, so the process provider and the tailnet provider agree on every
// resource attribute. The plain merge is not wrong about it — the series really
// is duplicated — but the duplicate carries no information the first copy does
// not, so the only effect was a gather error on every single scrape, forever.
// Under the endpoint's ContinueOnError that is a permanent WARN that also
// buries any genuine collision behind it.
//
// The rule is deliberately narrow, and target_info is deliberately the only
// name it applies to. Any OTHER duplicate series means two providers are
// emitting DIFFERENT data under one identity — the pii_filter.tailnet_name=false
// case in #103, where the distinguishing attribute is gone and the second
// provider's value is silently dropped. That is real data loss and must stay
// loud, so it is left to the standard merge to report.
type mergedGatherer struct{ gs prometheus.Gatherers }

func (m mergedGatherer) Gather() ([]*dto.MetricFamily, error) {
	var errs prometheus.MultiError
	seen := make(map[string]struct{})
	filtered := make(prometheus.Gatherers, 0, len(m.gs))
	for _, g := range m.gs {
		mfs, err := g.Gather()
		if err != nil {
			errs.Append(err)
		}
		filtered = append(filtered, staticGatherer(dropSeenTargetInfo(mfs, seen)))
	}
	// The real merge still runs over the filtered families, so every upstream
	// consistency check (help/type agreement, suffix collisions, duplicate
	// series that are NOT redundant target_info) is preserved verbatim.
	out, err := filtered.Gather()
	if err != nil {
		errs.Append(err)
	}
	return out, errs.MaybeUnwrap()
}

// staticGatherer exposes an already-gathered snapshot as a Gatherer so the
// filtered families can be fed back through prometheus.Gatherers.
type staticGatherer []*dto.MetricFamily

func (s staticGatherer) Gather() ([]*dto.MetricFamily, error) { return s, nil }

// dropSeenTargetInfo returns mfs with any target_info series whose label set is
// already in seen removed, recording the ones it keeps. Families are rebuilt
// rather than mutated: the gatherer that produced them owns them.
func dropSeenTargetInfo(mfs []*dto.MetricFamily, seen map[string]struct{}) []*dto.MetricFamily {
	out := make([]*dto.MetricFamily, 0, len(mfs))
	for _, mf := range mfs {
		if mf.GetName() != targetInfoMetric {
			out = append(out, mf)
			continue
		}
		kept := make([]*dto.Metric, 0, len(mf.Metric))
		for _, met := range mf.Metric {
			key := labelSetKey(met)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			kept = append(kept, met)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, &dto.MetricFamily{
			Name:   mf.Name,
			Help:   mf.Help,
			Type:   mf.Type,
			Unit:   mf.Unit,
			Metric: kept,
		})
	}
	return out
}

// labelSetKey identifies a series by its label set, order-independently. NUL and
// SOH separate, so no label name or value can forge a different key's encoding.
func labelSetKey(m *dto.Metric) string {
	pairs := make([]string, 0, len(m.Label))
	for _, lp := range m.Label {
		pairs = append(pairs, lp.GetName()+"\x00"+lp.GetValue())
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\x01")
}
