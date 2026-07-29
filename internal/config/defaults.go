package config

import (
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/geoip"
)

// dur is a small helper to express a Duration default from a time.Duration.
func dur(d time.Duration) Duration { return Duration(d) }

// defaultObjectStore is the tuning baseline shared by every object-store
// destination, whatever signal reads it. The two signals differ only in WHICH
// export they point at, never in how hard one cycle is allowed to work, so both
// default blocks come from here rather than being written out twice and drifting
// (TestAuditObjectStoreBoundsDefaults enforces that they stay equal).
//
// Credentials and destination identity are deliberately absent: an unset
// destination must stay unset so validation can tell "not configured" from
// "configured wrong".
func defaultObjectStore() ObjectStoreConfig {
	return ObjectStoreConfig{
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
	}
}

// Default returns a Config populated with the documented default values. Load
// starts from Default and unmarshals the user's YAML on top, so any key the
// user omits keeps its default.
func Default() *Config {
	return &Config{
		LogFormat: "text",
		LogLevel:  "info",
		Provider:  "tailscale",
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
			// Generous enough to be a no-op for every record this exporter
			// normally produces, so the bound only engages on a genuinely
			// pathological record rather than quietly reshaping normal output.
			Limits: OTLPLimits{
				LogBodyBytes:           32 * 1024,
				LogAttributeValueBytes: 4 * 1024,
			},
			// The reloader is built whenever a watched file is configured, so
			// last-known-good is always enforced; this only decides whether a
			// background poller looks for rotations. 30s is well inside a
			// Kubernetes secret-projection refresh and costs a few stats.
			CredentialReload: CredentialReloadConfig{Interval: dur(30 * time.Second)},
			// Stated explicitly rather than left zero, which is what #360 means by
			// deterministic: these ARE the pinned exporters' own defaults
			// (retry on, 5s/30s/1m), so declaring them changes no behavior while
			// making the effective policy visible in config.example.yaml, in the
			// generated schema, and in the status page's effective-settings echo.
			Retry: OTLPRetryConfig{
				Enabled:         boolPtr(true),
				InitialInterval: dur(5 * time.Second),
				MaxInterval:     dur(30 * time.Second),
				MaxElapsedTime:  dur(time.Minute),
			},
			// Matches the built-in stdout cadence; stated for the same reason.
			Stdout: OTLPStdoutConfig{MetricInterval: dur(5 * time.Second)},
		},
		Enrichment: EnrichmentConfig{
			CacheTTL: dur(5 * time.Minute),
			ReverseDNS: ReverseDNSConfig{
				Enabled:     false,
				Timeout:     dur(2 * time.Second),
				CacheTTL:    dur(24 * time.Hour),
				NegativeTTL: dur(5 * time.Minute),
				// One hour of stale serving against a 24h TTL: long enough that a
				// refresh (or several retries of one) always lands inside it, short
				// enough that a genuinely changed PTR is picked up the same day.
				StaleTTL:   dur(time.Hour),
				MaxEntries: 50000,
			},
			GeoIP: GeoIPConfig{
				Enabled: false,
				// Two independent clocks, both deliberately chosen: 6h notices a
				// file an external updater rewrote (cheap — one stat per file),
				// 24h asks MaxMind whether it has published a newer build.
				// GeoLite2 rebuilds twice a week, and each check is a conditional
				// request that costs a 304 when nothing changed.
				ReloadInterval: dur(6 * time.Hour),
				Download: GeoIPDownloadConfig{
					Enabled:  false,
					Editions: []string{"GeoLite2-Country", "GeoLite2-ASN"},
					Interval: dur(24 * time.Hour),
					// Generous: the ASN database is ~6 MB compressed and this
					// runs on a background goroutine where a slow link costs
					// nothing but a later retry.
					Timeout:  dur(5 * time.Minute),
					Endpoint: geoip.DefaultEndpoint,
				},
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
				ObjectStore:        defaultObjectStore(),
			},
			Auditlogs: AuditlogsCollector{
				Enabled:         true,
				Source:          "poll",
				Interval:        dur(60 * time.Second),
				Lag:             dur(60 * time.Second),
				InitialLookback: dur(5 * time.Minute),
				MaxWindow:       dur(6 * time.Hour),
				ObjectStore:     defaultObjectStore(),
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
			FilePath: LegacyCheckpointPath,
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
			// Per-class overrides are unset by default, so every class inherits
			// Sampler/SamplerArg — the single global sampler this repo has always
			// had. "trust" is likewise today's behavior: an inbound traceparent's
			// sampled bit is honored.
			RemoteParent: "trust",
		},
		Admin: AdminConfig{
			Enabled: true,
			// Loopback, NOT the wildcard ":9091" this used to be (#314). The landing
			// page is on by default and refuses every request on a network-reachable
			// bind with no token, so a wildcard default served 403 to the one surface
			// a new operator is most likely to open — the exporter's own UI, broken
			// under the exporter's own defaults.
			//
			// Anything that genuinely needs to be reached from another host sets a
			// bind explicitly: the Helm chart already pins ":9091" in values.yaml, and
			// Compose maps no admin port at all. Container probes are unaffected
			// either way — /healthz and /readyz are never gated, and the -healthcheck
			// subcommand runs inside the container where loopback is the right answer.
			Listen:                "127.0.0.1:9091",
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
			// "default" reproduces today's hardcoded per-bucket/ring limits exactly
			// (#329) — safe defaults stay unchanged unless an operator opts in.
			CapacityProfile: flowstore.ProfileDefault,
			// Store.Path defaults to "" (memory-only, the persistent backend is
			// never built). These sub-defaults only take effect once an operator
			// sets a path, and MUST equal sqlitestore's Default* constants
			// (internal/flowstore/sqlitestore/store.go) — hand-copied rather than
			// imported so internal/config does not pull in the sqlite driver just
			// to read seven numbers.
			Store: FlowsStoreConfig{
				Retention:     dur(30 * 24 * time.Hour), // sqlitestore.DefaultRetention
				MaxRows:       5_000_000,                // sqlitestore.DefaultMaxRows
				MaxExportRows: 50_000,                   // sqlitestore.DefaultMaxExportRows
				QueueSize:     8192,                     // sqlitestore.DefaultQueueSize
				BatchSize:     512,                      // sqlitestore.DefaultBatchSize
				FlushInterval: dur(5 * time.Second),     // sqlitestore.DefaultFlushInterval
				QueryTimeout:  dur(15 * time.Second),    // sqlitestore.DefaultQueryTimeout
				SweepInterval: dur(time.Hour),           // sqlitestore.DefaultSweepInterval
			},
		},
		Events: EventsConfig{
			Enabled: true,
			// 5000 individual audit+webhook events. Unlike flows.retention this is
			// a plain event count, not a time span: a busy tailnet's audit log can
			// run to hundreds of events a day, so this comfortably covers a
			// multi-day window without the unbounded growth a per-event ring
			// would otherwise have.
			MaxEvents: 5000,
		},
		Prometheus: PrometheusConfig{
			Enabled: false,
			Listen:  ":2112",
		},
		Profiling: ProfilingConfig{
			Pyroscope: ProfilingPyroscope{
				UploadRate: dur(60 * time.Second),
				// Off by default: correlation is useful but it wraps the
				// TracerProvider and puts pprof labels on every span.
				SpanProfiles: SpanProfilesConfig{Enabled: false},
				// Off: a tailnet name is a customer identifier and profiles are a
				// separate destination from the metric/log pipeline.
				TailnetLabel:     "off",
				CredentialReload: CredentialReloadConfig{Interval: dur(30 * time.Second)},
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

// boolPtr returns a pointer to b, for the *bool config fields where an
// explicitly-set false must be distinguishable from an omitted key.
func boolPtr(b bool) *bool { return &b }
