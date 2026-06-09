package app

import (
	"encoding/base64"
	"log/slog"
	"maps"
	"os"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/hsapi"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

const serviceName = "tailscale2otel"

// telemetryOptions maps the OTLP config into telemetry.Options, computing the
// Grafana Cloud Basic-auth header when grafana_cloud credentials are set.
func telemetryOptions(cfg *config.Config, version string) telemetry.Options {
	headers := make(map[string]string, len(cfg.OTLP.Headers)+1)
	maps.Copy(headers, cfg.OTLP.Headers)
	if gc := cfg.OTLP.GrafanaCloud; gc.InstanceID != "" {
		headers["Authorization"] = "Basic " +
			base64.StdEncoding.EncodeToString([]byte(gc.InstanceID+":"+gc.Token.Reveal()))
	}
	prov := cfg.Provider
	if prov == "" {
		prov = "tailscale"
	}
	return telemetry.Options{
		ServiceName:       serviceName,
		ServiceVersion:    version,
		Provider:          prov,
		InstanceID:        instanceID(cfg),
		Protocol:          cfg.OTLP.Protocol,
		Endpoint:          cfg.OTLP.Endpoint,
		Headers:           headers,
		Insecure:          cfg.OTLP.TLS.Insecure,
		CAFile:            cfg.OTLP.TLS.CAFile,
		CertFile:          cfg.OTLP.TLS.CertFile,
		KeyFile:           cfg.OTLP.TLS.KeyFile,
		MetricInterval:    cfg.OTLP.MetricInterval.D(),
		SelfObsEnabled:    cfg.SelfObservability.Enabled,
		CardinalityLimit:  cfg.Cardinality.MetricLimit,
		TracingEnabled:    cfg.Tracing.Enabled,
		TraceSampler:      cfg.Tracing.Sampler,
		TraceSamplerArg:   cfg.Tracing.SamplerArg,
		PIIFilter:         piiCategories(cfg.PIIFilter),
		PrometheusEnabled: cfg.Prometheus.Enabled,
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

// instanceID resolves the service.instance.id resource attribute: the explicit
// self_observability.instance_id when set, otherwise the host name. The hostname
// policy lives here (the app layer) so internal/telemetry stays free of it; a
// failed os.Hostname() yields "", which buildResource simply omits.
func instanceID(cfg *config.Config) string {
	if cfg.SelfObservability.InstanceID != "" {
		return cfg.SelfObservability.InstanceID
	}
	host, _ := os.Hostname()
	return host
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
// configured authentication method.
func tsapiOptions(cfg *config.Config) tsapi.Options {
	rts := cfg.ResolvedTailnets()
	if len(rts) == 0 {
		return tsapi.Options{}
	}
	return tsapiOptionsFor(rts[0])
}

// tsapiOptionsFor maps one resolved tailnet to tsapi client options.
func tsapiOptionsFor(rt config.ResolvedTailnet) tsapi.Options {
	o := tsapi.Options{
		Tailnet:     rt.Name,
		Timeout:     rt.HTTP.Timeout.D(),
		MaxAttempts: rt.HTTP.Retry.MaxAttempts,
		BaseDelay:   rt.HTTP.Retry.BaseDelay.D(),
		MaxDelay:    rt.HTTP.Retry.MaxDelay.D(),
		RateLimit:   rt.HTTP.RateLimit,
	}
	if rt.Auth.Method == "apikey" {
		o.APIKey = rt.Auth.APIKey.Reveal()
	} else {
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
// nodemetrics.Options, translating each configured target.
func nodeMetricsOptions(nm config.NodeMetricsConfig, api nodeDiscoveryAPI, logger *slog.Logger) nodemetrics.Options {
	targets := make([]nodemetrics.Target, 0, len(nm.Targets))
	for _, t := range nm.Targets {
		nt := nodemetrics.Target{
			URL:             t.URL,
			Instance:        t.Instance,
			Labels:          t.Labels,
			BearerToken:     t.BearerToken.Reveal(),
			BearerTokenFile: t.BearerTokenFile,
			Headers:         t.Headers,
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
		opts.Discoverer = newNodeDiscoverer(api, nm.Discovery, logger)
		opts.DiscoveryInterval = nm.Discovery.Interval.D()
	}
	return opts
}
