package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// The partitioned layout can never reach further back than objectstore's
// MaxDayPrefixes day partitions, and internal/config is the ONLY place an operator
// is told so — the engine never lists the older objects, so it emits no gap, no
// skip and no metric for them (#463). config therefore holds its own copy of the
// ceiling (it cannot import objectstore, which imports it), and this test is the
// seam that keeps the two from drifting.
//
// This lives in the external config_test package precisely because that is the one
// place both can be imported at once.
func TestObjectStoreBackfillCeilingMatchesTheEngine(t *testing.T) {
	engine := time.Duration(objectstore.MaxDayPrefixes) * 24 * time.Hour

	// Drive it through the observable behavior rather than the constant: an
	// initial_lookback exactly at the ceiling must NOT warn, and one a day past it
	// must.
	at := objectStoreCfgWithLookback(t, engine)
	if hasBackfillWarning(at) {
		t.Errorf("initial_lookback = %v (exactly the engine's %d-partition reach) warned; "+
			"config's ceiling is SHORTER than the engine's and the advice is needless",
			engine, objectstore.MaxDayPrefixes)
	}
	past := objectStoreCfgWithLookback(t, engine+24*time.Hour)
	if !hasBackfillWarning(past) {
		t.Errorf("initial_lookback = %v (a day past the engine's %d-partition reach) did not warn; "+
			"config's ceiling is LONGER than the engine's, so an operator is told nothing while "+
			"objects are silently skipped", engine+24*time.Hour, objectstore.MaxDayPrefixes)
	}
}

func objectStoreCfgWithLookback(t *testing.T, d time.Duration) []string {
	t.Helper()
	c := config.Default()
	c.Tailscale.Tailnet = "example.com"
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "k"
	c.Collectors.Flowlogs.Source = "objectstore"
	c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.Flowlogs.ObjectStore.Region = "eu-west-2"
	c.Collectors.Flowlogs.ObjectStore.Bucket = "flows"
	c.Collectors.Flowlogs.ObjectStore.InitialLookback = config.Duration(d)
	return c.Warnings()
}

func hasBackfillWarning(warnings []string) bool {
	for _, w := range warnings {
		if strings.Contains(w, "initial_lookback is") && strings.Contains(w, "day partitions") {
			return true
		}
	}
	return false
}
