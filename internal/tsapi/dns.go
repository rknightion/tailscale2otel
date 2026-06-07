package tsapi

import (
	"context"
	"path"
)

// DNSConfig is the unified DNS configuration returned by
// GET /api/v2/tailnet/{tailnet}/dns/configuration. It is a strict superset of
// the four former split DNS GETs (nameservers/searchpaths/split-dns/preferences)
// and additionally carries preferences.overrideLocalDNS and a per-resolver
// useWithExitNode flag (Tailscale v1.88.1+).
type DNSConfig struct {
	// Nameservers are the global DNS resolvers.
	Nameservers []DNSResolver
	// SplitDNS maps a DNS name suffix (domain) to its resolvers. A domain whose
	// wire value is null decodes to a present key with an empty slice.
	SplitDNS map[string][]DNSResolver
	// SearchPaths are the configured search domains.
	SearchPaths []string
	// OverrideLocalDNS reports whether Nameservers override the local OS DNS.
	OverrideLocalDNS bool
	// MagicDNS reports whether MagicDNS is enabled for the tailnet.
	MagicDNS bool
}

// DNSResolver is one configured resolver (global or split-DNS).
type DNSResolver struct {
	Address         string
	UseWithExitNode bool
}

type wireDNSConfig struct {
	Nameservers []wireDNSResolver            `json:"nameservers"`
	SplitDNS    map[string][]wireDNSResolver `json:"splitDNS"`
	SearchPaths []string                     `json:"searchPaths"`
	Preferences struct {
		OverrideLocalDNS bool `json:"overrideLocalDNS"`
		MagicDNS         bool `json:"magicDNS"`
	} `json:"preferences"`
}

type wireDNSResolver struct {
	Address         string `json:"address"`
	UseWithExitNode bool   `json:"useWithExitNode"`
}

// DNSConfiguration fetches the unified tailnet DNS configuration in a single
// call, replacing the four split DNS GETs. The required scope (dns:read /
// all:read) matches what the former split GETs used.
func (c *Client) DNSConfiguration(ctx context.Context) (*DNSConfig, error) {
	var wire wireDNSConfig
	if err := c.getJSON(ctx, c.dnsConfigURL(), &wire); err != nil {
		return nil, err
	}
	cfg := &DNSConfig{
		SearchPaths:      wire.SearchPaths,
		OverrideLocalDNS: wire.Preferences.OverrideLocalDNS,
		MagicDNS:         wire.Preferences.MagicDNS,
	}
	for _, r := range wire.Nameservers {
		cfg.Nameservers = append(cfg.Nameservers, DNSResolver(r))
	}
	if len(wire.SplitDNS) > 0 {
		cfg.SplitDNS = make(map[string][]DNSResolver, len(wire.SplitDNS))
		for domain, resolvers := range wire.SplitDNS {
			out := make([]DNSResolver, 0, len(resolvers))
			for _, r := range resolvers {
				out = append(out, DNSResolver(r))
			}
			cfg.SplitDNS[domain] = out
		}
	}
	return cfg, nil
}

// dnsConfigURL builds the unified DNS configuration endpoint URL, mirroring
// keysURL/devicesURL construction.
func (c *Client) dnsConfigURL() string {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, "/api/v2/tailnet", c.tailnet, "dns", "configuration")
	return u.String()
}
