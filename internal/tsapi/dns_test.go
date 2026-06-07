package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// dnsConfigFixtureFull exercises global + split-DNS resolvers, useWithExitNode
// present/absent, and both preferences. Shapes match the unified
// GET /dns/configuration response (and the omit-when-false useWithExitNode seen
// in .capture/probe_dns_configuration.json).
const dnsConfigFixtureFull = `{
  "nameservers":[
    {"address":"10.0.0.254"},
    {"address":"1.1.1.1","useWithExitNode":true}
  ],
  "splitDNS":{
    "corp.example.com":[
      {"address":"10.0.0.53","useWithExitNode":true},
      {"address":"10.0.1.53"}
    ],
    "other.internal":null
  },
  "searchPaths":["rob-knight.net"],
  "preferences":{"overrideLocalDNS":true,"magicDNS":true}
}`

func TestDNSConfiguration_DecodesUnifiedConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/tailnet/example.com/dns/configuration" {
			http.Error(w, "bad path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			http.Error(w, "auth = "+got, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(dnsConfigFixtureFull))
	}))
	defer srv.Close()

	cfg, err := newClient(t, srv.URL).DNSConfiguration(context.Background())
	if err != nil {
		t.Fatalf("DNSConfiguration: %v", err)
	}
	if !cfg.OverrideLocalDNS {
		t.Errorf("OverrideLocalDNS = false, want true")
	}
	if !cfg.MagicDNS {
		t.Errorf("MagicDNS = false, want true")
	}
	if len(cfg.Nameservers) != 2 {
		t.Fatalf("len(Nameservers) = %d, want 2", len(cfg.Nameservers))
	}
	if cfg.Nameservers[0].Address != "10.0.0.254" || cfg.Nameservers[0].UseWithExitNode {
		t.Errorf("Nameservers[0] = %+v, want {10.0.0.254 false}", cfg.Nameservers[0])
	}
	if cfg.Nameservers[1].Address != "1.1.1.1" || !cfg.Nameservers[1].UseWithExitNode {
		t.Errorf("Nameservers[1] = %+v, want {1.1.1.1 true}", cfg.Nameservers[1])
	}
	if len(cfg.SearchPaths) != 1 || cfg.SearchPaths[0] != "rob-knight.net" {
		t.Errorf("SearchPaths = %v, want [rob-knight.net]", cfg.SearchPaths)
	}
	if len(cfg.SplitDNS) != 2 {
		t.Fatalf("len(SplitDNS) = %d, want 2 (corp.example.com + other.internal)", len(cfg.SplitDNS))
	}
	corp := cfg.SplitDNS["corp.example.com"]
	if len(corp) != 2 {
		t.Fatalf("corp resolvers = %d, want 2", len(corp))
	}
	if corp[0].Address != "10.0.0.53" || !corp[0].UseWithExitNode {
		t.Errorf("corp[0] = %+v, want {10.0.0.53 true}", corp[0])
	}
	if corp[1].Address != "10.0.1.53" || corp[1].UseWithExitNode {
		t.Errorf("corp[1] = %+v, want {10.0.1.53 false}", corp[1])
	}
	// A null resolver list decodes to a present key with zero resolvers.
	if got, ok := cfg.SplitDNS["other.internal"]; !ok || len(got) != 0 {
		t.Errorf("SplitDNS[other.internal] = %v (ok=%v), want present + empty", got, ok)
	}
}

// TestDNSConfiguration_MinimalConfig matches the real capture: a single global
// resolver, no useWithExitNode, no splitDNS key at all.
func TestDNSConfiguration_MinimalConfig(t *testing.T) {
	const fixture = `{"nameservers":[{"address":"10.0.0.254"}],"searchPaths":["rob-knight.net"],"preferences":{"overrideLocalDNS":true,"magicDNS":true}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	cfg, err := newClient(t, srv.URL).DNSConfiguration(context.Background())
	if err != nil {
		t.Fatalf("DNSConfiguration: %v", err)
	}
	if len(cfg.Nameservers) != 1 || cfg.Nameservers[0].UseWithExitNode {
		t.Errorf("Nameservers = %+v, want one resolver, exit-node false", cfg.Nameservers)
	}
	if len(cfg.SplitDNS) != 0 {
		t.Errorf("SplitDNS = %v, want nil/empty", cfg.SplitDNS)
	}
}
