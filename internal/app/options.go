package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "tailscale2otel"

// telemetryOptions maps the OTLP config into telemetry.Options, computing the
// Grafana Cloud Basic-auth header when grafana_cloud credentials are set.
func telemetryOptions(cfg *config.Config, version string) telemetry.Options {
	headers := make(map[string]string, len(cfg.OTLP.Headers)+1)
	for k, v := range cfg.OTLP.Headers {
		headers[k] = v.Reveal() // Secret -> raw string at the point of legitimate use (#73)
	}
	if gc := cfg.OTLP.GrafanaCloud; gc.InstanceID != "" {
		headers["Authorization"] = "Basic " +
			base64.StdEncoding.EncodeToString([]byte(gc.InstanceID+":"+gc.Token.Reveal()))
	}
	prov := cfg.Provider
	if prov == "" {
		prov = "tailscale"
	}
	return telemetry.Options{
		ServiceName:              serviceName,
		ServiceVersion:           version,
		Provider:                 prov,
		InstanceID:               instanceID(cfg),
		Protocol:                 cfg.OTLP.Protocol,
		Endpoint:                 cfg.OTLP.Endpoint,
		Headers:                  headers,
		Insecure:                 cfg.OTLP.TLS.Insecure,
		InsecureSkipVerify:       cfg.OTLP.TLS.InsecureSkipVerify,
		CAFile:                   cfg.OTLP.TLS.CAFile,
		CertFile:                 cfg.OTLP.TLS.CertFile,
		KeyFile:                  cfg.OTLP.TLS.KeyFile,
		MetricInterval:           cfg.OTLP.MetricInterval.D(),
		MetricExportBatchSize:    cfg.OTLP.MetricExportBatchSize,
		MaxLogBodyBytes:          cfg.OTLP.Limits.LogBodyBytes,
		MaxLogAttrValueBytes:     cfg.OTLP.Limits.LogAttributeValueBytes,
		SelfObsEnabled:           cfg.SelfObservability.Enabled,
		CardinalityLimit:         cfg.Cardinality.MetricLimit,
		CardinalityLabelValueCap: cfg.Cardinality.LabelValueSampleCap,
		TracingEnabled:           cfg.Tracing.Enabled,
		TraceSampler:             cfg.Tracing.Sampler,
		TraceSamplerArg:          cfg.Tracing.SamplerArg,
		PIIFilter:                piiCategories(cfg.PIIFilter),
		PrometheusEnabled:        cfg.Prometheus.Enabled,

		Transport: transportOptions(cfg.OTLP.Compression, cfg.OTLP.Timeout, cfg.OTLP.MaxRequestSize,
			cfg.OTLP.GRPCReconnectionPeriod, cfg.OTLP.Retry),
		Signals: telemetry.SignalOptions{
			Metrics: signalOverride(cfg.OTLP.Metrics),
			Logs:    signalOverride(cfg.OTLP.Logs),
			Traces:  signalOverride(cfg.OTLP.Traces),
		},
		Batch: telemetry.BatchOptions{
			Logs:   queueOptions(cfg.OTLP.Batch.Logs),
			Traces: queueOptions(cfg.OTLP.Batch.Traces),
		},
		Stdout: telemetry.StdoutOptions{
			MetricInterval: cfg.OTLP.Stdout.MetricInterval.D(),
			Pretty:         cfg.OTLP.Stdout.Pretty,
		},
		Sampling: telemetry.SamplingOptions{
			Scrape:       samplerClass(cfg.Tracing.Samplers.Scrape),
			Receiver:     samplerClass(cfg.Tracing.Samplers.Receiver),
			Background:   samplerClass(cfg.Tracing.Samplers.Background),
			RemoteParent: cfg.Tracing.RemoteParent,
		},
		Resource: telemetry.ResourceOptions{
			ServiceNamespace:      cfg.Resource.ServiceNamespace,
			DeploymentEnvironment: cfg.Resource.DeploymentEnvironment,
			Attributes:            cfg.Resource.Attributes,
			FromEnv:               cfg.Resource.FromEnv,
		},
		Profiles: telemetry.ProfileOptions{
			// Correlation needs a span to label and a running profiler to receive
			// the labels; Validate() already refuses the half-configured shape, so
			// this conjunction is belt-and-braces rather than the enforcement.
			Enabled: cfg.Profiling.Pyroscope.SpanProfiles.Enabled &&
				cfg.Tracing.Enabled && cfg.Profiling.Pyroscope.Enabled,
		},
	}
}

