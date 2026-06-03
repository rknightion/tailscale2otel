package app

import (
	"encoding/base64"
	"maps"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
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
			base64.StdEncoding.EncodeToString([]byte(gc.InstanceID+":"+gc.Token))
	}
	return telemetry.Options{
		ServiceName:    serviceName,
		ServiceVersion: version,
		Protocol:       cfg.OTLP.Protocol,
		Endpoint:       cfg.OTLP.Endpoint,
		Headers:        headers,
		Insecure:       cfg.OTLP.TLS.Insecure,
		CAFile:         cfg.OTLP.TLS.CAFile,
		CertFile:       cfg.OTLP.TLS.CertFile,
		KeyFile:        cfg.OTLP.TLS.KeyFile,
		MetricInterval: cfg.OTLP.MetricInterval.D(),
	}
}

// tsapiOptions maps the Tailscale config into tsapi.Options, selecting the
// configured authentication method.
func tsapiOptions(cfg *config.Config) tsapi.Options {
	o := tsapi.Options{
		Tailnet:     cfg.Tailscale.Tailnet,
		Timeout:     cfg.Tailscale.HTTP.Timeout.D(),
		MaxAttempts: cfg.Tailscale.HTTP.Retry.MaxAttempts,
		BaseDelay:   cfg.Tailscale.HTTP.Retry.BaseDelay.D(),
		MaxDelay:    cfg.Tailscale.HTTP.Retry.MaxDelay.D(),
		RateLimit:   cfg.Tailscale.HTTP.RateLimit,
	}
	if cfg.Tailscale.Auth.Method == "apikey" {
		o.APIKey = cfg.Tailscale.Auth.APIKey
	} else {
		o.OAuthClientID = cfg.Tailscale.Auth.OAuth.ClientID
		o.OAuthClientSecret = cfg.Tailscale.Auth.OAuth.ClientSecret
		o.OAuthScopes = cfg.Tailscale.Auth.OAuth.Scopes
	}
	return o
}

// nodeMetricsOptions maps the node-metrics scraper config into
// nodemetrics.Options, translating each configured target.
func nodeMetricsOptions(nm config.NodeMetricsConfig) nodemetrics.Options {
	targets := make([]nodemetrics.Target, 0, len(nm.Targets))
	for _, t := range nm.Targets {
		nt := nodemetrics.Target{
			URL:             t.URL,
			Instance:        t.Instance,
			Labels:          t.Labels,
			BearerToken:     t.BearerToken,
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
	return nodemetrics.Options{
		Targets:  targets,
		Interval: nm.Interval.D(),
		Timeout:  nm.Timeout.D(),
	}
}
