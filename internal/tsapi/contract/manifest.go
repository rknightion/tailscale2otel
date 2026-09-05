package contract

// Manifest is the authoritative list of Tailscale API GET operations that
// tailscale2otel consumes. Every entry is verified against the vendored OAS
// (spec/tailscale-api.json) by TestManifest_EveryIDExistsInVendoredOAS
// in manifest_test.go.
//
// Confirmed operationId → Client method mapping (reconciled against
// the vendored spec via grep + URL-builder inspection):
//
//	listTailnetDevices        → DevicesRich          path: /api/v2/tailnet/{t}/devices?fields=all
//	listConfigurationAuditLogs→ ConfigAuditLogs       path: /api/v2/tailnet/{t}/logging/configuration
//	listNetworkFlowLogs       → NetworkFlowLogs       path: /api/v2/tailnet/{t}/logging/network
//	listTailnetKeys           → KeysRich              path: /api/v2/tailnet/{t}/keys?all=true
//	listUsers                 → Users                 path: /api/v2/tailnet/{t}/users
//	listWebhooks              → Webhooks              path: /api/v2/tailnet/{t}/webhooks
//	getContacts               → Contacts              path: /api/v2/tailnet/{t}/contacts
//	getTailnetSettings        → TailnetSettings       path: /api/v2/tailnet/{t}/settings
//	getPolicyFile             → PolicyFileRaw         path: /api/v2/tailnet/{t}/acl (HuJSON — FuzzSkip)
//	getLogStreamingConfiguration → LogStreamConfiguration(…,"configuration") path: /api/v2/tailnet/{t}/logging/{logType}/stream
//	getLogStreamingStatus     → LogStreamStatus(…,"configuration") path: /api/v2/tailnet/{t}/logging/{logType}/stream/status
//	getPostureIntegrations    → PostureIntegrations   path: /api/v2/tailnet/{t}/posture/integrations
//	listServices              → Services              path: /api/v2/tailnet/{t}/services
//	listServiceHosts          → ServiceHosts(…,name)  path: /api/v2/tailnet/{t}/services/{serviceName}/devices (LiveRequires service-name)
//	getDnsConfiguration       → DNSConfiguration      path: /api/v2/tailnet/{t}/dns/configuration
//	listDeviceInvites         → DeviceInvites(…,devID) path: /api/v2/device/{id}/device-invites (LiveRequires device-id)
//	getDevicePostureAttributes→ DevicePostureAttributes(…,devID) path: /api/v2/device/{id}/attributes (LiveRequires device-id)
//	listUserInvites           → UserInvites           path: /api/v2/tailnet/{t}/user-invites
//	listOAuthApps             → OAuthApps             path: /api/v2/tailnet/{t}/oauth-apps
//	listOrganizationTailnets  → OrganizationTailnets  path: /api/v2/organizations/{organization}/tailnets?limit=100

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// t0/t1 are the fixed log-window times used for Invoke closures on log-polling
// ops. They are package-level vars so they are computed once at init time.
var (
	t0 = mustTime("2026-01-01T00:00:00Z")
	t1 = mustTime("2026-01-01T01:00:00Z")
)

// mustTime parses an RFC3339 timestamp and panics if it fails. Only called from
// package-level var initialisers with compile-time constants.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("contract: mustTime: " + err.Error())
	}
	return t
}

