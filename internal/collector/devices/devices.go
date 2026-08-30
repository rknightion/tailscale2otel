// Package devices implements the "devices" snapshot collector. On each tick it
// lists every device in the tailnet via a single GET /devices?fields=all (the
// rich field set), emits per-device and aggregate metrics, and repopulates the
// shared enrich.DeviceCache so flow and audit records can be resolved to
// human-readable device identity.
//
// The rich device record carries a real connected-to-control flag, per-region
// DERP latency, inline advertised/enabled routes, and OS distribution details
// (os.version) with no per-device fan-out. The optional route gauges read the
// inline route slices (no extra API calls); posture collection, when enabled,
// makes one additional API call per device and is therefore off by default.
package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/entityage"
	"github.com/rknightion/tailscale2otel/v4/internal/geoip"
	"github.com/rknightion/tailscale2otel/v4/internal/release"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Compile-time assertions: *Collector is a SnapshotCollector and *tsapi.Client
// satisfies the narrow api surface this collector depends on.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

// Metric and event names emitted by this collector.
const (
	metricOnline          = "tailscale.device.online"
	metricLastSeen        = "tailscale.device.last_seen"
	metricKeyExpiry       = "tailscale.device.key.expiry"
	metricUpdateAvailable = "tailscale.device.update_available"
	metricDERPLatency     = "tailscale.device.derp.latency"

	metricMultipleConnections       = "tailscale.device.multiple_connections"
	metricBlocksIncomingConnections = "tailscale.device.blocks_incoming_connections"
	metricPostureIdentityDisabled   = "tailscale.device.posture_identity.disabled"
	metricRoutesAdvertised          = "tailscale.device.routes.advertised"
	metricRoutesEnabled             = "tailscale.device.routes.enabled"
	metricDevicesCount              = "tailscale.devices.count"
	metricAttribute                 = "tailscale.device.attribute"
	metricAttributeInfo             = "tailscale.device.attribute.info"
	metricAttributeExpiry           = "tailscale.device.attribute.expiry"
	metricAttributesDropped         = "tailscale.device.attributes.dropped"

	metricDevicesUntagged  = "tailscale.devices.untagged"
	metricDevicesEphemeral = "tailscale.devices.ephemeral"
	metricDevicesByVersion = "tailscale.devices.by_version"
	metricDevicesByTag     = "tailscale.devices.by_tag"
	metricDevicesKeyExpiry = "tailscale.devices.key_expiry"

	metricDeviceInvites           = "tailscale.device_invites.count"
	metricDeviceInvitesPendingAge = "tailscale.device_invites.pending_age"

	// #414 SSH / key-expiry-disabled posture: fleet counts plus per-device flags.
	metricDevicesSSHEnabled        = "tailscale.devices.ssh_enabled"
	metricDeviceSSHEnabled         = "tailscale.device.ssh_enabled"
	metricDevicesKeyExpiryDisabled = "tailscale.devices.key_expiry_disabled"
	metricDeviceKeyExpiryDisabled  = "tailscale.device.key_expiry_disabled"

	// #427 Linux distribution inventory.
	metricDevicesByDistro = "tailscale.devices.by_distro"
	metricDeviceDistro    = "tailscale.device.distro"

	// #426 fleet lifecycle/age distribution.
	metricDevicesAge = "tailscale.devices.age"

	metricDeviceVersionSkew  = "tailscale.device.version_skew"
	metricFleetLatestVersion = "tailscale.fleet.latest_version"
	metricDevicesOutdated    = "tailscale.devices.outdated"

	metricCacheAge  = "tailscale2otel.enrich.cache_age"
	metricCacheSize = "tailscale2otel.enrich.cache_size"

	eventPosture               = "tailscale.device.posture"
	eventDeviceInvite          = "tailscale.device_invite"
	eventDeviceKeyExpiry       = "tailscale.device.key_expiring"
	eventDeviceAttributeExpiry = "tailscale.device.attribute.expiring"

	metricTailnetLockErrors    = "tailscale.tailnet_lock.errors"
	metricDerpRegionLatencyMin = "tailscale.derp.region.latency_min"
	metricDerpRegionDevices    = "tailscale.derp.region.devices"
	metricDerpRegionPreferred  = "tailscale.derp.region.preferred"

	eventTailnetLockError = "tailscale.device.tailnet_lock_error"

	metricConnHardNAT       = "tailscale.device.connectivity.hard_nat"
	metricConnEndpoints     = "tailscale.device.connectivity.endpoints"
	metricDevicesByCountry  = "tailscale.devices.by_country"
	metricConnDirectCapable = "tailscale.device.connectivity.direct_capable"
	metricConnUDP           = "tailscale.device.connectivity.udp"
	metricConnIPv6          = "tailscale.device.connectivity.ipv6"

	metricDevicesHardNAT        = "tailscale.devices.hard_nat"
	metricDevicesDirectCapable  = "tailscale.devices.direct_capable"
	metricDevicesClientSupports = "tailscale.devices.client_supports"

	metricExitNodesCount       = "tailscale.exit_nodes.count"
	metricSubnetRoutesAdv      = "tailscale.subnet_routes.advertised"
	metricSubnetRoutesEnabled  = "tailscale.subnet_routes.enabled"
	metricSubnetRoutesUnapprvd = "tailscale.subnet_routes.unapproved"
	metricSubnetRoutesRouters  = "tailscale.subnet_routes.routers"
	metricDeviceExitNode       = "tailscale.device.exit_node"
)

// Attribute keys specific to this collector.
const (
	attrAuthorized    = "tailscale.authorized"
	attrExternal      = "tailscale.external"
	attrDERPRegion    = "tailscale.derp.region"
	attrDERPPreferred = "tailscale.derp.preferred"

	attrInviteAccepted      = "tailscale.device_invite.accepted"
	attrInviteAllowExitNode = "tailscale.device_invite.allow_exit_node"
	attrInviteMultiUse      = "tailscale.device_invite.multi_use"

	// attrInviteDelivery carries the bounded delivery classification of a
	// device-share invite (emailed | manual_link | unknown). It describes HOW an
	// invite was distributed, never to whom — the invitee email stays on the
	// PII-gated log event and the inviteUrl bearer token is never decoded at all.
	attrInviteDelivery = "tailscale.device_invite.delivery"

	// Distro inventory labels (#427). Bounded platform vocabulary from the
	// device's distro block; the raw distro VERSION is deliberately absent here —
	// it already has one home as semconv.OSVersion on the per-device identity
	// attrs, and a second, coarser home would only multiply series.
	attrDistroName     = "tailscale.distro.name"
	attrDistroCodename = "tailscale.distro.codename"

	// attrActorLogin is the loginName of the user who accepted a device-share
	// invite (acceptedBy.loginName on the wire). Uses the stable OTel user.*
	// registry key "user.name" (CatEmails in the PII registry).
	attrActorLogin = semconv.AttrUserName

	// attrDeviceKeyExpiresInDays carries the remaining days until a device's
	// node key expires on the tailscale.device.key_expiring log event.
	// Numeric, non-identifying — not added to the PII registry.
	attrDeviceKeyExpiresInDays = "tailscale.device.key_expires_in_days"

	// attrDeviceAttributeExpiresInDays carries the remaining days until a
	// device posture attribute expires on the
	// tailscale.device.attribute.expiring log event. Numeric, non-identifying
	// — not added to the PII registry, same rationale as
	// attrDeviceKeyExpiresInDays.
	attrDeviceAttributeExpiresInDays = "tailscale.device.attribute_expires_in_days"

	// Fleet-hygiene roll-up labels.
	attrClientVersion = "tailscale.client_version"
	attrTag           = "tailscale.tag"

	// Posture attribute metric labels (tailscale.device.attribute{,.info}): the
	// full namespaced posture key, and (info gauge only) its string value.
	attrAttribute      = "attribute"
	attrAttributeValue = "value"

	// attrPostureDetails carries the full, arbitrary posture attribute map
	// (JSON-encoded) on the tailscale.device.posture LOG event. The raw map's
	// keys are provider-namespaced (e.g. "intune:...", "custom:...") and
	// unbounded, so they can never individually be registered in the PII
	// registry; routing them all through this single classified key
	// (CatFreeTextDetails, same category as tailscale.audit.details and the
	// device.attribute.info "value" label) lets pii_filter.free_text_details
	// actually gate them (#56).
	attrPostureDetails = "tailscale.device.posture.details"

	// B3/B4 connectivity + routing labels.
	attrConnCapability  = "tailscale.connectivity.capability"
	attrExitNodeState   = "tailscale.exit_node.state"
	attrExitNodeEnabled = "tailscale.exit_node.enabled"
	attrRouteCIDR       = "tailscale.route.cidr"
)

// Exit-node state label values for tailscale.exit_nodes.count.
const (
	exitStateAdvertised = "advertised"
	exitStateEnabled    = "enabled"
)

// Curated posture label keys carried by the posture info gauge. Each maps a
// colon-namespaced posture attribute (node:os, node:osVersion, …) to a short,
// analytics-friendly label name; a label is set only when its source key is
// present in the device's posture map.
const (
	attrPostureOS         = "os"
	attrPostureOSVersion  = "os_version"
	attrPostureTSVersion  = "ts_version"
	attrPostureAutoUpdate = "auto_update"
	attrPostureEncrypted  = "encrypted"
	attrPostureTrack      = "track"
)

// Posture LOG emission modes, controlling how often the posture log event fires
// (the posture info gauge metric is unaffected and always emitted per scrape).
const (
	// postureLogChanges emits the posture log for a device only when its posture
	// changed since the previous scrape (first-seen counts as changed). Default.
	postureLogChanges = "changes"
	// postureLogAlways emits the posture log every scrape (legacy behavior).
	postureLogAlways = "always"
	// postureLogOff never emits the posture log (the info gauge is still emitted).
	postureLogOff = "off"
)

