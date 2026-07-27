package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// This file owns object-store destination RESOLUTION and the destination-level
// validation rules. Validate() and Warnings() call into it; internal/app reads
// only FlowObjectStore.
//
// The contract (#284, maintainer-frozen 2026-07-26):
//
//   - collectors.flowlogs.objectstore is the destination for LEGACY/single mode,
//     i.e. when tailnets: is empty. It stays the only form there.
//   - With a tailnets: list (ANY length, including one) each entry must carry its
//     own complete destination under objectstore.flow. There is no inheritance
//     and no fallback from the global block — not for the endpoint, not for the
//     credentials. A runtime with no destination of its own is a startup error.
//   - Two entries may not name the same normalized feed. Two runtimes reading one
//     feed would each ingest every record and attribute a copy to its own
//     tailnet, which is exactly the cross-attribution hazard #283 closed.
//   - Ingestion SOURCE selection stays global per signal
//     (collectors.flowlogs.source); mixed poll/object-store modes per runtime are
//     out of scope.

// The two supported export layouts. Anything else is a validation error, and an
// empty value means ObjectStoreLayoutPartitioned. There is no "auto": a flat and a
// partitioned bucket are distinguishable only by listing them, and guessing wrong
// changes what the durable scan positions mean, so the choice is explicit.
const (
	ObjectStoreLayoutPartitioned = "partitioned"
	ObjectStoreLayoutFlat        = "flat"
)

// objectStoreSignalSpec says WHERE one signal's destination lives, so resolution,
// validation and warnings are written once and cannot disagree between signals.
//
// Adding a signal is adding a spec; the flow and audit specs below are the only
// two, because S3 is the only supported export and Tailscale publishes exactly
// these two log types to it (#454/#455 closed non-S3 providers; #288 added the
// configuration export).
type objectStoreSignalSpec struct {
	// signal is the frozen checkpoint-namespace segment (#453). Changing one of
	// these values orphans every deployment's durable state for that signal.
	signal string
	// globalKey / entryKey are the dotted config paths the destination is read
	// from in legacy/single mode and in a tailnets: entry, so an error names the
	// exact key an operator must fix.
	globalKey string
	entryKey  string
	global    func(*Config) ObjectStoreConfig
	entry     func(TailnetConfig) ObjectStoreConfig
}

var objectStoreFlowSpec = objectStoreSignalSpec{
	signal:    "flow",
	globalKey: "collectors.flowlogs.objectstore",
	entryKey:  "objectstore.flow",
	global:    func(c *Config) ObjectStoreConfig { return c.Collectors.Flowlogs.ObjectStore },
	entry:     func(t TailnetConfig) ObjectStoreConfig { return t.ObjectStore.Flow },
}

var objectStoreAuditSpec = objectStoreSignalSpec{
	signal:    "audit",
	globalKey: "collectors.auditlogs.objectstore",
	entryKey:  "objectstore.audit",
	global:    func(c *Config) ObjectStoreConfig { return c.Collectors.Auditlogs.ObjectStore },
	entry:     func(t TailnetConfig) ObjectStoreConfig { return t.ObjectStore.Audit },
}

// resolveObjectStore is the shared body of FlowObjectStore and AuditObjectStore.
func (c *Config) resolveObjectStore(spec objectStoreSignalSpec, tailnet string) (ObjectStoreConfig, bool) {
	if len(c.Tailnets) == 0 {
		return objectStoreWithLayoutDefault(spec.global(c)), true
	}
	for _, t := range c.Tailnets {
		if t.Name == tailnet {
			return objectStoreWithLayoutDefault(objectStoreWithBudgetDefaults(spec.entry(t))), true
		}
	}
	return ObjectStoreConfig{}, false
}

// AuditObjectStore returns the effective configuration-log object-store
// destination for the runtime whose CONFIGURED tailnet name is tailnet, under
// exactly the rules FlowObjectStore documents.
//
// It is a separate destination from the flow one and never falls back to it: the
// network and configuration exports are different objects with different record
// shapes, so reading one for the other decodes nothing and looks like an idle
// tailnet (#288).
func (c *Config) AuditObjectStore(tailnet string) (ObjectStoreConfig, bool) {
	return c.resolveObjectStore(objectStoreAuditSpec, tailnet)
}