// Manifest is the consumed-surface manifest. Each entry must satisfy:
//   - ID matches an OAS operationId in spec/tailscale-api.json
//   - Method == "GET"
//   - Invoke != nil
var Manifest = []Op{
	{
		ID:                "listTailnetDevices",
		Method:            "GET",
		KnownTopLevelKeys: []string{"devices"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.DevicesRich(ctx)
			return err
		},
	},
	{
		ID:                "listConfigurationAuditLogs",
		Method:            "GET",
		KnownTopLevelKeys: []string{"logs"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.ConfigAuditLogs(ctx, t0, t1)
			return err
		},
	},
	{
		ID:                "listNetworkFlowLogs",
		Method:            "GET",
		KnownTopLevelKeys: []string{"logs"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.NetworkFlowLogs(ctx, t0, t1)
			return err
		},
	},
	{
		ID:                "listTailnetKeys",
		Method:            "GET",
		KnownTopLevelKeys: []string{"keys"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.KeysRich(ctx)
			return err
		},
	},
	{
		ID:                "listUsers",
		Method:            "GET",
		KnownTopLevelKeys: []string{"users"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.Users(ctx)
			return err
		},
	},
	{
		ID:     "listWebhooks",
		Method: "GET",
		// OAS schema: object with "webhooks" array key; tsclient handles unwrapping.
		KnownTopLevelKeys: []string{"webhooks"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.Webhooks(ctx)
			return err
		},
	},
	{
		ID:                "getContacts",
		Method:            "GET",
		KnownTopLevelKeys: []string{"account", "support", "security"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.Contacts(ctx)
			return err
		},
	},
	{
		ID:     "getTailnetSettings",
		Method: "GET",
		// TailnetSettings is a flat object; known keys match the struct fields.
		KnownTopLevelKeys: []string{
			"devicesApprovalOn",
			"devicesAutoUpdatesOn",
			"devicesKeyDurationDays",
			"usersApprovalOn",
			"usersRoleAllowedToJoinExternalTailnets",
			"networkFlowLoggingOn",
			"regionalRoutingOn",
			"postureIdentityCollectionOn",
			"httpsEnabled",
			"aclsExternallyManagedOn",
		},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.TailnetSettings(ctx)
			return err
		},
	},
	{
		ID:     "getPolicyFile",
		Method: "GET",
		// HuJSON body — not plain JSON; excluded from schema-driven fuzz.
		KnownTopLevelKeys: nil,
		FuzzSkip:          true,
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.PolicyFileRaw(ctx)
			return err
		},
	},
	{
		ID:     "getLogStreamingConfiguration",
		Method: "GET",
		// LogStreamConfiguration is the bounded subset of the endpoint's
		// destination configuration that the collector retains.
		KnownTopLevelKeys: []string{"logType", "destinationType"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.LogStreamConfiguration(ctx, "configuration")
			return err
		},
	},
	{
		ID:     "getLogStreamingStatus",
		Method: "GET",
		// LogStreamStatus is a flat object.
		KnownTopLevelKeys: []string{
			"lastActivity",
			"lastError",
			"maxBodySize",
			"maxNumEntries",
			"numSpoofedEntries",
			"numBytesSent",
			"numEntriesSent",
			"numFailedRequests",
			"numTotalRequests",
			"numMaxBodyRequests",
		},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.LogStreamStatus(ctx, "configuration")
			return err
		},
	},
	{
		ID:                "getPostureIntegrations",
		Method:            "GET",
		KnownTopLevelKeys: []string{"integrations"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.PostureIntegrations(ctx)
			return err
		},
	},
	{
		ID:                "listServices",
		Method:            "GET",
		KnownTopLevelKeys: []string{"vipServices"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.Services(ctx)
			return err
		},
	},
	{
		ID:     "listServiceHosts",
		Method: "GET",
		// Devices backing a VIP service. Guards the ServiceHost field tags (esp.
		// NodeID's stableNodeID wire name) against future drift (#72). Invoke's
		// placeholder is fine for Decode/fuzz (path-agnostic server); the live lane
		// substitutes a real service name resolved from listServices (#424).
		KnownTopLevelKeys: []string{"hosts"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.ServiceHosts(ctx, "svc:placeholder")
			return err
		},
		LiveRequires: []LiveResource{LiveServiceName},
		LiveInvoke: func(ctx context.Context, c *tsapi.Client, args LiveArgs) error {
			_, err := c.ServiceHosts(ctx, args.ServiceName)
			return err
		},
	},
	{
		ID:     "getDnsConfiguration",
		Method: "GET",
		// DNSConfiguration is a structured object.
		KnownTopLevelKeys: []string{"nameservers", "splitDNS", "searchPaths", "preferences"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.DNSConfiguration(ctx)
			return err
		},
	},
	{
		ID:     "listDeviceInvites",
		Method: "GET",
		// Bare array response. Invoke's placeholder device id is fine for
		// Decode/fuzz; the live lane substitutes a real id from listTailnetDevices.
		KnownTopLevelKeys: []string{""}, // sentinel: bare array
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.DeviceInvites(ctx, "placeholder-device-id")
			return err
		},
		LiveRequires: []LiveResource{LiveDeviceID},
		LiveInvoke: func(ctx context.Context, c *tsapi.Client, args LiveArgs) error {
			_, err := c.DeviceInvites(ctx, args.DeviceID)
			return err
		},
	},
	{
		ID:     "getDevicePostureAttributes",
		Method: "GET",
		// Invoke's placeholder device id is fine for Decode/fuzz; the live lane
		// substitutes a real id from listTailnetDevices.
		KnownTopLevelKeys: []string{"attributes"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.DevicePostureAttributes(ctx, "placeholder-device-id")
			return err
		},
		LiveRequires: []LiveResource{LiveDeviceID},
		LiveInvoke: func(ctx context.Context, c *tsapi.Client, args LiveArgs) error {
			_, err := c.DevicePostureAttributes(ctx, args.DeviceID)
			return err
		},
	},
	{
		ID:                "listUserInvites",
		Method:            "GET",
		KnownTopLevelKeys: []string{""}, // sentinel: bare array
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.UserInvites(ctx)
			return err
		},
	},
	{
		ID:                "listOAuthApps",
		Method:            "GET",
		KnownTopLevelKeys: []string{"oauthApps"},
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.OAuthApps(ctx)
			return err
		},
	},
	{
		ID:                "listOrganizationTailnets",
		Method:            "GET",
		KnownTopLevelKeys: []string{"tailnets", "cursor", "totalCount"},
		// The alpha operation needs an organization identifier which the live
		// contract lane intentionally never discovers or stores. Decode/fuzz
		// exercise the real method with a placeholder instead.
		LiveSkip: true,
		Invoke: func(ctx context.Context, c *tsapi.Client) error {
			_, err := c.OrganizationTailnets(ctx, "placeholder-organization")
			return err
		},
	},
}