// postureKeyToLabel maps the curated colon-namespaced posture attribute keys to
// their short metric label names. Posture keys not present here are not carried
// on the info gauge (they remain on the posture log's full attribute set).
var postureKeyToLabel = map[string]string{
	"node:os":               attrPostureOS,
	"node:osVersion":        attrPostureOSVersion,
	"node:tsVersion":        attrPostureTSVersion,
	"node:tsAutoUpdate":     attrPostureAutoUpdate,
	"node:tsStateEncrypted": attrPostureEncrypted,
	"node:tsReleaseTrack":   attrPostureTrack,
}

const defaultInterval = 60 * time.Second

// tagOther is the sentinel tailscale.tag value the by_tag rollup folds
// over-the-cap tags into, so per-tag totals are preserved.
const tagOther = "__other__"

// Distro-inventory bounding (#427). distroRollupLimit caps the distinct
// (name, codename) series on tailscale.devices.by_distro; the overflow folds
// into a single distroOther/distroOther series so the fleet total is preserved,
// exactly like the by_tag rollup's __other__ bucket. The cap is a constant, not
// a config key: unlike ACL tags (operator-authored, genuinely unbounded) the
// distro vocabulary is a short upstream platform list, so 50 is far above any
// real fleet and exists purely as a guard against a future free-text value.
const (
	distroRollupLimit = 50
	distroOther       = "__other__"
	// distroUnknown is the bounded fallback for a distro field that is present
	// on the device but empty or unparseable — most often a codename the
	// distribution does not publish. A device with NO distro name at all is
	// skipped entirely instead (absence, not an "unknown" mega-bucket): most
	// non-Linux devices report nothing here, and folding them all into one
	// series would swamp the actual distribution this metric exists to show.
	distroUnknown = "unknown"
)

// Bounded subrequest type names for the per-device N+1 calls (#421). These are
// metric label values, so they are a CLOSED set — one per extra API call the
// devices collector makes per device.
//
// subrequestDeviceInvites doubles as the apistate/tracker OPERATION name for
// the device-invites availability signal (#524): its value already equals
// app.SubrequestDeviceInvites in internal/app/capability.go, so no second
// constant is needed and it is passed directly to apistate.Observe.
// subrequestPostureAttributes is NOT reused the same way — its value
// ("posture_attributes") does not match app.SubrequestDevicePosture
// ("device_posture"), so the posture availability signal uses the separate
// opDevicePosture constant below instead. This Coverage label is untouched by
// #524.
const (
	subrequestDeviceInvites     = "device_invites"
	subrequestPostureAttributes = "posture_attributes"
)

// opListTailnetDevices is the upstream OpenAPI operationId of the main device
// listing call (GET /devices?fields=all via DevicesRich), used as the
// tailscale.api.operation attribute value on the apistate availability signal
// (#524).
const opListTailnetDevices = "listTailnetDevices"

// opDevicePosture is the apistate/tracker OPERATION name for the per-device
// posture-attributes subrequest (#524) — deliberately NOT its upstream
// operationId (getDevicePostureAttributes) and NOT the existing Coverage
// subrequest label (subrequestPostureAttributes, left unchanged above). It
// must equal app.SubrequestDevicePosture in internal/app/capability.go
// byte-for-byte: the capability matrix looks up a tracker entry whose
// Operation field equals this string, and a mismatch leaves that row
// permanently "unknown".
const opDevicePosture = "device_posture"

// Bounded device-invite delivery states (#413), carried on attrInviteDelivery.
const (
	// deliveryEmailed: the invite was addressed to an email (or upstream has
	// recorded an email send attempt for it).
	deliveryEmailed = "emailed"
	// deliveryManualLink: no email and no send attempt, but the record is
	// otherwise populated — a share link distributed out of band.
	deliveryManualLink = "manual_link"
	// deliveryUnknown: the record carries nothing to classify on (no email, no
	// send attempt, no creation time), so the control plane did not tell us how
	// the invite was delivered. Deliberately NOT folded into manual_link — a
	// provider that omits these fields must read as unknown, not as a confident
	// "shared by link".
	deliveryUnknown = "unknown"
)

// keyExpiryBucketsDays are the explicit histogram bucket boundaries (in days)
// for tailscale.devices.key_expiry. The first bucket (-inf, 0] captures keys
// that have already expired; the rest bracket "expiring soon" windows.
var keyExpiryBucketsDays = []float64{0, 7, 30, 90, 180, 365}

// keyExpiryWarnDays is the fixed look-ahead window (in days) within which a
// device's node key triggers the per-device tailscale.device.key_expiring WARN
// log event. Keys expiring further than this threshold in the future are
// covered by the fleet-wide histogram only.
const (
	keyExpiryWarnDays      = 14
	expiryReminderInterval = 24 * time.Hour
	expiryLogModeDaily     = "daily"
	expiryLogModeAlways    = "always"
	expiryLogModeOff       = "off"
)

type expiryState struct {
	expiresAt   time.Time
	lastEmitted time.Time
}

type attributeExpiryKey struct {
	deviceID  string
	attribute string
}

type postureResult struct {
	device     *tsapi.RichDevice
	attributes tsapi.DeviceAttributes
}

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	DevicesRich(ctx context.Context) ([]tsapi.RichDevice, error)
	DevicePostureAttributes(ctx context.Context, deviceID string) (tsapi.DeviceAttributes, error)
	DeviceInvites(ctx context.Context, deviceID string) ([]tsapi.DeviceInvite, error)
}

// Collector implements collector.SnapshotCollector for the device inventory.
type Collector struct {
	api                  api
	cache                *enrich.DeviceCache
	interval             time.Duration
	collectRoutes        bool
	collectPosture       bool
	collectDeviceInvites bool
	perEntity            bool
	derpRollup           bool
	collectConnectivity  bool
	// geo supplies country/continent for a device's public magicsock endpoint;
	// nil disables the fleet-geography gauge entirely.
	geo               geoip.Lookup
	subnetRouteRollup bool
	postureLogMode    string // "changes" (default) | "always" | "off"
	expiryLogMode     string // "daily" (default) | "always" | "off"

	// attrNamespaces is the set of posture-attribute namespace prefixes (the part
	// before ":") promoted to the tailscale.device.attribute{,.info} metrics, and
	// attrNamespaceWildcard promotes every namespace present. Empty set +
	// non-wildcard disables the attribute metrics. Built by WithAttributeNamespaces.
	attrNamespaces        map[string]bool
	attrNamespaceWildcard bool
	// attributeKeyLimit caps the selected posture keys across the fleet and
	// attributeValueLimit caps distinct string values per selected key. Both
	// are unlimited when non-positive. Set by WithAttributeLimits.
	attributeKeyLimit   int
	attributeValueLimit int

	// lastPosture remembers each device's last-emitted posture signature
	// (deviceID -> signature) so the posture LOG can fire on-change only. A
	// device absent from the map is first-seen and counts as changed.
	lastPosture map[string]string

	// Expiry state is retained only while an entity remains inside the warning
	// window. The maps are pruned to the current tick's expiring entities after
	// each successful collection, so device churn cannot grow them forever.
	deviceKeyExpiryState map[string]expiryState
	attributeExpiryState map[attributeExpiryKey]expiryState

	// collectTagRollup gates the tailscale.devices.by_tag distribution gauge;
	// tagRollupLimit caps its distinct tag series (<=0 = unlimited, overflow folds
	// into tagOther). Set by WithTagRollup; default on/50.
	collectTagRollup bool
	tagRollupLimit   int

	// updateAvailableData gates the per-device tailscale.device.update_available
	// gauge; ephemeralData gates the tailscale.devices.ephemeral fleet
	// aggregate. Both default true (Tailscale populates both source fields);
	// set false when the control-plane API has no such field at all (e.g.
	// Headscale), so the collector doesn't report a fabricated false/0 as if it
	// were real data. See WithUpdateAvailableData / WithEphemeralData.
	updateAvailableData bool
	ephemeralData       bool

	// multipleConnectionsData / blocksIncomingConnectionsData gate the
	// per-device tailscale.device.multiple_connections /
	// .blocks_incoming_connections gauges, same rationale as
	// updateAvailableData/ephemeralData above: both default true (Tailscale
	// reports these fields natively), set false when the control-plane API has
	// no such concept at all (e.g. Headscale), so the collector doesn't report
	// a fabricated "false" as if it were real data. tailscale.device.posture_identity.disabled
	// needs no such option — it is gated purely by wire presence (nil
	// PostureIdentity), which Headscale already satisfies since its adapter
	// never populates the field.
	multipleConnectionsData       bool
	blocksIncomingConnectionsData bool

	// sshData / keyExpiryDisabledData gate the tailscale.device{s}.ssh_enabled
	// and .key_expiry_disabled signals (#414), same rationale again: both
	// default true because Tailscale reports sshEnabled/keyExpiryDisabled
	// natively, and both must be set false for a control plane that has no such
	// concept (Headscale's adapter hard-codes them false — see
	// internal/hsapi/adapt.go), so an unsupported provider produces ABSENCE
	// rather than a fleet that confidently reports "SSH nowhere enabled".
	// tailscale.devices.by_distro needs no such option: it is gated on wire
	// presence of a distro name, which Headscale never populates.
	sshData               bool
	keyExpiryDisabledData bool

	// coverage tallies the per-device subrequests (posture attributes, device
	// invites) attempted and their outcomes for #421. Nil is a no-op, so a
	// collector built without WithCoverage behaves exactly as before.
	coverage *apistate.Coverage

	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker

	// ageBuckets is this collector's own copy of the shared entity-age bucket
	// bounds (#426), taken once in New rather than per observation. The OTEL SDK
	// retains the bounds slice it is handed, and entityage.BucketsSeconds returns
	// a fresh copy per call precisely so no two collectors can end up sharing
	// one — holding a single copy here preserves that while keeping the
	// per-device hot loop allocation-free.
	ageBuckets []float64

	// now returns the current time; injectable for tests (key-expiry histogram).
	now func() time.Time

	// upstreamLatest returns the latest upstream Tailscale stable version and
	// whether it is known yet; nil disables all version-skew metrics (B6).
	// outdatedThreshold is the minor-skew >= which a device counts as outdated.
	upstreamLatest    func() (string, bool)
	outdatedThreshold int

	// gsb accumulates every CHURNING (attribute-keyed) gauge this collector emits
	// and flushes them as observable-gauge snapshots each tick, so a device that
	// leaves the tailnet (or a version/tag/region/CIDR that stops appearing) drops
	// out of the export instead of ghosting at its last value forever (#55). Only
	// nil-attr single-series gauges (devices.count totals, etc.) stay synchronous.
	// A collector runs its Collect on a single goroutine, so the builder needs no
	// synchronization.
	gsb *telemetry.GaugeSnapshotBuilder
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-device gauges (online, last_seen,
// key.expiry, update_available, DERP latency, routes) are emitted. The default
// is true; false (cardinality.per_entity.device) emits only the aggregate
// tailscale.devices.count rollup, dropping the per-device series.
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// WithDerpRegionRollup controls whether the tailnet-wide per-DERP-region rollup
// gauges (latency_min, devices, preferred) are emitted (default true;
// cardinality.derp_region_rollup). The rollup is computed from the per-device
// DERP latency already fetched and is emitted independently of per_entity, so it
// is the low-cardinality DERP view that survives when cardinality.per_entity.device is off.
func WithDerpRegionRollup(enabled bool) Option {
	return func(c *Collector) { c.derpRollup = enabled }
}

// WithConnectivity gates the B3 connectivity signals (per-device hard_nat /
// endpoints / direct_capable / udp / ipv6 gauges and the fleet connectivity
// rollups). Read from the already-fetched devices payload — no extra API calls.
// Default true. Per-device gauges are additionally gated by per_entity.device.
func WithConnectivity(enabled bool) Option {
	return func(c *Collector) { c.collectConnectivity = enabled }
}

// WithGeo supplies the GeoIP databases used to derive fleet geography from each
// device's advertised public endpoint. Nil (the default) leaves
// tailscale.devices.by_country unemitted.
func WithGeo(g geoip.Lookup) Option {
	return func(c *Collector) { c.geo = g }
}

// WithSubnetRouteRollup gates the per-CIDR tailscale.subnet_routes.routers
// redundancy gauge (cardinality.subnet_route_rollup, default true). The fleet
// exit/subnet count aggregates are emitted regardless.
func WithSubnetRouteRollup(enabled bool) Option {
	return func(c *Collector) { c.subnetRouteRollup = enabled }
}

// WithDeviceInvites controls whether the collector fetches each device's share
// invites (GET /device/{id}/device-invites, one API call per device — N+1) and
// emits the tailscale.device_invites.count aggregate. Default false at the
// collector level; config (collect_device_invites) defaults it on. Requires the
// device_invites:read OAuth scope. Per-device fetch errors are non-fatal.
func WithDeviceInvites(enabled bool) Option {
	return func(c *Collector) { c.collectDeviceInvites = enabled }
}

// WithTagRollup controls the tailscale.devices.by_tag distribution gauge.
// enabled gates it (collect_tag_rollup, default on); limit caps the distinct tag
// series (tag_rollup_limit, default 50): the busiest `limit` tags keep their own
// series and the rest fold into a single tailscale.tag="__other__" series so
// totals are preserved. A limit <= 0 means unlimited (no cap).
func WithTagRollup(enabled bool, limit int) Option {
	return func(c *Collector) {
		c.collectTagRollup = enabled
		c.tagRollupLimit = limit
	}
}

// WithClock overrides the time source used for the key-expiry histogram
// (default time.Now). Intended for tests.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) {
		if now != nil {
			c.now = now
		}
	}
}

