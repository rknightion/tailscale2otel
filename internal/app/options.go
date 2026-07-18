package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/hsapi"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
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
		SelfObsEnabled:           cfg.SelfObservability.Enabled,
		CardinalityLimit:         cfg.Cardinality.MetricLimit,
		CardinalityLabelValueCap: cfg.Cardinality.LabelValueSampleCap,
		TracingEnabled:           cfg.Tracing.Enabled,
		TraceSampler:             cfg.Tracing.Sampler,
		TraceSamplerArg:          cfg.Tracing.SamplerArg,
		PIIFilter:                piiCategories(cfg.PIIFilter),
		PrometheusEnabled:        cfg.Prometheus.Enabled,
	}
}

// piiCategories converts config.PIIFilterConfig into the pii.Categories map used
// by the redactor. Every category is explicitly mapped; the redactor treats an
// absent key as enabled, but we emit all 13 so the config layer's defaults (all
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
func hsapiOptions(cfg *config.Config) hsapi.Options {
	return hsapi.Options{
		URL:     cfg.Headscale.URL,
		APIKey:  cfg.Headscale.APIKey.Reveal(),
		Timeout: cfg.Headscale.HTTP.Timeout.D(),
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
		Tailnet:     rt.Name,
		UserAgent:   serviceName + "/" + version,
		Timeout:     rt.HTTP.Timeout.D(),
		MaxAttempts: rt.HTTP.Retry.MaxAttempts,
		BaseDelay:   rt.HTTP.Retry.BaseDelay.D(),
		MaxDelay:    rt.HTTP.Retry.MaxDelay.D(),
		RateLimit:   rt.HTTP.RateLimit,
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
func instanceFor(base, tailnet string, multi bool) string {
	if !multi {
		return base
	}
	if base == "" {
		return tailnet
	}
	return base + "/" + tailnet
}

// nodeMetricsOptions maps the node-metrics scraper config into
// nodemetrics.Options, translating each configured target. When discovery is
// enabled, cache (the tailnet's shared device cache, populated by the devices
// collector) is offered to the discoverer so it reuses that inventory instead
// of issuing its own DevicesRich() poll against the heaviest Tailscale endpoint
// (#85); a nil/empty cache transparently falls back to the API poll.
func nodeMetricsOptions(nm config.NodeMetricsConfig, api nodeDiscoveryAPI, cache deviceCacheReader, logger *slog.Logger) nodemetrics.Options {
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
		MetricAllow:      nm.MetricAllow,
		MetricDeny:       nm.MetricDeny,
		DropLabels:       nm.DropLabels,
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
