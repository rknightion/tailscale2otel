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
			Tailnet: "-", // the authenticated principal's default tailnet (works out of the box for single-tailnet OAuth)
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
				CacheTTL:    dur(24 * time.Hour),
				NegativeTTL: dur(5 * time.Minute),
				MaxEntries:  50000,
			},
		},
		Cardinality: CardinalityConfig{
			MetricLimit:      10000,
			DerpRegionRollup: true,
			Flow: FlowCardinality{
				MetricsMode:        "rollup",
				RollupTopN:         500,
				SourcePort:         false,
				DestinationPort:    false,
				DestinationService: false,
				NodeDims:           true,
				CollapseExternal:   true,
			},
			PerEntity: PerEntityCardinality{
				Device:  true,
				User:    true,
				Key:     true,
				Webhook: true,
				Service: true,
			},
		},
		Collectors: Collectors{
			Devices: DevicesCollector{
				Enabled:              true,
				Interval:             dur(60 * time.Second),
				CollectRoutes:        false,
				CollectPosture:       false,
				CollectDeviceInvites: true,
				PostureLogMode:       "changes",
				// Opt-out default: once collect_posture is on, the integration
				// namespaces plus ip are promoted to attribute metrics. node is
				// covered by the curated posture gauge; custom is excluded (unbounded).
				AttributeNamespaces: []string{"intune", "jamf", "kandji", "crowdstrike", "sentinelone", "kolide", "ip"},
				CollectTagRollup:    true,
				TagRollupLimit:      50,
			},
			Flowlogs: FlowlogsCollector{
				Enabled:         true,
				Source:          "poll",
				Interval:        dur(60 * time.Second),
				Lag:             dur(120 * time.Second),
				InitialLookback: dur(5 * time.Minute),
				MaxWindow:       dur(1 * time.Hour),
				LogMode:         "per_connection",
			},
			Auditlogs: AuditlogsCollector{
				Enabled:         true,
				Source:          "poll",
				Interval:        dur(60 * time.Second),
				Lag:             dur(60 * time.Second),
				InitialLookback: dur(5 * time.Minute),
				MaxWindow:       dur(6 * time.Hour),
			},
			Users: SimpleCollector{
				Enabled:  true,
				Interval: dur(300 * time.Second),
			},
			Keys: KeysCollector{
				Enabled:    true,
				Interval:   dur(300 * time.Second),
				ExpiryWarn: dur(168 * time.Hour),
			},
			Settings: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Acl: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Dns: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Contacts: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Webhooks: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			PostureIntegrations: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			LogStream: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Services: ServicesCollector{
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
					InstanceSource:    "name", // MagicDNS short name: unique per tailnet AND human-friendly
					IncludeHostLabels: true,
					IncludeTagsLabel:  true,
				},
			},
		},
		Checkpoint: CheckpointConfig{
			Store:    "file", // persist window cursors across restarts; falls back to memory + WARN if the path is not writable
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
				UploadRate: dur(60 * time.Second),
			},
		},
	}
}