// transportOptions maps the shared OTLP transport knobs (#360). Every zero value
// is passed through as a zero so the telemetry layer can tell "unset, defer to
// the standard OTEL_EXPORTER_* variable" from "explicitly configured".
func transportOptions(compression string, timeout config.Duration, maxRequestSize int,
	grpcReconnect config.Duration, retry config.OTLPRetryConfig,
) telemetry.TransportOptions {
	return telemetry.TransportOptions{
		Compression:            compression,
		Timeout:                timeout.D(),
		MaxRequestSize:         maxRequestSize,
		GRPCReconnectionPeriod: grpcReconnect.D(),
		Retry:                  retryPolicy(retry),
	}
}

// retryPolicy returns nil for a wholly untouched retry block, which is what
// tells the exporter to keep its own default policy. A block is "touched" if any
// field is set — including Enabled: false, which is the explicit way to turn
// retry off and must not be confused with an omitted key.
func retryPolicy(r config.OTLPRetryConfig) *telemetry.RetryPolicy {
	if r.Enabled == nil && r.InitialInterval.D() == 0 && r.MaxInterval.D() == 0 && r.MaxElapsedTime.D() == 0 {
		return nil
	}
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return &telemetry.RetryPolicy{
		Enabled:         enabled,
		InitialInterval: r.InitialInterval.D(),
		MaxInterval:     r.MaxInterval.D(),
		MaxElapsedTime:  r.MaxElapsedTime.D(),
	}
}

// signalOverride returns nil for an untouched per-signal block so the signal
// inherits the common settings verbatim (#361). Returning a zero *SignalOverride
// instead would be indistinguishable in effect but would lose that "no override
// configured" fact, which the effective-settings echo reports.
func signalOverride(c config.OTLPSignalConfig) *telemetry.SignalOverride {
	empty := c.Enabled == nil && c.Protocol == "" && c.Endpoint == "" && len(c.Headers) == 0 &&
		c.TLS == (config.OTLPSignalTLS{}) && c.Compression == "" && c.Timeout.D() == 0 &&
		c.MaxRequestSize == 0 && c.GRPCReconnectionPeriod.D() == 0 &&
		retryPolicy(c.Retry) == nil
	if empty {
		return nil
	}
	o := &telemetry.SignalOverride{
		Enabled:            c.Enabled,
		Protocol:           c.Protocol,
		Endpoint:           c.Endpoint,
		Insecure:           c.TLS.Insecure,
		InsecureSkipVerify: c.TLS.InsecureSkipVerify,
		CAFile:             c.TLS.CAFile,
		CertFile:           c.TLS.CertFile,
		KeyFile:            c.TLS.KeyFile,
	}
	if len(c.Headers) > 0 {
		// Reveal at the point of legitimate use (#73), same as the common block.
		// This REPLACES rather than merges with the common headers, so one signal's
		// credential can never reach another's endpoint.
		o.Headers = make(map[string]string, len(c.Headers))
		for k, v := range c.Headers {
			o.Headers[k] = v.Reveal()
		}
	}
	if t := transportOptions(c.Compression, c.Timeout, c.MaxRequestSize,
		c.GRPCReconnectionPeriod, c.Retry); t != (telemetry.TransportOptions{}) {
		o.Transport = &t
	}
	return o
}

// queueOptions maps one signal's processor-queue settings (#358).
func queueOptions(q config.OTLPQueueConfig) telemetry.QueueOptions {
	return telemetry.QueueOptions{
		MaxQueueSize:       q.MaxQueueSize,
		ExportMaxBatchSize: q.ExportMaxBatchSize,
		ExportInterval:     q.ExportInterval.D(),
		ExportTimeout:      q.ExportTimeout.D(),
	}
}

// samplerClass maps one workload class's head-sampler override (#372). An empty
// Sampler means the class inherits the global tracing.sampler.
func samplerClass(c config.TracingSamplerClass) telemetry.SamplerClassOptions {
	return telemetry.SamplerClassOptions{Sampler: c.Sampler, SamplerArg: c.Arg}
}

// piiCategories converts config.PIIFilterConfig into the pii.Categories map used
// by the redactor. Every category is explicitly mapped; the redactor treats an
// absent key as enabled, but we emit all 14 so the config layer's defaults (all
// true) are faithfully reflected and future categories can't silently escape.
func piiCategories(f config.PIIFilterConfig) pii.Categories {
	return pii.Categories{
		pii.CatEmails:           f.Emails,
		pii.CatUserDisplayNames: f.UserDisplayNames,
		pii.CatUserIDs:          f.UserIDs,
		pii.CatHostnames:        f.Hostnames,
		pii.CatNodeIDs:          f.NodeIDs,
		pii.CatTailscaleIPs:     f.TailscaleIPs,
		pii.CatInternalIPs:      f.InternalIPs,
		pii.CatExternalIPs:      f.ExternalIPs,
		pii.CatServiceAddrs:     f.ServiceAddrs,
		pii.CatEndpointPaths:    f.EndpointPaths,
		pii.CatNetworkTopology:  f.NetworkTopology,
		pii.CatTailnetName:      f.TailnetName,
		pii.CatFreeTextDetails:  f.FreeTextDetails,
		pii.CatCommandText:      f.CommandText,
	}
}

