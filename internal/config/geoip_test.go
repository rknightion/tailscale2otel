package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// geoipCfg returns a valid baseline config with GeoIP enabled from local files,
// so each test below can change exactly the one thing it is about.
func geoipCfg(t *testing.T) *Config {
	t.Helper()
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "tskey-api-test"
	c.Enrichment.GeoIP.Enabled = true
	c.Enrichment.GeoIP.CountryDatabase = filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
	return c
}

func TestGeoIPDefaults(t *testing.T) {
	g := Default().Enrichment.GeoIP
	if g.Enabled {
		t.Error("geoip is enabled by default; it must be opt-in")
	}
	if g.Download.Enabled {
		t.Error("geoip download is enabled by default; it must be opt-in")
	}
	if g.ReloadInterval.D() != 6*time.Hour {
		t.Errorf("reload_interval = %v, want 6h", g.ReloadInterval.D())
	}
	if g.Download.Interval.D() != 24*time.Hour {
		t.Errorf("download.interval = %v, want 24h", g.Download.Interval.D())
	}
	if g.Download.Timeout.D() != 5*time.Minute {
		t.Errorf("download.timeout = %v, want 5m", g.Download.Timeout.D())
	}
	want := []string{"GeoLite2-Country", "GeoLite2-ASN"}
	if strings.Join(g.Download.Editions, ",") != strings.Join(want, ",") {
		t.Errorf("download.editions = %v, want %v", g.Download.Editions, want)
	}
	if g.Download.Endpoint == "" || !strings.HasPrefix(g.Download.Endpoint, "https://") {
		t.Errorf("download.endpoint = %q, want an https MaxMind URL", g.Download.Endpoint)
	}
	if Default().Cardinality.Flow.GeoDims {
		t.Error("cardinality.flow.geo_dims is on by default; country labels on raw flow metrics must be opt-in")
	}
}

func TestGeoIPValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // substring; empty means the config must validate
	}{
		{
			name:   "local files only",
			mutate: func(*Config) {},
		},
		{
			name: "disabled config is never checked",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP = GeoIPConfig{Enabled: false, ReloadInterval: dur(-1)}
			},
		},
		{
			// Enabling geoip with no database and no downloader would start,
			// emit nothing, and look like a broken feature forever.
			name: "enabled with no source at all",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.CountryDatabase = ""
			},
			wantErr: "country_database",
		},
		{
			name: "asn database alone is a valid source",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.CountryDatabase = ""
				c.Enrichment.GeoIP.ASNDatabase = filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb")
			},
		},
		{
			name: "download without an account id",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
			},
			wantErr: "account_id",
		},
		{
			name: "download without a license key",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
			},
			wantErr: "license_key",
		},
		{
			name: "download with credentials",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
			},
		},
		{
			name: "download with no editions",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Editions = nil
			},
			wantErr: "editions",
		},
		{
			// The edition name is interpolated into a URL path AND a filename,
			// so anything that could escape either is refused up front rather
			// than sanitized into something the operator did not ask for.
			name: "download with a traversal edition name",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Editions = []string{"../../etc/passwd"}
			},
			wantErr: "editions",
		},
		{
			name: "download with a non-absolute endpoint",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Endpoint = "download.maxmind.com"
			},
			wantErr: "endpoint",
		},
		{
			name: "negative reload interval",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.ReloadInterval = dur(-time.Second)
			},
			wantErr: "reload_interval",
		},
		{
			name: "non-positive download interval",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Interval = dur(0)
			},
			wantErr: "download.interval",
		},
		{
			name: "non-positive download timeout",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Timeout = dur(0)
			},
			wantErr: "download.timeout",
		},
		{
			// geo_dims without geoip enabled emits nothing; saying so beats
			// letting an operator wonder why the label never appears.
			name: "geo_dims without geoip",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Enabled = false
				c.Enrichment.GeoIP.CountryDatabase = ""
				c.Cardinality.Flow.GeoDims = true
			},
			wantErr: "geo_dims",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := geoipCfg(t)
			tc.mutate(c)
			err := c.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// Enabling the downloader with no explicit database paths must default them to
// where the downloader installs the files. Otherwise the obvious minimal config
// -- credentials and nothing else -- downloads databases and then never loads
// them, which is the worst kind of silent no-op.
func TestGeoIPDownloadDefaultsThePaths(t *testing.T) {
	c := geoipCfg(t)
	dir := t.TempDir()
	c.Enrichment.GeoIP.CountryDatabase = ""
	c.Enrichment.GeoIP.Download.Enabled = true
	c.Enrichment.GeoIP.Download.AccountID = "359153"
	c.Enrichment.GeoIP.Download.LicenseKey = "k"
	c.Enrichment.GeoIP.Download.Directory = dir

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := c.Enrichment.GeoIP.CountryDatabase, filepath.Join(dir, "GeoLite2-Country.mmdb"); got != want {
		t.Errorf("country_database = %q, want it defaulted to %q", got, want)
	}
	if got, want := c.Enrichment.GeoIP.ASNDatabase, filepath.Join(dir, "GeoLite2-ASN.mmdb"); got != want {
		t.Errorf("asn_database = %q, want it defaulted to %q", got, want)
	}
}

