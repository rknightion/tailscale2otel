package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

func TestObjectStoreOptionsMapsEveryConfigFieldExactly(t *testing.T) {
	got := objectStoreOptions(config.ObjectStoreConfig{
		Prefix:                     "prefix-marker",
		Interval:                   config.Duration(11 * time.Second),
		Lookback:                   config.Duration(13 * time.Minute),
		InitialLookback:            config.Duration(17 * time.Hour),
		MaxObjects:                 19,
		MaxObjectWireBytes:         21,
		MaxObjectDecompressedBytes: 23,
		MaxObjectRecords:           29,
		MaxCycleWireBytes:          30,
		MaxCycleDecompressedBytes:  31,
		MaxCycleRecords:            37,
	})
	want := objectstore.Options{
		Prefix:                     "prefix-marker",
		Interval:                   11 * time.Second,
		Lookback:                   13 * time.Minute,
		InitialLookback:            17 * time.Hour,
		MaxObjects:                 19,
		MaxObjectWireBytes:         21,
		MaxObjectDecompressedBytes: 23,
		MaxObjectRecords:           29,
		MaxCycleWireBytes:          30,
		MaxCycleDecompressedBytes:  31,
		MaxCycleRecords:            37,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("objectStoreOptions() = %+v, want exact config-only mapping %+v", got, want)
	}
}