// instanceID resolves the service.instance.id resource attribute.
//
// Priority:
//  1. Explicit self_observability.instance_id — always honored (operator's choice).
//  2. pii_filter.hostnames == true  → bare hostname (backward-compatible default).
//  3. pii_filter.hostnames == false → first 12 hex chars of SHA-256(hostname).
//     Uniqueness per host is preserved; the name is not disclosed.
//
// The hostname policy lives here (the app layer) so internal/telemetry stays
// free of it; a failed os.Hostname() yields "", which buildResource omits.
func instanceID(cfg *config.Config) string {
	if cfg.SelfObservability.InstanceID != "" {
		return cfg.SelfObservability.InstanceID
	}
	host, _ := os.Hostname()
	if cfg.PIIFilter.Hostnames || host == "" {
		return host // "" (failed lookup) is omitted by buildResource
	}
	// Hostnames PII category disabled: return a stable non-reversible identifier
	// so service.instance.id still uniquely identifies the host without leaking
	// the hostname to the OTLP backend.
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:])[:12]
}

// hsapiOptions maps the Headscale config into hsapi.Options. Auth is the Bearer
// API key; the minimal client uses only the request timeout (no retry in v1).
//
// tracer produces one client span per Headscale API call, bringing it to parity
// with the Tailscale client, which has been traced since #371's baseline. The
// span-name label space is bounded by construction — the client only ever calls
// five literal paths — so no per-item elision is needed. A no-op tracer (which
// is what Provider hands over when tracing.enabled=false) makes this free.
func hsapiOptions(cfg *config.Config, tracer trace.Tracer) hsapi.Options {
	return hsapi.Options{
		URL:              cfg.Headscale.URL,
		APIKey:           cfg.Headscale.APIKey.Reveal(),
		Timeout:          cfg.Headscale.HTTP.Timeout.D(),
		MaxResponseBytes: cfg.Headscale.MaxResponseBytes,
		Tracer:           tracer,
	}
}

// tsapiOptions maps the Tailscale config into tsapi.Options, selecting the
// configured authentication method. version stamps the outbound User-Agent.
func tsapiOptions(cfg *config.Config, version string) tsapi.Options {
	rts := cfg.ResolvedTailnets()
	if len(rts) == 0 {
		return tsapi.Options{}
	}
	return tsapiOptionsFor(rts[0], version)
}

// tsapiOptionsFor maps one resolved tailnet to tsapi client options. version
// stamps the outbound User-Agent ("tailscale2otel/<version>") so Tailscale-side
// request logs can attribute traffic to this exporter and its build (#66).
func tsapiOptionsFor(rt config.ResolvedTailnet, version string) tsapi.Options {
	o := tsapi.Options{
		Tailnet:             rt.Name,
		UserAgent:           serviceName + "/" + version,
		Timeout:             rt.HTTP.Timeout.D(),
		MaxAttempts:         rt.HTTP.Retry.MaxAttempts,
		BaseDelay:           rt.HTTP.Retry.BaseDelay.D(),
		MaxDelay:            rt.HTTP.Retry.MaxDelay.D(),
		RateLimit:           rt.HTTP.RateLimit,
		MaxResponseBytes:    rt.MaxResponseBytes,
		MaxLogResponseBytes: rt.MaxLogResponseBytes,
	}
	switch rt.Auth.Method {
	case "apikey":
		o.APIKey = rt.Auth.APIKey.Reveal()
	case "workload_identity":
		o.WorkloadIdentityClientID = rt.Auth.WorkloadIdentity.ClientID
		o.WorkloadIdentityIDTokenFile = rt.Auth.WorkloadIdentity.IDTokenFile
	default: // "oauth"
		o.OAuthClientID = rt.Auth.OAuth.ClientID
		o.OAuthClientSecret = rt.Auth.OAuth.ClientSecret.Reveal()
		o.OAuthScopes = rt.Auth.OAuth.Scopes
	}
	return o
}