// WithUpstreamLatest supplies the latest upstream Tailscale stable version
// (typically backed by a release.Fetcher) and the minor-skew threshold at which
// a device counts toward tailscale.devices.outdated. When the provider is nil
// the per-device/fleet version-skew metrics (B6) are not emitted at all.
func WithUpstreamLatest(latest func() (string, bool), outdatedThreshold int) Option {
	return func(c *Collector) {
		c.upstreamLatest = latest
		c.outdatedThreshold = outdatedThreshold
	}
}

// WithPostureLogMode controls how often the per-device posture LOG event fires
// (it does not affect the posture info-gauge metric, which is emitted every
// scrape when collect_posture is on):
//
//   - "changes" (default): emit the posture log for a device only when its
//     posture changed since the previous scrape; a first-seen device counts as
//     changed, so process start dumps a full baseline, then only deltas.
//   - "always": emit the posture log every scrape (the legacy behavior).
//   - "off": never emit the posture log (the info-gauge metric is still emitted).
//
// An empty or unrecognized mode falls back to "changes".
func WithPostureLogMode(mode string) Option {
	return func(c *Collector) { c.postureLogMode = normalizePostureLogMode(mode) }
}

// normalizePostureLogMode maps an arbitrary mode string to a known mode,
// defaulting unknown/empty values to "changes".
func normalizePostureLogMode(mode string) string {
	switch mode {
	case postureLogAlways, postureLogOff, postureLogChanges:
		return mode
	default:
		return postureLogChanges
	}
}

// WithExpiryLogMode controls the cadence of both node-key and posture-attribute
// expiry WARN events. The metrics remain emitted in every mode:
//
//   - "daily" (default): emit on first entry or expiry-time change, then at
//     most one reminder per 24 hours.
//   - "always": emit on every scrape (the legacy behavior).
//   - "off": suppress expiry WARN events.
//
// An empty or unrecognized mode falls back to "daily".
func WithExpiryLogMode(mode string) Option {
	return func(c *Collector) { c.expiryLogMode = normalizeExpiryLogMode(mode) }
}

// normalizeExpiryLogMode maps an arbitrary mode string to a known mode,
// defaulting unknown/empty values to "daily".
func normalizeExpiryLogMode(mode string) string {
	switch mode {
	case expiryLogModeDaily, expiryLogModeAlways, expiryLogModeOff:
		return mode
	default:
		return expiryLogModeDaily
	}
}

// expiryLogDue records the current expiry timestamp and reports whether a WARN
// should be emitted for id. State is written only for the daily mode; always
// and off retain the legacy/suppressed behavior without building a second
// cadence.
func expiryLogDue[K comparable](mode string, states map[K]expiryState, id K, expiresAt, now time.Time) bool {
	switch mode {
	case expiryLogModeAlways:
		return true
	case expiryLogModeOff:
		return false
	}

	state, seen := states[id]
	due := !seen || !state.expiresAt.Equal(expiresAt) ||
		now.Sub(state.lastEmitted) >= expiryReminderInterval
	state.expiresAt = expiresAt
	if due {
		state.lastEmitted = now
	}
	states[id] = state
	return due
}

// WithAttributeNamespaces sets the posture-attribute namespace allow-list: each
// entry is a namespace prefix (the part before ":" in a posture key, e.g.
// "intune", "ip") whose attributes are promoted to the
// tailscale.device.attribute{,.info} metrics. The sentinel "*" promotes every
// namespace present (including node and custom). An empty list (the default)
// disables the attribute metrics; the posture info-gauge and posture log are
// unaffected. Requires collect_posture (which fetches the attributes) — no extra
// API calls are made, the already-fetched attribute map is reused.
func WithAttributeNamespaces(ns []string) Option {
	return func(c *Collector) {
		c.attrNamespaces = make(map[string]bool, len(ns))
		c.attrNamespaceWildcard = false
		for _, n := range ns {
			if n == "*" {
				c.attrNamespaceWildcard = true
				continue
			}
			if n = strings.TrimSpace(n); n != "" {
				c.attrNamespaces[n] = true
			}
		}
	}
}

// WithAttributeLimits bounds the posture attributes promoted by
// WithAttributeNamespaces. The busiest keys (and, for .info, values per key)
// are retained; ties are lexical. Keys beyond keyLimit are dropped from all
// three attribute metric families, while values beyond valueLimit fold into
// value="__other__". A non-positive limit is unlimited.
func WithAttributeLimits(keyLimit, valueLimit int) Option {
	return func(c *Collector) {
		c.attributeKeyLimit = keyLimit
		c.attributeValueLimit = valueLimit
	}
}

// WithUpdateAvailableData controls whether the per-device
// tailscale.device.update_available gauge is emitted. The default is true
// (Tailscale reports this field natively); pass false when the control-plane
// API does not report available-update status at all (e.g. Headscale has no
// such concept), so the collector doesn't report a fabricated "no update
// available" (false) as if it were real data. Per-device gauges are
// additionally gated by WithPerEntity.
func WithUpdateAvailableData(enabled bool) Option {
	return func(c *Collector) { c.updateAvailableData = enabled }
}

// WithEphemeralData controls whether the tailscale.devices.ephemeral
// fleet-aggregate gauge is emitted. The default is true (Tailscale reports
// per-device ephemeral status natively); pass false when the control-plane API
// does not report it at all (e.g. Headscale's node listing has no ephemeral
// field), so the collector doesn't report a fabricated all-non-ephemeral count
// as if it were real data.
func WithEphemeralData(enabled bool) Option {
	return func(c *Collector) { c.ephemeralData = enabled }
}

// WithMultipleConnectionsData controls whether the per-device
// tailscale.device.multiple_connections gauge is emitted. The default is true
// (Tailscale reports this field natively); pass false when the control-plane
// API does not report it at all (e.g. Headscale), so the collector doesn't
// report a fabricated "no multiple connections" (false) as if it were real
// data. Per-device gauges are additionally gated by WithPerEntity.
func WithMultipleConnectionsData(enabled bool) Option {
	return func(c *Collector) { c.multipleConnectionsData = enabled }
}

// WithBlocksIncomingConnectionsData controls whether the per-device
// tailscale.device.blocks_incoming_connections gauge is emitted. The default
// is true (Tailscale reports this field natively); pass false when the
// control-plane API does not report it at all (e.g. Headscale), so the
// collector doesn't report a fabricated "does not block incoming" (false) as
// if it were real data. Per-device gauges are additionally gated by
// WithPerEntity.
func WithBlocksIncomingConnectionsData(enabled bool) Option {
	return func(c *Collector) { c.blocksIncomingConnectionsData = enabled }
}

