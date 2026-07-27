package config

import (
	"strings"
	"testing"
)

// auditObjectStoreConfig is the legacy/single-mode shape: audit logs ingested
// from their own export destination.
func auditObjectStoreConfig(tune func(*Config)) *Config {
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "k"
	c.Collectors.Auditlogs.Source = "objectstore"
	c.Collectors.Auditlogs.ObjectStore.Endpoint = "https://s3.us-east-1.amazonaws.com"
	c.Collectors.Auditlogs.ObjectStore.Region = "us-east-1"
	c.Collectors.Auditlogs.ObjectStore.Bucket = "config-logs"
	if tune != nil {
		tune(c)
	}
	return c
}

// Configuration logs ARE exported to S3, verified against a live export on
// 2026-07-27 (#288). This replaces TestValidate_ObjectStoreIsFlowLogsOnly, which
// asserted the opposite on a premise that has since been disproved.
func TestValidate_AuditObjectStoreSourceIsAccepted(t *testing.T) {
	if err := auditObjectStoreConfig(nil).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// The audit destination is its own block. Nothing is inherited from the flow
// block: they are different exports, so silently reading the flow bucket for
// audit records would decode nothing and look like an idle tailnet.
func TestValidate_AuditObjectStoreIsNotInheritedFromFlow(t *testing.T) {
	c := Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "k"
	c.Collectors.Auditlogs.Source = "objectstore"
	c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.us-east-1.amazonaws.com"
	c.Collectors.Flowlogs.ObjectStore.Region = "us-east-1"
	c.Collectors.Flowlogs.ObjectStore.Bucket = "flows"

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted auditlogs.source=objectstore with no audit destination")
	}
	if !strings.Contains(err.Error(), "collectors.auditlogs.objectstore") {
		t.Errorf("error = %v, want it to name collectors.auditlogs.objectstore", err)
	}
}

// Two signals reading ONE feed is the same hazard as two tailnets reading one
// feed: each engine would fetch every object, decode the other signal's records
// as garbage, and burn its budget doing it.
func TestValidate_FlowAndAuditMayNotShareOneFeed(t *testing.T) {
	c := auditObjectStoreConfig(func(c *Config) {
		c.Collectors.Flowlogs.Source = "objectstore"
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.us-east-1.amazonaws.com"
		c.Collectors.Flowlogs.ObjectStore.Region = "us-east-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "config-logs"
	})

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted flow and audit reading the same bucket and prefix")
	}
	if !strings.Contains(err.Error(), "same object-store feed") {
		t.Errorf("error = %v, want it to report the shared feed", err)
	}
}

// One bucket with a prefix per signal is the normal deployment — Tailscale's own
// console writes both exports with an operator-chosen key prefix.
func TestValidate_FlowAndAuditMayShareABucketWithDistinctPrefixes(t *testing.T) {
	c := auditObjectStoreConfig(func(c *Config) {
		c.Collectors.Auditlogs.ObjectStore.Prefix = "configuration/"
		c.Collectors.Flowlogs.Source = "objectstore"
		c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.us-east-1.amazonaws.com"
		c.Collectors.Flowlogs.ObjectStore.Region = "us-east-1"
		c.Collectors.Flowlogs.ObjectStore.Bucket = "config-logs"
		c.Collectors.Flowlogs.ObjectStore.Prefix = "network/"
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A feed collision is only a problem for a signal that actually reads it. A
// leftover audit block with auditlogs still polling must not fail startup.
func TestValidate_UnusedAuditDestinationIsNotCheckedForCollision(t *testing.T) {
	c := objectStoreConfig(func(c *Config) {
		c.Collectors.Auditlogs.Source = "poll"
		c.Collectors.Auditlogs.ObjectStore = c.Collectors.Flowlogs.ObjectStore
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// In multi-tailnet mode the audit destination comes from the entry, exactly as
// the flow one does, and is never inherited from the global block (#284).
func TestAuditObjectStore_MultiTailnetTakesTheEntryDestination(t *testing.T) {
	c := Default()
	c.Collectors.Auditlogs.Source = "objectstore"
	c.Collectors.Auditlogs.ObjectStore.Bucket = "global-should-be-ignored"
	c.Tailnets = []TailnetConfig{{
		Name: "a.example",
		Auth: TailscaleAuth{Method: "apikey", APIKey: "k"},
		ObjectStore: TailnetObjectStore{Audit: ObjectStoreConfig{
			Endpoint: "https://s3.us-east-1.amazonaws.com",
			Region:   "us-east-1",
			Bucket:   "a-config-logs",
		}},
	}}

	got, ok := c.AuditObjectStore("a.example")
	if !ok {
		t.Fatal("AuditObjectStore(a.example) not found")
	}
	if got.Bucket != "a-config-logs" {
		t.Errorf("bucket = %q, want the entry's own", got.Bucket)
	}
	// Tuning fields are defaulted for an entry (it never passes through Default),
	// which is what makes a bucket-only entry valid.
	if got.MaxObjects == 0 || got.Layout == "" {
		t.Errorf("entry destination missing tuning defaults: %+v", got)
	}
	if _, ok := c.AuditObjectStore("b.example"); ok {
		t.Error("AuditObjectStore returned a destination for an unlisted tailnet")
	}
}

// A tailnets: entry using the audit object store must declare its own audit
// destination — a flow destination on the same entry is not a substitute.
func TestValidate_MultiTailnetAuditRequiresItsOwnAuditBlock(t *testing.T) {
	c := Default()
	c.Collectors.Auditlogs.Source = "objectstore"
	c.Tailnets = []TailnetConfig{{
		Name: "a.example",
		Auth: TailscaleAuth{Method: "apikey", APIKey: "k"},
		ObjectStore: TailnetObjectStore{Flow: ObjectStoreConfig{
			Endpoint: "https://s3.us-east-1.amazonaws.com",
			Region:   "us-east-1",
			Bucket:   "a-flows",
		}},
	}}

	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted an entry with no audit destination")
	}
	if !strings.Contains(err.Error(), "objectstore.audit") {
		t.Errorf("error = %v, want it to name the entry's objectstore.audit key", err)
	}
}

// The audit block gets the same budget defaults as the flow one, so a
// bucket-only configuration is valid rather than failing the positive-budget
// rules.
func TestAuditObjectStoreBoundsDefaults(t *testing.T) {
	audit := Default().Collectors.Auditlogs.ObjectStore
	flow := Default().Collectors.Flowlogs.ObjectStore
	if audit != flow {
		t.Errorf("auditlogs.objectstore defaults differ from flowlogs.objectstore:\n audit = %+v\n flow  = %+v",
			audit, flow)
	}
}
