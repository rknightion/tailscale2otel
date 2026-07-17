package telemetry

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// PerTailnetOptions overrides the per-tailnet pieces of an Options template.
// InstanceID MUST be distinct per tailnet: on Grafana Cloud's OTLP->Prometheus
// mapping, resource attributes other than job/instance/service_* live only in
// target_info, so two tailnet providers sharing one service.instance.id would
// emit colliding series (same job+instance+labels) and ambiguous target_info
// rows. Making instance unique per tailnet models each tailnet as its own OTLP
// target. tailscale.tailnet itself is NOT one of those Resource attributes —
// per Roadmap item L it is stamped as a per-signal const attribute on every
// metric data point/log record/span (see constLabelAttrs in provider.go), not
// carried in the Resource/target_info; the InstanceID uniqueness requirement
// above is about whatever other Resource attributes each per-tailnet target
// carries, not about disambiguating tailnet in target_info.
type PerTailnetOptions struct {
	Name       string
	InstanceID string // distinct service.instance.id for this tailnet (required)
}

// ProviderSet owns one process-level Provider (no tailscale.tailnet attribute;
// carries process/global self-obs) plus one Provider per tailnet (each Provider
// stamps tailscale.tailnet=<name> as a const attribute on every signal it emits,
// via constLabelAttrs — NOT a Resource attribute; carries that tailnet's signals
// + per-tailnet self-obs). All providers export to the same configured backend.
type ProviderSet struct {
	process *Provider
	tailnet map[string]*Provider
	order   []string
}

// NewProviderSet builds the process provider from base (with TailnetName cleared)
// and one tailnet provider per entry (base with TailnetName + InstanceID set). On
// any failure it shuts down whatever was already built and returns the error.
func NewProviderSet(ctx context.Context, base Options, tailnets []PerTailnetOptions) (*ProviderSet, error) {
	procOpts := base
	procOpts.TailnetName = ""
	proc, err := NewProvider(ctx, procOpts)
	if err != nil {
		return nil, fmt.Errorf("process provider: %w", err)
	}
	ps := &ProviderSet{process: proc, tailnet: make(map[string]*Provider, len(tailnets))}
	for _, tn := range tailnets {
		o := base
		o.TailnetName = tn.Name
		o.InstanceID = tn.InstanceID // distinct per tailnet — see PerTailnetOptions
		p, err := NewProvider(ctx, o)
		if err != nil {
			_ = ps.Shutdown(ctx)
			return nil, fmt.Errorf("tailnet %q provider: %w", tn.Name, err)
		}
		ps.tailnet[tn.Name] = p
		ps.order = append(ps.order, tn.Name)
	}
	return ps, nil
}

// Process returns the process-level provider.
func (s *ProviderSet) Process() *Provider { return s.process }

// Tailnet returns the provider for name, or nil if unknown.
func (s *ProviderSet) Tailnet(name string) *Provider { return s.tailnet[name] }

// TailnetNames returns the tailnet names in construction order.
func (s *ProviderSet) TailnetNames() []string { return s.order }

// PromGatherers returns the per-provider Prometheus registries (process first,
// then each tailnet in construction order) merged as prometheus.Gatherers — the
// safe way to expose multiple registries with differing target_info label sets at
// one /metrics endpoint. Empty when the Prometheus reader is disabled.
func (s *ProviderSet) PromGatherers() prometheus.Gatherers {
	var gs prometheus.Gatherers
	if g := s.process.PromGatherer(); g != nil {
		gs = append(gs, g)
	}
	for _, name := range s.order {
		if p := s.tailnet[name]; p != nil {
			if g := p.PromGatherer(); g != nil {
				gs = append(gs, g)
			}
		}
	}
	return gs
}

// Shutdown flushes and stops every provider (process + all tailnets). The
// providers are independent, so they are shut down concurrently under the shared
// deadline (see shutdownAll / #204): one tailnet's blocked exporter must not
// consume the whole budget and prevent other tailnets — or the process provider —
// from flushing. Each provider in turn shuts its own metric/log/trace pipelines
// concurrently, so the concurrency is two levels deep under one overall ceiling.
func (s *ProviderSet) Shutdown(ctx context.Context) error {
	fns := make([]func(context.Context) error, 0, 1+len(s.order))
	if s.process != nil {
		fns = append(fns, s.process.Shutdown)
	}
	for _, name := range s.order {
		if p := s.tailnet[name]; p != nil {
			fns = append(fns, p.Shutdown)
		}
	}
	return shutdownAll(ctx, fns...)
}