// FlowObjectStore returns the effective flow-log object-store destination for the
// runtime whose CONFIGURED tailnet name is tailnet (the literal configured value,
// including the "-" placeholder — the same identity that keys the checkpoint
// namespace).
//
// In legacy/single mode the global collectors.flowlogs.objectstore block is
// returned verbatim for any tailnet. With a tailnets: list the matching entry's
// objectstore.flow block is returned and NOTHING is merged in from the global
// block; ok is false when no entry has that name, which the caller must treat as
// "this runtime has no object-store ingestion" rather than falling back.
//
// Credentials ride along on the returned value, so a caller only ever holds the
// material of the one tailnet it asked for.
func (c *Config) FlowObjectStore(tailnet string) (ObjectStoreConfig, bool) {
	return c.resolveObjectStore(objectStoreFlowSpec, tailnet)
}

// objectStoreWithLayoutDefault resolves an unset layout to
// ObjectStoreLayoutPartitioned. This is the ONE place that normalization happens,
// on both branches of FlowObjectStore, so every reader of a destination sees a
// concrete layout and no caller re-derives the default.
//
// It is deliberately NOT part of objectStoreWithBudgetDefaults: that function fills
// TUNING fields for a list entry and its contract is that nothing else is defaulted.
// Validate accepts an empty layout on either shape (it means partitioned), so the
// two paths agree without the global block leaking into an entry.
func objectStoreWithLayoutDefault(os ObjectStoreConfig) ObjectStoreConfig {
	if os.Layout == "" {
		os.Layout = ObjectStoreLayoutPartitioned
	}
	return os
}

// objectStoreWithBudgetDefaults fills a per-tailnet destination's TUNING fields
// from the built-in defaults, and only those: interval/lookback and the
// per-object and per-cycle budgets. Endpoint, region, bucket, prefix, path_style,
// allow_insecure_http and every credential are left exactly as configured —
// defaulting any of those would be the inheritance the contract forbids.
//
// A list entry cannot pass through Default() (there is no entry to seed before
// the file is parsed), so without this a destination that names only its bucket
// would be rejected by the budget rules that Default() satisfies for the global
// block. Only an unset (zero) field is filled, matching mergeHTTPDefaults: for a
// list entry a zero is indistinguishable from unset. A NEGATIVE value is left
// alone so validation still rejects it.
func objectStoreWithBudgetDefaults(os ObjectStoreConfig) ObjectStoreConfig {
	d := Default().Collectors.Flowlogs.ObjectStore
	if os.Interval == 0 {
		os.Interval = d.Interval
	}
	if os.Lookback == 0 {
		os.Lookback = d.Lookback
	}
	if os.InitialLookback == 0 {
		os.InitialLookback = d.InitialLookback
	}
	if os.MaxObjects == 0 {
		os.MaxObjects = d.MaxObjects
	}
	if os.MaxObjectWireBytes == 0 {
		os.MaxObjectWireBytes = d.MaxObjectWireBytes
	}
	if os.MaxObjectDecompressedBytes == 0 {
		os.MaxObjectDecompressedBytes = d.MaxObjectDecompressedBytes
	}
	if os.MaxObjectRecords == 0 {
		os.MaxObjectRecords = d.MaxObjectRecords
	}
	if os.MaxCycleWireBytes == 0 {
		os.MaxCycleWireBytes = d.MaxCycleWireBytes
	}
	if os.MaxCycleDecompressedBytes == 0 {
		os.MaxCycleDecompressedBytes = d.MaxCycleDecompressedBytes
	}
	if os.MaxCycleRecords == 0 {
		os.MaxCycleRecords = d.MaxCycleRecords
	}
	return os
}

// objectStoreDestinationDeclared reports whether an entry declared a destination
// at all. It deliberately looks at the RAW block (before budget defaulting) and
// only at destination-identity and credential fields, so "I set a lookback but
// no bucket" still counts as declared and is reported as incomplete rather than
// as missing.
func objectStoreDestinationDeclared(os ObjectStoreConfig) bool {
	return os.Endpoint != "" || os.Region != "" || os.Bucket != "" || os.Prefix != "" ||
		os.PathStyle || os.AllowInsecureHTTP ||
		os.AccessKeyID != "" || os.AccessKeyIDFile != "" ||
		os.SecretAccessKey != "" || os.SecretAccessKeyFile != "" ||
		os.SessionToken != "" || os.SessionTokenFile != ""
}