// instanceFor derives a distinct service.instance.id per tailnet so each tailnet
// is its own OTLP target (resource attributes other than job/instance/service_*
// live only in target_info on Grafana Cloud, so a shared instance would collide
// series). Single-tailnet keeps the bare base instance for output continuity.
//
// piiTailnet is pii_filter.tailnet_name. When it is false, suppressing the
// tailscale.tailnet ATTRIBUTE is not enough (#356): the raw name was still
// embedded here, and service.instance.id is promoted to Grafana's `instance`
// label and appears in target_info and on every log/trace Resource — so
// disabling the category did not honor its contract. The tailnet must remain a
// distinct DIMENSION (otherwise two tailnets collide on one instance, the exact
// collision this function exists to prevent) while ceasing to be a NAME. Same
// treatment as instanceID gives a hostname: 12 hex chars of SHA-256, stable
// across restarts so the series is continuous, non-reversible so the name is not
// disclosed.
//
// Single-tailnet mode has no suffix and so nothing to leak; it is unaffected.
func instanceFor(base, tailnet string, multi, piiTailnet bool) string {
	if !multi {
		return base
	}
	label := tailnet
	if !piiTailnet {
		sum := sha256.Sum256([]byte(tailnet))
		label = hex.EncodeToString(sum[:])[:12]
	}
	if base == "" {
		return label
	}
	return base + "/" + label
}

// nodeMetricsOptions maps the node-metrics scraper config into
// nodemetrics.Options, translating each configured target. When discovery is
// enabled, cache (the tailnet's shared device cache, populated by the devices
// collector) is offered to the discoverer so it reuses that inventory instead
// of issuing its own DevicesRich() poll against the heaviest Tailscale endpoint
// (#85); a nil/empty cache transparently falls back to the API poll.
//
// tracer produces one client span per target scrape (#371). The span NAME is the
// fixed class "nodemetrics.scrape", never the target URL: targets are discovered
// dynamically and are therefore unbounded, so per-target detail belongs in an
// attribute rather than in the name.
func nodeMetricsOptions(nm config.NodeMetricsConfig, api nodeDiscoveryAPI, cache deviceCacheReader, logger *slog.Logger, tracer trace.Tracer) nodemetrics.Options {
	targets := make([]nodemetrics.Target, 0, len(nm.Targets))
	for _, t := range nm.Targets {
		var headers map[string]string
		if len(t.Headers) > 0 {
			headers = make(map[string]string, len(t.Headers))
			for k, v := range t.Headers {
				headers[k] = v.Reveal() // Secret -> raw string at the point of legitimate use (#73)
			}
		}
		nt := nodemetrics.Target{
			URL:             t.URL,
			Instance:        t.Instance,
			Labels:          t.Labels,
			BearerToken:     t.BearerToken.Reveal(),
			BearerTokenFile: t.BearerTokenFile,
			Headers:         headers,
		}
		if t.TLS != nil {
			nt.TLS = &nodemetrics.TLSClientConfig{
				InsecureSkipVerify: t.TLS.InsecureSkipVerify,
				CAFile:             t.TLS.CAFile,
				CertFile:           t.TLS.CertFile,
				KeyFile:            t.TLS.KeyFile,
				ServerName:         t.TLS.ServerName,
			}
		}
		targets = append(targets, nt)
	}
	opts := nodemetrics.Options{
		Targets:          targets,
		Interval:         nm.Interval.D(),
		Timeout:          nm.Timeout.D(),
		MaxResponseBytes: nm.MaxResponseBytes,
		MaxSamples:       nm.MaxSamples,
		// A target picks its own metric names and every unseen name creates an
		// instrument held for the process lifetime, which MaxSamples (a
		// per-scrape cap) does not bound (GHSA-gp33-6r5x-hw2f).
		MaxDistinctMetrics: nm.MaxDistinctMetrics,
		MetricAllow:        nm.MetricAllow,
		MetricDeny:         nm.MetricDeny,
		DropLabels:         nm.DropLabels,
		// Needed so a target whose custom TLS material fails to build is
		// reported rather than silently falling back (GHSA-2q4v-rrm9-966w).
		Logger: logger,
		Tracer: tracer,
	}
	// Dynamic discovery: poll the Tailscale device inventory on its own interval
	// and union the result with the static targets (handled by the collector).
	if nm.Discovery.Enabled {
		var dopts []nodeDiscovererOption
		if cache != nil {
			dopts = append(dopts, withDeviceCache(cache))
		}
		opts.Discoverer = newNodeDiscoverer(api, nm.Discovery, logger, dopts...)
		opts.DiscoveryInterval = nm.Discovery.Interval.D()
	}
	return opts
}