// WithSSHData controls whether the Tailscale SSH posture signals
// (tailscale.devices.ssh_enabled fleet count + the per-device
// tailscale.device.ssh_enabled gauge) are emitted. The default is true
// (Tailscale reports `sshEnabled` natively); pass false when the control-plane
// API has no Tailscale-SSH concept at all (e.g. Headscale), so the collector
// doesn't publish a fabricated "SSH enabled nowhere" as if it were measured.
// The per-device gauge is additionally gated by WithPerEntity.
func WithSSHData(enabled bool) Option {
	return func(c *Collector) { c.sshData = enabled }
}

// WithKeyExpiryDisabledData controls whether the key-expiry-disabled posture
// signals (tailscale.devices.key_expiry_disabled fleet count + the per-device
// tailscale.device.key_expiry_disabled gauge) are emitted. Default true; pass
// false for a control plane that does not report whether key expiry has been
// disabled for a node (Headscale). Same absence-over-false-zero rationale as
// WithSSHData. Note this gates only the NEW posture signals — the existing
// tailscale.device.key.expiry gauge and tailscale.devices.key_expiry histogram
// already suppress themselves per device via the KeyExpiryDisabled flag and are
// unaffected.
func WithKeyExpiryDisabledData(enabled bool) Option {
	return func(c *Collector) { c.keyExpiryDisabledData = enabled }
}

