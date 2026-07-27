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
		Provider: "tailscale",
		Headscale: HeadscaleConfig{
			HTTP:             TailscaleHTTPConfig{Timeout: dur(30 * time.Second)},
			MaxResponseBytes: 4 << 20, // 4 MiB — snapshot endpoints only; ~5,800 nodes at ~715 B each
		},
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
			// Response decode budgets (#474). 4 MiB covers ~2,400 devices on the
			// heaviest snapshot endpoint; 32 MiB covers ~12,000 flow-log records in
			// one poll window. Both are far below the 256 MiB memory limit the Helm
			// chart ships by default. See internal/tsapi/limit.go.
			MaxResponseBytes:    4 << 20,
			MaxLogResponseBytes: 32 << 20,
		},
		OTLP: OTLPConfig{
			Protocol: "http",
			Endpoint: "https://otlp-gateway-prod-us-central-0.grafana.net/otlp",
			Headers:  map[string]Secret{},
			TLS:      TLSConfig{Insecure: false},
			// 60s aligns the OTLP push cadence with the default collector scrape
			// interval (1 data-point-per-minute), avoiding Grafana Cloud DPM churn.
			MetricInterval:        dur(60 * time.Second),
			MetricExportBatchSize: 10000,
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
			MetricLimit:         10000,
			DerpRegionRollup:    true,
			SubnetRouteRollup:   true,
			WarningThreshold:    2000,
			CriticalThreshold:   8000,
			LabelValueSampleCap: 100,
			Flow: FlowCardinality{
				MetricsMode:         "rollup",
				RollupTopN:          500,
				SourcePort:          false,
				DestinationPort:     false,
				NodeDims:            true,
				CollapseExternal:    true,
				ExitNodeAttribution: true,
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
				CollectConnectivity:  true,
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
				ReplayOverlap:   dur(5 * time.Minute),
				// A busy tailnet can return many connection identities inside a
				// five-minute replay window. Keep the durable guard bounded but
				// comfortably above the normal per-window volume.
				ReplaySeenCapacity: 131072,
				LogMode:            "per_connection",
				ObjectStore: ObjectStoreConfig{
					Layout:                     ObjectStoreLayoutPartitioned,
					Interval:                   dur(60 * time.Second),
					Lookback:                   dur(1 * time.Hour),
					InitialLookback:            dur(6 * time.Hour),
					MaxObjects:                 200,
					MaxObjectWireBytes:         64 << 20,
					MaxObjectDecompressedBytes: 32 << 20,
					MaxObjectRecords:           100_000,
					MaxCycleWireBytes:          512 << 20,
					MaxCycleDecompressedBytes:  256 << 20,
					MaxCycleRecords:            500_000,
				},
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
			Acl: AclCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
				Validate: true,
			},
			Dns: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Contacts: SimpleCollector{
				Enabled:  true,
				Interval: dur(600 * time.Second),
			},
			Webhooks: WebhooksCollector{
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
			OAuthApps: SimpleCollector{
				Enabled:  true,
				Interval: dur(300 * time.Second),
			},
			NodeMetrics: NodeMetricsConfig{
				Enabled:          false,
				Interval:         dur(60 * time.Second),
				Timeout:          dur(10 * time.Second),
				MaxResponseBytes: 4 * 1024 * 1024,
				MaxSamples:       50000,
				// Distinct forwarded metric NAMES are node-controlled and each one
				// costs a permanent instrument, so they get their own budget.
				MaxDistinctMetrics: 2000,
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
		IngressWAL: IngressWALConfig{
			Enabled:    false,
			Directory:  "/var/lib/tailscale2otel/ingress-wal",
			MaxBytes:   268435456,
			MaxEntries: 10000,
			Corruption: "fail",
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
		PIIFilter: PIIFilterConfig{
			Emails: true, UserDisplayNames: true, UserIDs: true, Hostnames: true, NodeIDs: true,
			TailscaleIPs: true, InternalIPs: true, ExternalIPs: true, ServiceAddrs: true,
			EndpointPaths: true, NetworkTopology: true, TailnetName: true, FreeTextDetails: true,
		},
		Tracing: TracingConfig{
			Enabled:    false,
			Sampler:    "parentbased_always_on",
			SamplerArg: 1.0,
		},
		Admin: AdminConfig{
			Enabled:               true,
			Listen:                ":9091",
			LandingPage:           true,
			StatusRefreshInterval: dur(5 * time.Second),
		},
		Flows: FlowsConfig{
			Enabled:       true,
			MaxFutureSkew: dur(5 * time.Minute),
			// Six hours of one-minute buckets (360). Long enough to cover a shift and
			// see a diurnal shape, short enough that the ring stays small on a real
			// tailnet.
			Retention: dur(6 * time.Hour),
		},
		Prometheus: PrometheusConfig{
			Enabled: false,
			Listen:  ":2112",
		},
		Profiling: ProfilingConfig{
			Pyroscope: ProfilingPyroscope{
				UploadRate: dur(60 * time.Second),
			},
			// Contention profiling on by default (applied only when pprof or
			// Pyroscope is enabled — see startProfiling). Fraction 5 samples 1/5 of
			// mutex-contention events; block rate 100µs records blocking events
			// averaging at least that long. Set either to 0 to drop that profile.
			MutexProfileFraction: 5,
			BlockProfileRate:     100_000,
		},
		VersionChecks: VersionChecksConfig{
			Self:     VersionCheckSelf{Enabled: true},
			Devices:  VersionCheckDevices{Enabled: true, OutdatedMinorThreshold: 3},
			CacheTTL: dur(time.Hour),
			Timeout:  dur(10 * time.Second),
		},
	}
}