// objectStoreFeedKey is the normalized identity of one physical feed: endpoint,
// region, bucket, prefix and path_style, per the frozen contract. Values are
// length-prefixed so no separator can be forged out of a bucket or prefix.
//
// Normalization is deliberately conservative in the direction of catching MORE
// duplicates: an endpoint differing only in case, trailing slash or a prefix
// differing only in leading/trailing slashes is the SAME feed (the ingestion
// engine trims the prefix the same way, see collector/objectstore/state.go).
// Bucket case is preserved — S3 requires lower case but a non-AWS
// implementation may not, so two spellings could be two buckets.
func objectStoreFeedKey(os ObjectStoreConfig) string {
	parts := []string{
		normalizedObjectStoreEndpoint(os.Endpoint),
		strings.ToLower(strings.TrimSpace(os.Region)),
		strings.TrimSpace(os.Bucket),
		strings.Trim(strings.TrimSpace(os.Prefix), "/"),
		strconv.FormatBool(os.PathStyle),
	}
	var b strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&b, "%d:%s|", len(p), p)
	}
	return b.String()
}

func normalizedObjectStoreEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
}

// validateFlowObjectStore checks every flow destination the configured runtimes
// will actually read. Retained as the frozen #284 entry point; the body is shared
// with the audit signal.
func (c *Config) validateFlowObjectStore(collectorName string) error {
	return c.validateObjectStore(objectStoreFlowSpec, collectorName)
}

// validateAuditObjectStore checks every configuration-log destination the
// configured runtimes will actually read.
func (c *Config) validateAuditObjectStore(collectorName string) error {
	return c.validateObjectStore(objectStoreAuditSpec, collectorName)
}

// validateObjectStore checks every destination for ONE signal, dispatching on the
// config shape: the global block in legacy mode, one complete block per entry
// with a tailnets: list. collectorName is the log collector whose source selected
// the object store, so the message can say what turned this path on.
func (c *Config) validateObjectStore(spec objectStoreSignalSpec, collectorName string) error {
	if len(c.Tailnets) == 0 {
		// The global block has already been through Default(), so an explicit zero
		// there is an operator error, not "unset" — validate it as written.
		return validateObjectStoreDestination(collectorName, spec.globalKey, spec.global(c))
	}
	for i, t := range c.Tailnets {
		key := fmt.Sprintf("tailnets[%d].%s", i, spec.entryKey)
		if !objectStoreDestinationDeclared(spec.entry(t)) {
			return fmt.Errorf("tailnets[%d] %q: collectors.%s.source=objectstore requires %s "+
				"(endpoint + region + bucket): an object-store destination is never inherited from "+
				"%s in multi-tailnet mode, because two runtimes reading one "+
				"feed would each ingest every record and attribute a copy to their own tailnet",
				i, t.Name, collectorName, key, spec.globalKey)
		}
		dest := objectStoreWithBudgetDefaults(spec.entry(t))
		if err := validateObjectStoreDestination(collectorName, key, dest); err != nil {
			return fmt.Errorf("tailnets[%d] %q: %w", i, t.Name, err)
		}
	}
	return nil
}