// WithCoverage supplies the shared per-tailnet subrequest coverage tally (#421).
// The devices collector makes one posture-attributes and one device-invites call
// PER DEVICE; before this, a failure on either was discarded silently, so a
// missing device_invites:read scope read as "this tailnet has no invites" while
// the scrape still reported success. With a Coverage wired, every attempt is
// classified through apistate and emitted as
// tailscale2otel.subrequest.{attempts,failures,coverage}, and the same tally
// backs the admin status page.
//
// A nil *apistate.Coverage is a no-op (and emits nothing), so existing
// constructions keep working unchanged.
func WithCoverage(cov *apistate.Coverage) Option {
	return func(c *Collector) { c.coverage = cov }
}

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// New returns a devices Collector that lists via the rich devices endpoint,
// repopulates cache, and uses interval as its poll cadence (a non-positive
// interval defaults to 60s). When collectRoutes is true the per-device route
// gauges are emitted (read from the inline route slices, no extra API call).
// When collectPosture is true the collector additionally calls
// DevicePostureAttributes once per device (N API calls per tick) and emits a
// posture log event per device; it is off by default. Options (e.g.
// WithPerEntity) tune cardinality; per-entity gauges are emitted by default.
func New(api api, cache *enrich.DeviceCache, interval time.Duration, collectRoutes, collectPosture bool, opts ...Option) *Collector {
	c := &Collector{
		api:                  api,
		cache:                cache,
		interval:             interval,
		collectRoutes:        collectRoutes,
		collectPosture:       collectPosture,
		perEntity:            true,
		derpRollup:           true,
		collectConnectivity:  true,
		subnetRouteRollup:    true,
		postureLogMode:       postureLogChanges,
		lastPosture:          make(map[string]string),
		expiryLogMode:        expiryLogModeDaily,
		deviceKeyExpiryState: make(map[string]expiryState),
		attributeExpiryState: make(map[attributeExpiryKey]expiryState),
		collectTagRollup:     true,
		tagRollupLimit:       50,
		now:                  time.Now,
		updateAvailableData:  true,
		ephemeralData:        true,

		multipleConnectionsData:       true,
		blocksIncomingConnectionsData: true,
		sshData:                       true,
		keyExpiryDisabledData:         true,

		ageBuckets: entityage.BucketsSeconds(),

		gsb: telemetry.NewGaugeSnapshotBuilder(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "devices" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect lists devices, repopulates the cache, and emits metrics (and, when
// enabled, route gauges and posture log events).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	// Start each tick from a clean coverage tally so the emitted ratio describes
	// the most recent pass rather than an ever-growing lifetime total. Reset
	// before the list call on purpose: if the list itself fails there was no
	// pass, and an empty tally is the honest answer (nothing is emitted, so the
	// last good values stand until the next successful tick).
	c.coverage.ResetCollector(c.Name())

	devs, err := c.api.DevicesRich(ctx)
	apistate.Observe(e, c.tracker, c.Name(), opListTailnetDevices, apistate.Disposition{}, err, c.now())
	if err != nil {
		return err
	}

	c.cache.Replace(toMetas(devs))
	// cache_age is NOT emitted here: right after Replace it is always ~0, so a
	// last-value gauge could never grow to reveal a stalled devices collector. It is
	// emitted at export time by the app-level enrich-cache-age reporter instead (#108).
	e.Gauge(docCacheSize.Name, docCacheSize.Unit, docCacheSize.Description,
		float64(c.cache.Len()), nil)

	type countKey struct {
		os         string
		authorized bool
		external   bool
	}
	counts := make(map[countKey]int, len(devs))
	lockErrors := 0
	inviteCounts := map[deviceInviteKey]int{}

	// Fleet-hygiene roll-up accumulators (aggregate, low-cardinality).
	untagged := 0
	ephemeral := 0
	byVersion := make(map[string]int)
	byTag := make(map[string]int)
	nowT := c.now()

	// #524: aggregate the per-tick outcome of each per-device subrequest into
	// ONE apistate.Observe call after the loop — apistate.Tracker holds one
	// entry per (collector, operation), not per device, so N per-device calls
	// must collapse to a single observation. *Attempted is set the first time
	// the corresponding collectX flag fires in the loop below; *FirstErr keeps
	// the FIRST non-nil error seen (these failures are systemic — a missing
	// scope 403s every device — so first-error is as representative as any
	// other choice and there is no need to rank them). Zero attempts this tick
	// (subrequest disabled, or no devices) leaves *Attempted false, and no
	// Observe call is made for it: a probe that never happened must stay
	// unknown, never fabricate a state.
	var (
		postureAttempted bool
		postureFirstErr  error
		inviteAttempted  bool
		inviteFirstErr   error
	)

	// #414 posture roll-ups and #427 distro inventory accumulators.
	sshEnabled := 0
	keyExpiryDisabled := 0
	byDistro := make(map[distroKey]int)

	// B6 version-skew: resolve the latest upstream stable once per tick.
	var latestVer release.Version
	haveLatest := false
	var latestNorm string
	if c.upstreamLatest != nil {
		if raw, ok := c.upstreamLatest(); ok {
			if v, vok := release.Parse(raw); vok {
				latestVer, haveLatest, latestNorm = v, true, release.Normalize(raw)
			}
		}
	}
	outdated := 0

	// B3 fleet connectivity + B4 routing accumulators.
	hardNATCount := 0
	directCapableCount := 0
	capSupports := map[string]int{} // capability -> count of devices supporting
	exitAdvertised := 0
	exitEnabled := 0
	subnetAdvertised := map[string]struct{}{}
	subnetEnabled := map[string]struct{}{}
	routersByCIDR := map[string]int{}
	currentDeviceKeyExpiries := make(map[string]struct{})
	currentAttributeExpiries := make(map[attributeExpiryKey]struct{})
	postureResults := make([]postureResult, 0, len(devs))

	for i := range devs {
		d := &devs[i]

		// Per-device gauges (one series per device) are gated by
		// cardinality.per_entity.device; when off, only the aggregate
		// devices.count rollup below is emitted.
		if c.perEntity {
			idAttrs := telemetry.Attrs{
				semconv.HostName: d.Hostname,
				semconv.HostID:   d.ID,
				semconv.OSType:   d.OS,
				semconv.AttrUser: d.User,
			}
			if d.Distro.Version != "" {
				idAttrs[semconv.OSVersion] = d.Distro.Version
			}
			// tailscale.tags: comma-joined ACL tags (matches the node-metrics
			// label in nodediscovery.go). Omitted for untagged devices, like
			// os.version above. Functionally dependent on the device, so it adds
			// no new series — just a richer label on the existing per-device ones.
			if len(d.Tags) > 0 {
				idAttrs[semconv.AttrTags] = strings.Join(d.Tags, ",")
			}

			c.gsb.Add(docOnline.Name, docOnline.Unit, docOnline.Description,
				boolToFloat(d.ConnectedToControl), idAttrs)

			if !d.LastSeen.IsZero() {
				c.gsb.Add(docLastSeen.Name, docLastSeen.Unit, docLastSeen.Description,
					float64(d.LastSeen.Unix()), idAttrs)
			}

			if !d.KeyExpiryDisabled && !d.Expires.IsZero() {
				c.gsb.Add(docKeyExpiry.Name, docKeyExpiry.Unit, docKeyExpiry.Description,
					float64(d.Expires.Unix()), idAttrs)
			}

			// Gated by updateAvailableData: some control planes (Headscale) don't
			// report update-available status at all, so emitting it
			// unconditionally would report a fabricated "no update available" as
			// if it were real data (issue #64).
			if c.updateAvailableData {
				c.gsb.Add(docUpdateAvailable.Name, docUpdateAvailable.Unit, docUpdateAvailable.Description,
					boolToFloat(d.UpdateAvailable), idAttrs)
			}

			// Gated by multipleConnectionsData/blocksIncomingConnectionsData: same
			// rationale as updateAvailableData above (Headscale reports neither).
			if c.multipleConnectionsData {
				c.gsb.Add(docMultipleConnections.Name, docMultipleConnections.Unit, docMultipleConnections.Description,
					boolToFloat(d.MultipleConnections), idAttrs)
			}
			if c.blocksIncomingConnectionsData {
				c.gsb.Add(docBlocksIncomingConnections.Name, docBlocksIncomingConnections.Unit, docBlocksIncomingConnections.Description,
					boolToFloat(d.BlocksIncomingConnections), idAttrs)
			}

			// #414: Tailscale SSH + key-expiry-disabled posture, each gated on
			// its own data-source option so an unsupported control plane emits
			// nothing rather than a fabricated false.
			if c.sshData {
				c.gsb.Add(docDeviceSSHEnabled.Name, docDeviceSSHEnabled.Unit, docDeviceSSHEnabled.Description,
					boolToFloat(d.SSHEnabled), idAttrs)
			}
			if c.keyExpiryDisabledData {
				c.gsb.Add(docDeviceKeyExpiryDisabled.Name, docDeviceKeyExpiryDisabled.Unit, docDeviceKeyExpiryDisabled.Description,
					boolToFloat(d.KeyExpiryDisabled), idAttrs)
			}

			// #427: per-device distro info gauge. Emitted only for devices that
			// actually report a distribution — most non-Linux devices send an
			// empty distro block, and a per-device "unknown" series for each of
			// them would be pure noise.
			if dk, ok := distroKeyFor(d.Distro); ok {
				c.gsb.Add(docDeviceDistro.Name, docDeviceDistro.Unit, docDeviceDistro.Description,
					1, telemetry.Attrs{
						semconv.HostName:   d.Hostname,
						semconv.HostID:     d.ID,
						attrDistroName:     dk.name,
						attrDistroCodename: dk.codename,
					})
			}

			// tailscale.device.posture_identity.disabled is gated purely by wire
			// presence, not a data-source option: Headscale's adapter never
			// populates PostureIdentity (stays nil), so this already suppresses
			// itself for control planes with no such concept.
			if d.PostureIdentity != nil {
				c.gsb.Add(docPostureIdentityDisabled.Name, docPostureIdentityDisabled.Unit, docPostureIdentityDisabled.Description,
					boolToFloat(d.PostureIdentity.Disabled), idAttrs)
			}

			for region, derp := range d.DERPLatency {
				c.gsb.Add(docDERPLatency.Name, docDERPLatency.Unit, docDERPLatency.Description,
					derp.LatencyMs/1000, telemetry.Attrs{
						semconv.HostName:  d.Hostname,
						semconv.HostID:    d.ID,
						attrDERPRegion:    region,
						attrDERPPreferred: derp.Preferred,
					})
			}

			if c.collectRoutes {
				routeAttrs := telemetry.Attrs{
					semconv.HostName: d.Hostname,
					semconv.HostID:   d.ID,
				}
				c.gsb.Add(docRoutesAdvertised.Name, docRoutesAdvertised.Unit, docRoutesAdvertised.Description,
					float64(len(d.AdvertisedRoutes)), routeAttrs)
				c.gsb.Add(docRoutesEnabled.Name, docRoutesEnabled.Unit, docRoutesEnabled.Description,
					float64(len(d.EnabledRoutes)), routeAttrs)
			}

			if c.collectConnectivity {
				connAttrs := telemetry.Attrs{semconv.HostName: d.Hostname, semconv.HostID: d.ID}
				c.gsb.Add(docConnHardNAT.Name, docConnHardNAT.Unit, docConnHardNAT.Description,
					boolToFloat(d.HardNAT), connAttrs)
				c.gsb.Add(docConnEndpoints.Name, docConnEndpoints.Unit, docConnEndpoints.Description,
					float64(len(d.Endpoints)), connAttrs)
				if d.ClientSupports.UDP != nil {
					udp := boolPtrTrue(d.ClientSupports.UDP)
					c.gsb.Add(docConnUDP.Name, docConnUDP.Unit, docConnUDP.Description,
						boolToFloat(udp), connAttrs)
					c.gsb.Add(docConnDirectCapable.Name, docConnDirectCapable.Unit, docConnDirectCapable.Description,
						boolToFloat(udp && !d.HardNAT), connAttrs)
				}
				if d.ClientSupports.IPv6 != nil {
					c.gsb.Add(docConnIPv6.Name, docConnIPv6.Unit, docConnIPv6.Description,
						boolToFloat(boolPtrTrue(d.ClientSupports.IPv6)), connAttrs)
				}
			}
		}

		if c.collectPosture {
			postureAttempted = true
			if da, perr := c.emitPosture(ctx, e, d); perr != nil {
				if postureFirstErr == nil {
					postureFirstErr = perr
				}
			} else {
				postureResults = append(postureResults, postureResult{device: d, attributes: da})
			}
		}

		if c.collectDeviceInvites {
			inviteAttempted = true
			if ierr := c.tallyDeviceInvites(ctx, e, d, inviteCounts, nowT); ierr != nil && inviteFirstErr == nil {
				inviteFirstErr = ierr
			}
		}

		if d.TailnetLockError != "" {
			lockErrors++
			e.LogEvent(telemetry.Event{
				Name:     docTailnetLockError.Name,
				Severity: telemetry.SeverityError,
				Body:     d.TailnetLockError,
				// Raw upstream error text — free-text; drop the body when free_text_details is off (#197).
				BodyPII: []pii.Category{pii.CatFreeTextDetails},
				Attrs:   telemetry.Attrs{semconv.HostName: d.Hostname, semconv.HostID: d.ID},
			})
		}

		counts[countKey{os: d.OS, authorized: d.Authorized, external: d.IsExternal}]++

		// Fleet-hygiene roll-ups (aggregate, always computed; low-cardinality).
		if d.IsEphemeral {
			ephemeral++
		}
		if len(d.Tags) == 0 {
			// External (shared-in) devices can't be tagged by this tailnet, so
			// they aren't a tagging-hygiene signal — exclude them from untagged.
			if !d.IsExternal {
				untagged++
			}
		} else if c.collectTagRollup {
			for _, tag := range d.Tags {
				byTag[tag]++
			}
		}
		if d.ClientVersion != "" {
			byVersion[NormalizeVersion(d.ClientVersion)]++
		}

		// #414 fleet posture counts (independent of per_entity, so the
		// low-cardinality view survives when the per-device gauges are off).
		if d.SSHEnabled {
			sshEnabled++
		}
		if d.KeyExpiryDisabled {
			keyExpiryDisabled++
		}

		// #427 fleet distro distribution.
		if dk, ok := distroKeyFor(d.Distro); ok {
			byDistro[dk]++
		}

		// #426 fleet age distribution. A device whose provider supplied no
		// creation timestamp is SKIPPED, never recorded as age 0 — a fleet of
		// apparently brand-new devices is a materially wrong answer, and
		// external/shared-in devices legitimately arrive with created:"".
		if age, ok := entityage.Seconds(d.Created, nowT); ok {
			e.Histogram(docDevicesAge.Name, docDevicesAge.Unit, docDevicesAge.Description,
				age, c.ageBuckets, nil)
		}

		// B6 version-skew per-device (gated by perEntity; outdated count always).
		if haveLatest {
			if dv, ok := release.Parse(d.ClientVersion); ok {
				skew := release.MinorsBehind(dv, latestVer)
				if c.perEntity {
					skewAttrs := telemetry.Attrs{
						semconv.HostName: d.Hostname,
						semconv.HostID:   d.ID,
						semconv.OSType:   d.OS,
						semconv.AttrUser: d.User,
					}
					if d.Distro.Version != "" {
						skewAttrs[semconv.OSVersion] = d.Distro.Version
					}
					if len(d.Tags) > 0 {
						skewAttrs[semconv.AttrTags] = strings.Join(d.Tags, ",")
					}
					c.gsb.Add(docDeviceVersionSkew.Name, docDeviceVersionSkew.Unit, docDeviceVersionSkew.Description,
						float64(skew), skewAttrs)
				}
				if skew >= c.outdatedThreshold {
					outdated++
				}
			}
		}

		if c.collectConnectivity {
			if d.HardNAT {
				hardNATCount++
			}
			if boolPtrTrue(d.ClientSupports.UDP) && !d.HardNAT {
				directCapableCount++
			}
			for capName, p := range map[string]*bool{
				"udp": d.ClientSupports.UDP, "ipv6": d.ClientSupports.IPv6,
				"pcp": d.ClientSupports.PCP, "pmp": d.ClientSupports.PMP, "upnp": d.ClientSupports.UPnP,
			} {
				if boolPtrTrue(p) {
					capSupports[capName]++
				}
			}
		}

		// Routing analytics (B4) — derived from the inline route slices.
		advertisesExit := false
		enabledExit := false
		// seenCIDR de-dupes a device's own advertised routes so a device that
		// lists the same subnet twice contributes only one router to that CIDR's
		// redundancy count (AdvertisedRoutes is a raw []string with no uniqueness
		// guarantee).
		seenCIDR := map[string]struct{}{}
		for _, r := range d.AdvertisedRoutes {
			if isExitRoute(r) {
				advertisesExit = true
			} else {
				subnetAdvertised[r] = struct{}{}
				if _, dup := seenCIDR[r]; !dup {
					seenCIDR[r] = struct{}{}
					routersByCIDR[r]++
				}
			}
		}
		for _, r := range d.EnabledRoutes {
			if isExitRoute(r) {
				enabledExit = true
			} else {
				subnetEnabled[r] = struct{}{}
			}
		}
		if advertisesExit {
			exitAdvertised++
			if enabledExit {
				exitEnabled++
			}
			if c.perEntity {
				c.gsb.Add(docDeviceExitNode.Name, docDeviceExitNode.Unit, docDeviceExitNode.Description,
					1, telemetry.Attrs{
						semconv.HostName:    d.Hostname,
						semconv.HostID:      d.ID,
						attrExitNodeEnabled: enabledExit,
					})
			}
		}

		if !d.KeyExpiryDisabled && !d.Expires.IsZero() {
			days := d.Expires.Sub(nowT).Hours() / 24
			e.Histogram(docDevicesKeyExpiry.Name, docDevicesKeyExpiry.Unit, docDevicesKeyExpiry.Description,
				days, keyExpiryBucketsDays, nil)
			// Emit a per-device WARN log when the key expires within the warn
			// window and has not yet expired (days > 0). The histogram already
			// covers the full distribution (including already-expired keys);
			// this log is the actionable per-device signal.
			if days > 0 && days <= keyExpiryWarnDays {
				currentDeviceKeyExpiries[d.ID] = struct{}{}
				if expiryLogDue(c.expiryLogMode, c.deviceKeyExpiryState, d.ID, d.Expires, nowT) {
					e.LogEvent(telemetry.Event{
						Name:     docDeviceKeyExpiryLog.Name,
						Severity: telemetry.SeverityWarn,
						Body:     "device node key expiring soon",
						Attrs: telemetry.Attrs{
							semconv.HostName:           d.Hostname,
							semconv.HostID:             d.ID,
							attrDeviceKeyExpiresInDays: fmt.Sprintf("%.2f", days),
						},
					})
				}
			}
		}
	}

	// Attribute key ranking is fleet-wide, so it intentionally follows the
	// per-device posture fetches rather than emitting opportunistically in that
	// loop. That gives every successful fetch this tick an equal vote and keeps
	// .attribute, .attribute.info and .attribute.expiry on the same key set.
	droppedAttributeKeys := c.emitAttributes(e, postureResults, nowT, currentAttributeExpiries)
	e.Gauge(docAttributesDropped.Name, docAttributesDropped.Unit, docAttributesDropped.Description,
		float64(droppedAttributeKeys), nil)

	// #61: prune lastPosture to the current tick's fleet so it does not retain
	// entries for devices that have left the tailnet (it grows unbounded
	// otherwise under high device churn). A device absent this tick loses its
	// remembered posture signature; if it later rejoins, postureLogChanges
	// treats it as first-seen again (a fresh baseline log) rather than silently
	// comparing against a stale signature from a device that, from the
	// collector's perspective, no longer existed in between.
	if len(c.lastPosture) > 0 {
		current := make(map[string]struct{}, len(devs))
		for i := range devs {
			current[devs[i].ID] = struct{}{}
		}
		for id := range c.lastPosture {
			if _, ok := current[id]; !ok {
				delete(c.lastPosture, id)
			}
		}
	}

	// Expiry state is intentionally narrower than lastPosture: an entity that
	// leaves the warning window must be forgotten so a later re-entry emits a
	// fresh change warning. Attribute state is keyed by device plus attribute,
	// since one device can carry multiple independently expiring attributes.
	for id := range c.deviceKeyExpiryState {
		if _, ok := currentDeviceKeyExpiries[id]; !ok {
			delete(c.deviceKeyExpiryState, id)
		}
	}
	for id := range c.attributeExpiryState {
		if _, ok := currentAttributeExpiries[id]; !ok {
			delete(c.attributeExpiryState, id)
		}
	}

	for k, n := range counts {
		c.gsb.Add(docDevicesCount.Name, docDevicesCount.Unit, docDevicesCount.Description,
			float64(n), telemetry.Attrs{
				semconv.OSType: k.os,
				attrAuthorized: k.authorized,
				attrExternal:   k.external,
			})
	}

	e.Gauge(docTailnetLockErrors.Name, docTailnetLockErrors.Unit, docTailnetLockErrors.Description,
		float64(lockErrors), nil)

	// Fleet-hygiene aggregate gauges (always emitted; low cardinality).
	e.Gauge(docDevicesUntagged.Name, docDevicesUntagged.Unit, docDevicesUntagged.Description,
		float64(untagged), nil)
	// Gated by ephemeralData: some control planes (Headscale) don't report
	// per-device ephemeral status at all, so emitting this aggregate
	// unconditionally would report a fabricated all-non-ephemeral count as if
	// it were real data (issue #64).
	if c.ephemeralData {
		e.Gauge(docDevicesEphemeral.Name, docDevicesEphemeral.Unit, docDevicesEphemeral.Description,
			float64(ephemeral), nil)
	}
	for ver, n := range byVersion {
		c.gsb.Add(docDevicesByVersion.Name, docDevicesByVersion.Unit, docDevicesByVersion.Description,
			float64(n), telemetry.Attrs{attrClientVersion: ver})
	}
	c.emitTagRollup(byTag)

	// #414 fleet posture counts. Nil-attr single-series gauges, so they stay
	// synchronous — they cannot churn.
	if c.sshData {
		e.Gauge(docDevicesSSHEnabled.Name, docDevicesSSHEnabled.Unit, docDevicesSSHEnabled.Description,
			float64(sshEnabled), nil)
	}
	if c.keyExpiryDisabledData {
		e.Gauge(docDevicesKeyExpiryDisabled.Name, docDevicesKeyExpiryDisabled.Unit, docDevicesKeyExpiryDisabled.Description,
			float64(keyExpiryDisabled), nil)
	}

	c.emitDistroRollup(byDistro)

	// B6 fleet-level version-skew gauges (emitted when upstream latest is known).
	if haveLatest {
		c.gsb.Add(docFleetLatestVersion.Name, docFleetLatestVersion.Unit, docFleetLatestVersion.Description,
			1, telemetry.Attrs{attrClientVersion: latestNorm})
		e.Gauge(docDevicesOutdated.Name, docDevicesOutdated.Unit, docDevicesOutdated.Description,
			float64(outdated), nil)
	}

	if c.collectDeviceInvites {
		for k, n := range inviteCounts {
			c.gsb.Add(docDeviceInvites.Name, docDeviceInvites.Unit, docDeviceInvites.Description,
				float64(n), telemetry.Attrs{
					attrInviteAccepted:      k.accepted,
					attrInviteAllowExitNode: k.allowExitNode,
					attrInviteMultiUse:      k.multiUse,
					attrInviteDelivery:      k.delivery,
				})
		}
	}

	if c.collectConnectivity {
		e.Gauge(docDevicesHardNAT.Name, docDevicesHardNAT.Unit, docDevicesHardNAT.Description,
			float64(hardNATCount), nil)
		e.Gauge(docDevicesDirectCapable.Name, docDevicesDirectCapable.Unit, docDevicesDirectCapable.Description,
			float64(directCapableCount), nil)
		for capName, n := range capSupports {
			c.gsb.Add(docDevicesClientSupports.Name, docDevicesClientSupports.Unit, docDevicesClientSupports.Description,
				float64(n), telemetry.Attrs{attrConnCapability: capName})
		}
	}

	// B4 fleet exit/subnet aggregates (always emitted; low cardinality).
	c.gsb.Add(docExitNodesCount.Name, docExitNodesCount.Unit, docExitNodesCount.Description,
		float64(exitAdvertised), telemetry.Attrs{attrExitNodeState: exitStateAdvertised})
	c.gsb.Add(docExitNodesCount.Name, docExitNodesCount.Unit, docExitNodesCount.Description,
		float64(exitEnabled), telemetry.Attrs{attrExitNodeState: exitStateEnabled})
	e.Gauge(docSubnetRoutesAdv.Name, docSubnetRoutesAdv.Unit, docSubnetRoutesAdv.Description,
		float64(len(subnetAdvertised)), nil)
	e.Gauge(docSubnetRoutesEnabled.Name, docSubnetRoutesEnabled.Unit, docSubnetRoutesEnabled.Description,
		float64(len(subnetEnabled)), nil)
	unapproved := 0
	for cidr := range subnetAdvertised {
		if _, ok := subnetEnabled[cidr]; !ok {
			unapproved++
		}
	}
	e.Gauge(docSubnetRoutesUnapproved.Name, docSubnetRoutesUnapproved.Unit, docSubnetRoutesUnapproved.Description,
		float64(unapproved), nil)
	if c.subnetRouteRollup {
		for cidr, n := range routersByCIDR {
			c.gsb.Add(docSubnetRoutesRouters.Name, docSubnetRoutesRouters.Unit, docSubnetRoutesRouters.Description,
				float64(n), telemetry.Attrs{attrRouteCIDR: cidr})
		}
	}

	if c.derpRollup {
		c.emitDERPRollup(devs)
	}

	// Gated on collect_connectivity as well as on geo being configured: the
	// endpoints this reads are clientConnectivity data, and an operator who
	// turned that collection off has said not to derive things from it.
	if c.geo != nil && c.collectConnectivity {
		c.emitGeoRollup(devs)
	}

	// Flush all churning per-entity/aggregate gauges accumulated this tick as
	// observable-gauge snapshots, so a device (or version/tag/region/CIDR/posture
	// series) that stopped appearing drops out of the export instead of ghosting
	// (#55). Reached only on the success path; a mid-Collect API error returned
	// earlier, leaving the previous snapshot in place until the next good tick.
	c.gsb.Flush(e)

	// #524: publish each per-device subrequest's aggregated availability, once
	// per subrequest per tick. Emitted only when the subrequest was actually
	// attempted this tick (see the *Attempted comment above the loop).
	if postureAttempted {
		apistate.Observe(e, c.tracker, c.Name(), opDevicePosture, apistate.Disposition{}, postureFirstErr, nowT)
	}
	if inviteAttempted {
		apistate.Observe(e, c.tracker, c.Name(), subrequestDeviceInvites, apistate.Disposition{}, inviteFirstErr, nowT)
	}

	// #421: publish this tick's per-device subrequest coverage. Nil Coverage
	// emits nothing (Snapshot returns nil), so a collector built without
	// WithCoverage is byte-identical to the pre-#421 behavior.
	apistate.EmitCoverage(e, c.coverage)
	return nil
}

// deviceGeoKey is the bounded label pair of the fleet-geography gauge.
type deviceGeoKey struct {
	country   string
	continent string
}

// emitGeoRollup emits one gauge series per country the fleet is present in,
// derived from each device's advertised public magicsock endpoint.
//
// A ROLLUP, not a per-device label, and that is the whole design. Country is
// bounded (~250 values, in practice at most fleet-size), so a count-by-country
// gauge stays cheap. Hanging the same label off the existing per-device gauges
// would instead change those series' identity, silently breaking every
// dashboard, alert and recording rule already querying them — and the AS number,
// which is not bounded by anything useful, stays off metrics entirely.
//
// Devices whose endpoints yield no globally-routable address, or that the
// databases do not cover, contribute nothing at all. There is deliberately no
// "unknown" bucket: it would read as a country an operator could act on.
func (c *Collector) emitGeoRollup(devs []tsapi.RichDevice) {
	counts := make(map[deviceGeoKey]int)
	for _, d := range devs {
		res, ok := c.deviceGeo(d)
		if !ok || res.CountryISO == "" {
			continue
		}
		counts[deviceGeoKey{country: res.CountryISO, continent: res.ContinentCode}]++
	}
	for k, n := range counts {
		attrs := telemetry.Attrs{semconv.DeviceGeoCountryISO: k.country}
		if k.continent != "" {
			attrs[semconv.DeviceGeoContinentCode] = k.continent
		}
		c.gsb.Add(docDevicesByCountry.Name, docDevicesByCountry.Unit, docDevicesByCountry.Description,
			float64(n), attrs)
	}
}

// deviceGeo locates a device from the first GLOBALLY ROUTABLE address among the
// magicsock endpoints it advertises.
//
// The endpoint list routinely leads with LAN addresses (192.168.x, fe80::), which
// say nothing about where the device is — magicsock advertises every candidate
// path, not a location. geoip.Enrichable is what filters them, so the same rule
// that keeps tailnet addresses out of the flow path applies here too.
func (c *Collector) deviceGeo(d tsapi.RichDevice) (geoip.Result, bool) {
	for _, ep := range d.Endpoints {
		host := ep
		if h, _, err := net.SplitHostPort(ep); err == nil {
			host = h
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !geoip.Enrichable(addr) {
			continue
		}
		if res, ok := c.geo.Lookup(addr); ok {
			return res, true
		}
	}
	return geoip.Result{}, false
}

// distroKey is the bounded (name, codename) label pair for the distro
// inventory. Both components are normalized before they land here, so this is
// also the map key used for the fleet rollup.
type distroKey struct {
	name     string
	codename string
}

// distroKeyFor normalizes a device's distro block into the bounded label pair,
// reporting false when the device declares no distribution at all.
//
// Normalization is deliberately narrow: trim and lowercase (so "Ubuntu " and
// "ubuntu" are one series rather than two) and substitute distroUnknown for an
// empty codename, which many distributions simply do not publish. The distro
// VERSION never appears — it is already carried as semconv.OSVersion on the
// per-device identity attributes, and putting a raw version string on a fleet
// rollup is exactly the high-cardinality dimension #427 forbids.
func distroKeyFor(d tsapi.DistroInfo) (distroKey, bool) {
	name := strings.ToLower(strings.TrimSpace(d.Name))
	if name == "" {
		return distroKey{}, false
	}
	codename := strings.ToLower(strings.TrimSpace(d.CodeName))
	if codename == "" {
		codename = distroUnknown
	}
	return distroKey{name: name, codename: codename}, true
}

// emitDistroRollup emits tailscale.devices.by_distro, one series per distinct
// (distro name, codename) pair. Over distroRollupLimit distinct pairs the
// busiest `limit` keep their own series (ties broken by name then codename for
// determinism) and the remainder fold into a single __other__/__other__ series,
// so the fleet total is preserved — same shape as emitTagRollup's overflow
// bucket.
func (c *Collector) emitDistroRollup(byDistro map[distroKey]int) {
	if len(byDistro) == 0 {
		return
	}
	emit := func(k distroKey, n int) {
		c.gsb.Add(docDevicesByDistro.Name, docDevicesByDistro.Unit, docDevicesByDistro.Description,
			float64(n), telemetry.Attrs{attrDistroName: k.name, attrDistroCodename: k.codename})
	}
	if len(byDistro) <= distroRollupLimit {
		for k, n := range byDistro {
			emit(k, n)
		}
		return
	}
	type distroCount struct {
		key distroKey
		n   int
	}
	all := make([]distroCount, 0, len(byDistro))
	for k, n := range byDistro {
		all = append(all, distroCount{k, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n // busiest first
		}
		if all[i].key.name != all[j].key.name {
			return all[i].key.name < all[j].key.name
		}
		return all[i].key.codename < all[j].key.codename
	})
	other := 0
	for i, dc := range all {
		if i < distroRollupLimit {
			emit(dc.key, dc.n)
		} else {
			other += dc.n
		}
	}
	if other > 0 {
		emit(distroKey{name: distroOther, codename: distroOther}, other)
	}
}

// emitTagRollup emits tailscale.devices.by_tag, one series per ACL tag. When
// collectTagRollup is off it emits nothing. When tagRollupLimit > 0 and there are
// more distinct tags than the limit, only the busiest tagRollupLimit tags (by
// device count; ties broken by tag name for determinism) keep their own series
// and the remainder fold into a single tailscale.tag="__other__" series so the
// total is preserved.
func (c *Collector) emitTagRollup(byTag map[string]int) {
	if !c.collectTagRollup || len(byTag) == 0 {
		return
	}
	emit := func(tag string, n int) {
		c.gsb.Add(docDevicesByTag.Name, docDevicesByTag.Unit, docDevicesByTag.Description,
			float64(n), telemetry.Attrs{attrTag: tag})
	}
	if c.tagRollupLimit <= 0 || len(byTag) <= c.tagRollupLimit {
		for tag, n := range byTag {
			emit(tag, n)
		}
		return
	}
	type tagCount struct {
		tag string
		n   int
	}
	tags := make([]tagCount, 0, len(byTag))
	for tag, n := range byTag {
		tags = append(tags, tagCount{tag, n})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].n != tags[j].n {
			return tags[i].n > tags[j].n // busiest first
		}
		return tags[i].tag < tags[j].tag // stable tie-break
	})
	other := 0
	for i, tc := range tags {
		if i < c.tagRollupLimit {
			emit(tc.tag, tc.n)
		} else {
			other += tc.n
		}
	}
	if other > 0 {
		emit(tagOther, other)
	}
}

// deviceInviteKey is the bounded label combination for the device-invites count
// gauge: accepted/pending, the two exposure flags, and the delivery state
// (2x2x2x3 = 24 series max).
type deviceInviteKey struct {
	accepted      bool
	allowExitNode bool
	multiUse      bool
	delivery      string
}

// inviteDelivery classifies HOW a device-share invite was distributed, into the
// bounded {emailed, manual_link, unknown} set (#413).
//
// The order matters. An email address, or an upstream record of a send attempt,
// is positive evidence of an emailed invite. Absence of both is only evidence of
// a link-only share if the record is otherwise populated — which Created stands
// in for. A record with no email, no send attempt AND no creation time tells us
// nothing, so it reads as unknown rather than being folded into manual_link:
// a control plane that simply omits these fields must not be reported as having
// confidently shared everything by link.
func inviteDelivery(inv tsapi.DeviceInvite) string {
	switch {
	case inv.Email != "" || !inv.LastEmailSentAt.IsZero():
		return deliveryEmailed
	case !inv.Created.IsZero():
		return deliveryManualLink
	default:
		return deliveryUnknown
	}
}

// tallyDeviceInvites fetches one device's share invites, folds them into
// counts for the aggregate gauge, records the pending-invite age distribution,
// and emits a per-invite log event carrying the invitee email, the acceptedBy
// login, and the sharing device identity.
//
// Per-device errors (e.g. a missing device_invites:read scope -> 403, or a
// transient failure) stay NON-FATAL: the device is skipped and collection
// continues, so device-invite collection can never break the devices snapshot.
// They are no longer SILENT, though — every attempt is classified through
// apistate and tallied on the shared Coverage (#421). Before that, a 403 on
// every device was indistinguishable from a tailnet with no invites at all,
// while the scrape still reported success. The returned error (nil on
// success) lets Collect aggregate this tick's per-device outcomes into a
// single apistate.Observe call after the loop (#524); it is never used to
// fail the enclosing Collect.
func (c *Collector) tallyDeviceInvites(ctx context.Context, e telemetry.Emitter, d *tsapi.RichDevice, counts map[deviceInviteKey]int, nowT time.Time) error {
	invs, err := c.api.DeviceInvites(ctx, d.ID)
	c.coverage.Record(c.Name(), subrequestDeviceInvites, apistate.Classify(err, apistate.Disposition{}))
	if err != nil {
		return err
	}
	for _, inv := range invs {
		delivery := inviteDelivery(inv)
		counts[deviceInviteKey{
			accepted:      inv.Accepted,
			allowExitNode: inv.AllowExitNode,
			multiUse:      inv.MultiUse,
			delivery:      delivery,
		}]++

		// Age of invites still outstanding. Accepted invites are excluded (their
		// age is history, not exposure), and an invite whose creation time the
		// provider did not supply is skipped rather than recorded as brand new.
		if !inv.Accepted {
			if age, ok := entityage.Seconds(inv.Created, nowT); ok {
				e.Histogram(docDeviceInvitesPendingAge.Name, docDeviceInvitesPendingAge.Unit,
					docDeviceInvitesPendingAge.Description, age, c.ageBuckets, nil)
			}
		}

		// Emit a per-invite log event so "N pending shares" becomes observable
		// as "who shared which device with whom". Only emit when there is
		// identifying data — at minimum the invitee email or an acceptedBy login
		// must be present (a share link with no email and not yet accepted has
		// neither, so there is nothing useful to record).
		if inv.Email == "" && inv.AcceptedByLogin == "" {
			continue
		}
		body := "device share invite pending"
		if inv.Accepted {
			body = "device share invite accepted"
		}
		e.LogEvent(telemetry.Event{
			Name:     docDeviceInviteLog.Name,
			Severity: telemetry.SeverityInfo,
			Body:     body,
			Attrs: telemetry.Attrs{
				semconv.HostName:   d.Hostname,
				semconv.HostID:     d.ID, // device id, consistent with every other device signal
				semconv.AttrUser:   inv.Email,
				attrActorLogin:     inv.AcceptedByLogin,
				attrInviteDelivery: delivery,
			},
		})
	}
	return nil
}

// emitDERPRollup aggregates the per-device DERP latency already fetched into
// tailnet-wide per-region gauges: the best (min) latency to each region, the
// number of devices reporting it, and how many prefer it.
func (c *Collector) emitDERPRollup(devs []tsapi.RichDevice) {
	type agg struct {
		minMs     float64
		haveMin   bool
		devices   int
		preferred int
	}
	byRegion := map[string]*agg{}
	for i := range devs {
		for region, derp := range devs[i].DERPLatency {
			a := byRegion[region]
			if a == nil {
				a = &agg{}
				byRegion[region] = a
			}
			a.devices++
			if derp.Preferred {
				a.preferred++
			}
			if !a.haveMin || derp.LatencyMs < a.minMs {
				a.minMs = derp.LatencyMs
				a.haveMin = true
			}
		}
	}
	for region, a := range byRegion {
		attrs := telemetry.Attrs{attrDERPRegion: region}
		c.gsb.Add(docDerpRegionLatencyMin.Name, docDerpRegionLatencyMin.Unit, docDerpRegionLatencyMin.Description,
			a.minMs/1000, attrs)
		c.gsb.Add(docDerpRegionDevices.Name, docDerpRegionDevices.Unit, docDerpRegionDevices.Description,
			float64(a.devices), attrs)
		c.gsb.Add(docDerpRegionPreferred.Name, docDerpRegionPreferred.Unit, docDerpRegionPreferred.Description,
			float64(a.preferred), attrs)
	}
}

// emitPosture fetches the posture attributes for one device, always emits the
// posture info-gauge metric (constant 1, curated labels), and conditionally
// emits the full posture LOG event depending on the configured posture log mode.
//
// Per-device errors are non-fatal — the device is skipped and collection
// continues — but they are recorded on the shared Coverage tally first (#421),
// so a posture fetch that fails on every device shows up as degraded coverage
// instead of an empty posture surface on a green scrape. On success the
// returned attributes are held until every device has been fetched, so the
// later attribute-metric pass can rank keys fleet-wide; the returned error lets
// Collect aggregate this tick's per-device outcomes into one apistate.Observe
// call after the loop (#524) without failing the enclosing Collect.
func (c *Collector) emitPosture(ctx context.Context, e telemetry.Emitter, d *tsapi.RichDevice) (tsapi.DeviceAttributes, error) {
	da, err := c.api.DevicePostureAttributes(ctx, d.ID)
	c.coverage.Record(c.Name(), subrequestPostureAttributes, apistate.Classify(err, apistate.Disposition{}))
	if err != nil {
		return tsapi.DeviceAttributes{}, err
	}
	attrs := da.Attributes

	// Info-gauge metric: always emitted (independent of log mode). Constant 1,
	// carrying the curated posture subset plus device identity as labels.
	metricAttrs := telemetry.Attrs{
		semconv.HostName: d.Hostname,
		semconv.HostID:   d.ID,
	}
	for srcKey, label := range postureKeyToLabel {
		if v, ok := attrs[srcKey]; ok {
			metricAttrs[label] = fmt.Sprint(v)
		}
	}
	c.gsb.Add(docPostureInfo.Name, docPostureInfo.Unit, docPostureInfo.Description, 1, metricAttrs)

	// Decide whether to emit the LOG. The signature is computed over the FULL
	// posture map so any posture change (not just curated keys) fires the log.
	sig := postureSignature(attrs)
	emitLog := false
	switch c.postureLogMode {
	case postureLogOff:
		emitLog = false
	case postureLogAlways:
		emitLog = true
	default: // postureLogChanges
		prev, seen := c.lastPosture[d.ID]
		emitLog = !seen || prev != sig
	}
	c.lastPosture[d.ID] = sig

	if !emitLog {
		return da, nil
	}

	evAttrs := telemetry.Attrs{
		semconv.HostName: d.Hostname,
		semconv.HostID:   d.ID,
	}
	// The full posture map is arbitrary and provider-namespaced (never a fixed
	// key set), so it cannot be spread onto the log record as individual raw
	// keys without bypassing PII classification (#56). Carry it JSON-encoded
	// under the single classified attrPostureDetails key instead, so
	// pii_filter.free_text_details gates it like any other free-text signal.
	if len(attrs) > 0 {
		if raw, err := json.Marshal(attrs); err == nil {
			evAttrs[attrPostureDetails] = string(raw)
		}
	}
	e.LogEvent(telemetry.Event{
		Name:     docPosture.Name,
		Severity: telemetry.SeverityInfo,
		Body:     fmt.Sprintf("device has %d posture attribute(s)", len(attrs)),
		Attrs:    evAttrs,
	})
	return da, nil
}

// emitAttributes selects the bounded fleet-wide attribute key/value vocabulary,
// then emits every promoted attribute family from that single selection. This
// prevents an expiry or info metric from leaking a key the numeric attribute
// metric dropped.
func (c *Collector) emitAttributes(e telemetry.Emitter, results []postureResult, nowT time.Time, current map[attributeExpiryKey]struct{}) int {
	if !c.attrNamespaceWildcard && len(c.attrNamespaces) == 0 {
		return 0
	}

	keyCounts := map[string]int{}
	for _, result := range results {
		seen := map[string]struct{}{}
		for key, value := range result.attributes.Attributes {
			if c.attributeAllowed(key) && attributeScalar(value) {
				seen[key] = struct{}{}
			}
		}
		for key := range result.attributes.Expiries {
			if c.attributeAllowed(key) {
				seen[key] = struct{}{}
			}
		}
		for key := range seen {
			keyCounts[key]++
		}
	}
	selectedKeys := rankedAttributeNames(keyCounts, c.attributeKeyLimit)
	selected := make(map[string]bool, len(selectedKeys))
	for _, key := range selectedKeys {
		selected[key] = true
	}

	valueCounts := map[string]map[string]int{}
	for _, result := range results {
		for key, value := range result.attributes.Attributes {
			if !selected[key] {
				continue
			}
			if value, ok := value.(string); ok {
				if valueCounts[key] == nil {
					valueCounts[key] = map[string]int{}
				}
				valueCounts[key][value]++
			}
		}
	}
	selectedValues := make(map[string]map[string]bool, len(valueCounts))
	for key, counts := range valueCounts {
		values := rankedAttributeNames(counts, c.attributeValueLimit)
		selectedValues[key] = make(map[string]bool, len(values))
		for _, value := range values {
			selectedValues[key][value] = true
		}
	}

	for _, result := range results {
		d := result.device
		for key, value := range result.attributes.Attributes {
			if !selected[key] {
				continue
			}
			labels := telemetry.Attrs{semconv.HostName: d.Hostname, semconv.HostID: d.ID, attrAttribute: key}
			switch value := value.(type) {
			case bool:
				c.gsb.Add(docAttribute.Name, docAttribute.Unit, docAttribute.Description, boolToFloat(value), labels)
			case float64:
				c.gsb.Add(docAttribute.Name, docAttribute.Unit, docAttribute.Description, value, labels)
			case string:
				if !selectedValues[key][value] {
					value = tagOther
				}
				labels[attrAttributeValue] = value
				c.gsb.Add(docAttributeInfo.Name, docAttributeInfo.Unit, docAttributeInfo.Description, 1, labels)
			}
		}
		c.emitAttributeExpiries(e, d, result.attributes.Expiries, selected, nowT, current)
	}
	return len(keyCounts) - len(selected)
}

func (c *Collector) attributeAllowed(key string) bool {
	namespace, _, ok := strings.Cut(key, ":")
	return ok && (c.attrNamespaceWildcard || c.attrNamespaces[namespace])
}

func attributeScalar(value any) bool {
	switch value.(type) {
	case bool, float64, string:
		return true
	default:
		return false
	}
}

// rankedAttributeNames returns every name when limit is non-positive and the
// busiest limit names otherwise. Lexical tie-breaking makes the selection
// stable across Go map iteration orders.
func rankedAttributeNames(counts map[string]int, limit int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if limit > 0 && len(names) > limit {
		return names[:limit]
	}
	return names
}

func (c *Collector) emitAttributeExpiries(e telemetry.Emitter, d *tsapi.RichDevice, expiries map[string]time.Time, selected map[string]bool, nowT time.Time, current map[attributeExpiryKey]struct{}) {
	for key, expiry := range expiries {
		if !selected[key] {
			continue
		}
		labels := telemetry.Attrs{semconv.HostName: d.Hostname, semconv.HostID: d.ID, attrAttribute: key}
		c.gsb.Add(docAttributeExpiry.Name, docAttributeExpiry.Unit, docAttributeExpiry.Description, float64(expiry.Unix()), labels)
		days := expiry.Sub(nowT).Hours() / 24
		if days > 0 && days <= keyExpiryWarnDays {
			stateKey := attributeExpiryKey{deviceID: d.ID, attribute: key}
			current[stateKey] = struct{}{}
			if expiryLogDue(c.expiryLogMode, c.attributeExpiryState, stateKey, expiry, nowT) {
				e.LogEvent(telemetry.Event{Name: docDeviceAttributeExpiringLog.Name, Severity: telemetry.SeverityWarn,
					Body: "device posture attribute expiring soon", Attrs: telemetry.Attrs{
						semconv.HostName: d.Hostname, semconv.HostID: d.ID, attrAttribute: key,
						attrDeviceAttributeExpiresInDays: fmt.Sprintf("%.2f", days),
					}})
			}
		}
	}
}

// postureSignature returns a stable string fingerprint of a posture map: each
// entry rendered as key=value, sorted by key and joined, so logically-equal
// maps produce equal signatures regardless of Go map iteration order.
func postureSignature(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for k, v := range attrs {
		parts = append(parts, k+"="+fmt.Sprint(v))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x1f")
}

// toMetas converts rich API devices to the cache's normalized DeviceMeta form,
// parsing each address and skipping any that fail to parse. NodeID is set to the
// control-plane node id (used in flow logs); the separate numeric device ID
// (used e.g. as the node-metrics HostID label, #85) is carried in DeviceMeta.ID.
// Online mirrors ConnectedToControl, needed by node-metrics discovery's
// online_only filter when it sources targets from this cache (#85).
func toMetas(devs []tsapi.RichDevice) []enrich.DeviceMeta {
	metas := make([]enrich.DeviceMeta, 0, len(devs))
	for i := range devs {
		d := &devs[i]
		addrs := make([]netip.Addr, 0, len(d.Addresses))
		for _, s := range d.Addresses {
			a, err := netip.ParseAddr(s)
			if err != nil {
				continue
			}
			addrs = append(addrs, a)
		}
		metas = append(metas, enrich.DeviceMeta{
			ID:        d.ID,
			NodeID:    d.NodeID,
			Name:      d.Name,
			Hostname:  d.Hostname,
			OS:        d.OS,
			OSVersion: d.Distro.Version,
			User:      d.User,
			Tags:      d.Tags,
			Addrs:     addrs,
			External:  d.IsExternal,
			Online:    d.ConnectedToControl,
		})
	}
	return metas
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// isExitRoute reports whether a CIDR is an exit-node default route.
func isExitRoute(cidr string) bool {
	return cidr == "0.0.0.0/0" || cidr == "::/0"
}

// boolPtrTrue reports whether p is non-nil and true.
func boolPtrTrue(p *bool) bool { return p != nil && *p }