// An explicit path always wins over the derived one, and an edition that is not
// requested does not get a path invented for it.
func TestGeoIPDownloadDoesNotOverrideExplicitPaths(t *testing.T) {
	c := geoipCfg(t)
	explicit := filepath.Join(t.TempDir(), "mine.mmdb")
	c.Enrichment.GeoIP.CountryDatabase = explicit
	c.Enrichment.GeoIP.Download.Enabled = true
	c.Enrichment.GeoIP.Download.AccountID = "359153"
	c.Enrichment.GeoIP.Download.LicenseKey = "k"
	c.Enrichment.GeoIP.Download.Directory = t.TempDir()
	c.Enrichment.GeoIP.Download.Editions = []string{"GeoLite2-Country"}

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Enrichment.GeoIP.CountryDatabase != explicit {
		t.Errorf("country_database = %q, want the explicit %q", c.Enrichment.GeoIP.CountryDatabase, explicit)
	}
	if c.Enrichment.GeoIP.ASNDatabase != "" {
		t.Errorf("asn_database = %q, want empty (GeoLite2-ASN was not requested)", c.Enrichment.GeoIP.ASNDatabase)
	}
}

func TestGeoIPWarnings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string // substring the advisory must contain; empty means none
	}{
		{
			// The expensive combination: raw per-connection metrics, external
			// addresses collapsed into one bucket, and a country label that
			// splits that bucket ~250 ways.
			name: "geo dims on raw metrics with collapse_external",
			mutate: func(c *Config) {
				c.Cardinality.Flow.GeoDims = true
				c.Cardinality.Flow.MetricsMode = "all"
				c.Cardinality.Flow.CollapseExternal = true
			},
			want: "geo_dims",
		},
		{
			name: "acknowledged",
			mutate: func(c *Config) {
				c.Cardinality.Flow.GeoDims = true
				c.Cardinality.Flow.MetricsMode = "all"
				c.Cardinality.Flow.CollapseExternal = true
				c.Enrichment.GeoIP.AcknowledgeCardinality = true
			},
		},
		{
			// Rollup mode is top-N bounded whatever the key carries, so the
			// advisory must NOT fire there or it is just noise on the default
			// configuration.
			name: "geo dims on the rollup family only",
			mutate: func(c *Config) {
				c.Cardinality.Flow.GeoDims = true
				c.Cardinality.Flow.MetricsMode = "rollup"
			},
		},
		{
			name: "http download endpoint",
			mutate: func(c *Config) {
				c.Enrichment.GeoIP.Download.Enabled = true
				c.Enrichment.GeoIP.Download.AccountID = "359153"
				c.Enrichment.GeoIP.Download.LicenseKey = "k"
				c.Enrichment.GeoIP.Download.Endpoint = "http://mirror.internal/geoip"
			},
			want: "http://",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := geoipCfg(t)
			tc.mutate(c)
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			joined := strings.Join(c.Warnings(), "\n")
			hasGeo := strings.Contains(joined, "geoip") || strings.Contains(joined, "geo_dims")
			switch {
			case tc.want == "" && hasGeo:
				t.Fatalf("unexpected geoip advisory: %s", joined)
			case tc.want != "" && !strings.Contains(joined, tc.want):
				t.Fatalf("advisories = %q, want one mentioning %q", joined, tc.want)
			}
		})
	}
}

// The license key is a Secret, so it must redact itself and must be settable
// from a file like every other credential in this config.
func TestGeoIPLicenseKeySecretHandling(t *testing.T) {
	key := Secret("not-a-real-license-key")
	if got := key.String(); strings.Contains(got, "not-a-real-key") {
		t.Errorf("Secret.String() = %q, want it redacted", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "maxmind.key")
	if err := os.WriteFile(path, []byte("  file-sourced-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := geoipCfg(t)
	c.Enrichment.GeoIP.Download.LicenseKeyFile = path
	if err := c.resolveSecretFiles(); err != nil {
		t.Fatalf("resolveSecretFiles: %v", err)
	}
	if got := c.Enrichment.GeoIP.Download.LicenseKey.Reveal(); got != "file-sourced-key" {
		t.Errorf("license_key = %q, want the trimmed file contents", got)
	}

	// Setting both is the documented conflict and must be reported, not
	// silently resolved one way or the other.
	c2 := geoipCfg(t)
	c2.Enrichment.GeoIP.Download.Enabled = true
	c2.Enrichment.GeoIP.Download.AccountID = "359153"
	c2.Enrichment.GeoIP.Download.LicenseKey = "inline"
	c2.Enrichment.GeoIP.Download.LicenseKeyFile = path
	if err := c2.resolveSecretFiles(); err != nil {
		t.Fatalf("resolveSecretFiles: %v", err)
	}
	err := c2.Validate()
	if err == nil || !strings.Contains(err.Error(), "license_key") {
		t.Fatalf("Validate() = %v, want a conflict error naming license_key", err)
	}
}