// validateObjectStoreFeeds rejects any two destinations THIS PROCESS will read
// that name the same physical feed.
//
// One check covers both collision shapes, because they are the same fault:
//
//   - two tailnets on one feed — each runtime ingests every record and attributes
//     a copy to its own tailnet, the cross-attribution hazard #283 closed;
//   - two signals on one feed — each engine fetches every object and then decodes
//     the other signal's records as garbage, burning its budget to produce
//     undecodable-object errors.
//
// Only destinations an ENABLED collector actually reads are considered: a
// leftover block for a signal that is polling is inert config, and failing
// startup over it would be a false alarm.
func (c *Config) validateObjectStoreFeeds(specs ...objectStoreSignalSpec) error {
	type owner struct {
		desc string
	}
	seen := map[string]owner{}
	claim := func(dest ObjectStoreConfig, desc string) error {
		feed := objectStoreFeedKey(dest)
		if prev, dup := seen[feed]; dup {
			return fmt.Errorf("%s and %s name the same object-store feed (endpoint, region, bucket, "+
				"prefix and path_style all match): both would ingest every object in it, so give each "+
				"one its own export destination (a distinct bucket, or a distinct prefix within one "+
				"bucket)", prev.desc, desc)
		}
		seen[feed] = owner{desc: desc}
		return nil
	}
	for _, spec := range specs {
		if len(c.Tailnets) == 0 {
			if err := claim(spec.global(c), spec.globalKey); err != nil {
				return err
			}
			continue
		}
		for i, t := range c.Tailnets {
			dest := objectStoreWithBudgetDefaults(spec.entry(t))
			desc := fmt.Sprintf("tailnets[%d] %q %s", i, t.Name, spec.entryKey)
			if err := claim(dest, desc); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateObjectStoreDestination rejects one destination that cannot work.
// key is the dotted config path it was read from, so every message names the
// exact key an operator has to fix. No configured VALUE is echoed: a
// validation error is logged, and the credential fields sit in the same struct.
func validateObjectStoreDestination(collectorName, key string, os ObjectStoreConfig) error {
	switch {
	case os.Bucket == "":
		return fmt.Errorf("collectors.%s.source=objectstore requires "+
			"%s.bucket — without it there is no ingestion "+
			"path and flow logs are silently never collected", collectorName, key)
	case os.Endpoint == "":
		return fmt.Errorf("collectors.%s.source=objectstore requires "+
			"%s.endpoint (e.g. https://s3.eu-west-2.amazonaws.com); "+
			"it is not derived from the region, because a non-AWS implementation would be "+
			"derived wrong", collectorName, key)
	case os.Region == "":
		return fmt.Errorf("collectors.%s.source=objectstore requires "+
			"%s.region — it is part of the request signature, "+
			"so a wrong or missing value fails every request with HTTP 403", collectorName, key)
	case !usableObjectStoreEndpointURL(os.Endpoint):
		// Structural and immutable: the S3 client cannot be built at all, so fail
		// startup here where the message can name the key, rather than leaving one
		// runtime silently without ingestion.
		return fmt.Errorf("%s.endpoint is not a usable service URL: it must be an absolute "+
			"http:// or https:// URL with a host, e.g. https://s3.eu-west-2.amazonaws.com", key)
	case plaintextRemoteObjectStoreEndpoint(os.Endpoint) && !os.AllowInsecureHTTP:
		return fmt.Errorf("%s.endpoint uses plaintext HTTP for a remote "+
			"host: set allow_insecure_http only when accepting that credentials and session tokens "+
			"will cross the network without TLS", key)
	case !oneOf(os.Layout, "", ObjectStoreLayoutPartitioned, ObjectStoreLayoutFlat):
		// No autodetection and no third value: the layout decides which prefixes are
		// enumerated, so an unrecognized one would either leave every flat object
		// undiscovered or re-interpret the durable scan positions.
		return fmt.Errorf("%s.layout %q invalid: must be %q (the default, and what "+
			"Tailscale's own export writes) or %q (a copied/mirrored export whose "+
			"self-contained basenames sit directly under the prefix); unset means %q",
			key, os.Layout, ObjectStoreLayoutPartitioned, ObjectStoreLayoutFlat,
			ObjectStoreLayoutPartitioned)
	case os.MaxObjects < 0:
		return fmt.Errorf("%s.max_objects must be >= 0 (got %d)", key, os.MaxObjects)
	case os.MaxObjectWireBytes <= 0:
		return fmt.Errorf("%s.max_object_wire_bytes must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxObjectWireBytes)
	case os.MaxObjectDecompressedBytes <= 0:
		return fmt.Errorf("%s.max_object_decompressed_bytes must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxObjectDecompressedBytes)
	case os.MaxObjectRecords <= 0:
		return fmt.Errorf("%s.max_object_records must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxObjectRecords)
	case os.MaxCycleWireBytes <= 0:
		return fmt.Errorf("%s.max_cycle_wire_bytes must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxCycleWireBytes)
	case os.MaxCycleDecompressedBytes <= 0:
		return fmt.Errorf("%s.max_cycle_decompressed_bytes must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxCycleDecompressedBytes)
	case os.MaxCycleRecords <= 0:
		return fmt.Errorf("%s.max_cycle_records must be positive "+
			"(supplied %d; required >= 1)", key, os.MaxCycleRecords)
	case os.MaxCycleWireBytes < os.MaxObjectWireBytes:
		return fmt.Errorf("%s.max_cycle_wire_bytes must be at least "+
			"max_object_wire_bytes (supplied %d; required >= %d)",
			key, os.MaxCycleWireBytes, os.MaxObjectWireBytes)
	case os.MaxCycleDecompressedBytes < os.MaxObjectDecompressedBytes:
		return fmt.Errorf("%s.max_cycle_decompressed_bytes must be at least "+
			"max_object_decompressed_bytes (supplied %d; required >= %d)",
			key, os.MaxCycleDecompressedBytes, os.MaxObjectDecompressedBytes)
	case os.MaxCycleRecords < os.MaxObjectRecords:
		return fmt.Errorf("%s.max_cycle_records must be at least "+
			"max_object_records (supplied %d; required >= %d)",
			key, os.MaxCycleRecords, os.MaxObjectRecords)
	case os.Interval.D() < 0 || os.Lookback.D() < 0 || os.InitialLookback.D() < 0:
		return fmt.Errorf("%s interval/lookback/initial_lookback must be >= 0", key)
	}
	return nil
}

// usableObjectStoreEndpointURL mirrors the endpoint rules in s3.New: absolute,
// http or https, with a host. Anything else makes client construction fail.
func usableObjectStoreEndpointURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != ""
}

// objectStoreWarnings returns the per-destination advisories for one destination,
// keyed by the config path it came from so a multi-tailnet operator can tell
// which entry is at fault.
func objectStoreWarnings(key string, os ObjectStoreConfig) []string {
	var w []string
	if os.Lookback.D() > 0 && os.Lookback.D() < os.Interval.D() {
		w = append(w, fmt.Sprintf("%s.lookback (%v) is shorter than its "+
			"interval (%v): the overlap that catches a late-arriving object is smaller than the gap "+
			"between listings, so an object that lands between two cycles can be missed entirely.",
			key, os.Lookback.D(), os.Interval.D()))
	}
	if os.Layout == ObjectStoreLayoutFlat {
		w = append(w, fmt.Sprintf("%s.layout=flat is for copied or mirrored exports, not for "+
			"Tailscale's own — those are always written under YYYY/MM/DD partitions. Flat has no "+
			"partitions to bound re-listing, so once caught up every cycle re-walks the prefix: more "+
			"LIST requests than the partitioned default, and a new object is discovered only when the "+
			"walk reaches its key. Each cycle is still bounded by max_objects and resumes from a "+
			"durable position, so no single cycle scans the whole bucket.", key))
	}
	if os.AllowInsecureHTTP && plaintextRemoteObjectStoreEndpoint(os.Endpoint) {
		w = append(w, fmt.Sprintf("%s.allow_insecure_http=true sends S3 signing "+
			"credentials and temporary session tokens over a plaintext remote connection. "+
			"Use HTTPS unless this transport is isolated and explicitly trusted.", key))
	}
	return w
}

// flowObjectStoreWarnings collects the advisories for every destination the
// configured runtimes will read, in the same shape validateFlowObjectStore
// checks them.
func (c *Config) flowObjectStoreWarnings() []string {
	if len(c.Tailnets) == 0 {
		return objectStoreWarnings("collectors.flowlogs.objectstore", c.Collectors.Flowlogs.ObjectStore)
	}
	var w []string
	// A global block left behind by a migration is inert, and silently-ignored
	// destination config is how an operator concludes the exporter is broken.
	if objectStoreDestinationDeclared(c.Collectors.Flowlogs.ObjectStore) {
		w = append(w, "collectors.flowlogs.objectstore is ignored in multi-tailnet mode: with a "+
			"tailnets: list each entry reads its own objectstore.flow destination and nothing is "+
			"inherited from this block. Move it under the tailnets[] entry it belongs to, then delete it.")
	}
	for i, t := range c.Tailnets {
		w = append(w, objectStoreWarnings(
			fmt.Sprintf("tailnets[%d].objectstore.flow", i),
			objectStoreWithBudgetDefaults(t.ObjectStore.Flow))...)
	}
	return w
}
