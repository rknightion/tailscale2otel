package config

import "time"

// dur is a small helper to express a Duration default from a time.Duration.
func dur(d time.Duration) Duration { return Duration(d) }

// Default returns a Config populated with the documented default values. Load
// starts from Default and unmarshals the user's YAML on top, so any key the
// user omits keeps its default.
func Default() *Config {
	return &Config{
		LogLevel: "info",
		Tailscale: TailscaleConfig{
			Tailnet: "example.com",
			Auth: TailscaleAuth{
				Method: "oauth",
				OAuth: OAuthConfig{
					Scopes: []string{"all:read"},
				},
			},
			HTTP: TailscaleHTTPConfig{
				Timeout: dur(30 * time.Second),
				Retry: RetryConfig{
					MaxAttempts: 4,
					BaseDelay:   dur(500 * time.Millisecond),
					MaxDelay:    dur(10 * time.Second),
				},
			},
		},
		OTLP: OTLPConfig{
			Protocol: "http",
			Endpoint: "https://otlp-gateway-prod-us-central-0.grafana.net/otlp",
			Headers:  map[string]string{},
			TLS:      TLSConfig{Insecure: false},
			// 60s aligns the OTLP push cadence with the default collector scrape
			// interval (1 data-point-per-minute), avoiding Grafana Cloud DPM churn.
			MetricInterval: dur(60 * time.Second),
		},
		Enrichment: EnrichmentConfig{
			CacheTTL: dur(5 * time.Minute),
			ReverseDNS: ReverseDNSConfig{
				Enabled:     false,
				Timeout:     dur(2 * time.Second),
				CacheTTL:    dur(1 * time.Hour),
				NegativeTTL: dur(5 * time.Minute),
				MaxEntries:  4096,
			},
		},
		Cardinality: CardinalityConfig{
			FlowMetricsMode:        "rollup",
			FlowIncludePorts:       false,
			FlowSourcePort:         false,
			FlowDestinationPort:    false,
			FlowDestinationService: false,
			FlowNodeDims:           true,
			CollapseExternal:       true,
			DevicePerEntity:        true,
			UserPerEntity:          true,
			KeyPerEntity:           true,
			MetricLimit:            10000,
		},
		Collectors: Collectors{
			Devices: CollectorConfig{
				Enabled:        true,
				Interval:       dur(60 * time.Second),
				CollectRoutes:  false,
				CollectPosture: false,
				PostureLogMode: "changes",
			},
			Flowlogs: CollectorConfig{
				Enabled:         true,
				Source:          "poll",
				Interval:        dur(60 * time.Second),
				Lag:             dur(120 * time.Second),
				InitialLookback: dur(5 * time.Minute),
				MaxWindow:       dur(1 * time.Hour),
				LogMode:         "per_connection",
				FlowRollupTopN:  500,
			},
			Auditlogs: CollectorConfig{
				Enabled:         true,
				Source:          "poll",
				Interval:        dur(60 * time.Second),
				Lag:             dur(60 * time.Second),
				InitialLookback: dur(5 * time.Minute),
				MaxWindow:       dur(6 * time.Hour),
			},
			Users: CollectorConfig{
				Enabled:  true,
				Interval: dur(300 * time.Second),
			},
			Keys: CollectorConfig{
				Enabled:    true,
				Interval:   dur(300 * time.Second),
				ExpiryWarn: dur(168 * time.Hour),
			},
			Settings: CollectorConfig{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Acl: CollectorConfig{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Dns: CollectorConfig{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			NodeMetrics: NodeMetricsConfig{
				Enabled:          false,
				Interval:         dur(60 * time.Second),
				Timeout:          dur(10 * time.Second),
				MaxResponseBytes: 4 * 1024 * 1024,
				MaxSamples:       50000,
				Discovery: NodeMetricsDiscovery{
					Enabled:           false,
					Interval:          dur(5 * time.Minute),
					MaxTargets:        1000,
					Scheme:            "http",
					Port:              5252,
					Path:              "/metrics",
					OnlineOnly:        true,
					ExcludeExternal:   true,
					AddressOrder:      "ipv4",
					InstanceSource:    "address",
					IncludeHostLabels: true,
					IncludeTagsLabel:  true,
				},
			},
		},
		Checkpoint: CheckpointConfig{
			Store:    "memory",
			FilePath: "/var/lib/tailscale2otel/checkpoints.json",
		},
		Streaming: StreamingConfig{
			Enabled:       false,
			Listen:        ":8088",
			Path:          "/services/collector/event",
			Decompress:    "auto",
			AutoConfigure: false,
		},
		Webhook: WebhookConfig{
			Enabled:   false,
			Listen:    ":8089",
			Path:      "/tailscale/webhook",
			Tolerance: dur(5 * time.Minute),
		},
		SelfObservability: SelfObservabilityConfig{
			Enabled: true,
		},
		Admin: AdminConfig{
			Enabled:     false,
			Listen:      ":9090",
			LandingPage: true,
		},
		Profiling: ProfilingConfig{
			Pyroscope: ProfilingPyroscope{
				UploadRate: dur(15 * time.Second),
			},
		},
	}
}
