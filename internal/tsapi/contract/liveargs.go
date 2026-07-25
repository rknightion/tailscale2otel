package contract

// Live path-parameter resolution (#424).
//
// Three consumed operations are addressed by a path parameter rather than the
// tailnet alone: listDeviceInvites and getDevicePostureAttributes (a device id)
// and listServiceHosts (a service name). Their Invoke closures pass a hardcoded
// placeholder, which is fine for Decode/fuzz (that harness serves the same body
// on every path) but 404s against the real API — so all three were excluded
// from the live lane and their decoders were never exercised against real data.
//
// This resolves REAL values from list endpoints the manifest already consumes.

import (
	"context"
	"fmt"

	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

// LiveResource names a real tailnet resource that the live-contract lane must
// resolve before it can exercise a path-parameterized operation.
type LiveResource string

const (
	// LiveDeviceID is a device id usable in /api/v2/device/{id}/….
	LiveDeviceID LiveResource = "device-id"
	// LiveServiceName is a VIP service name usable in …/services/{name}/devices.
	LiveServiceName LiveResource = "service-name"
)

// LiveArgs carries the real path-parameter values resolved for one live run.
// The zero value resolves nothing, which makes every parameterized op
// not-applicable rather than running it against an empty path.
type LiveArgs struct {
	DeviceID    string
	ServiceName string
}

// Has reports whether r was resolved. An unrecognised resource reports false:
// a mistyped LiveRequires entry must exclude the op, never let it run with a
// zero-valued path parameter (which is exactly the placeholder bug this file
// exists to remove).
func (a LiveArgs) Has(r LiveResource) bool {
	switch r {
	case LiveDeviceID:
		return a.DeviceID != ""
	case LiveServiceName:
		return a.ServiceName != ""
	default:
		return false
	}
}

// Missing returns the entries of required that Has reports absent, preserving
// order. Returns nil when everything required is present.
func (a LiveArgs) Missing(required []LiveResource) []LiveResource {
	var out []LiveResource
	for _, r := range required {
		if !a.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

// ResolveLiveArgs resolves real path-parameter values from list endpoints that
// the manifest already consumes.
//
// It is strictly READ-ONLY: it issues GETs and NEVER creates, mutates or
// deletes anything to manufacture a fixture. TestResolveLiveArgs_ResolvesOnlyViaReads
// is the guard.
//
// Selection is the lowest-sorting id/name rather than whatever the API happened
// to return first, so a rerun against the same tailnet picks the same resource:
// a failure is then reproducible instead of depending on response ordering.
//
// The second return value maps each unresolved resource to an explicit,
// human-readable reason. A reason means "these ops are not applicable on this
// tailnet" and callers MUST surface it — it is never grounds for reporting a
// clean run. A resolution call that errors rather than coming back empty is not
// swallowed signal either: listTailnetDevices and listServices are themselves
// manifest ops exercised by the same lane, so a broken list endpoint fails there.
func ResolveLiveArgs(ctx context.Context, c *tsapi.Client) (LiveArgs, map[LiveResource]string) {
	var args LiveArgs
	unavailable := make(map[LiveResource]string, 2)

	devices, err := c.DevicesRich(ctx)
	switch {
	case err != nil:
		unavailable[LiveDeviceID] = fmt.Sprintf("listing tailnet devices failed: %v", err)
	default:
		ids := make([]string, 0, len(devices))
		for _, d := range devices {
			ids = append(ids, d.ID)
		}
		if id := lowestNonEmpty(ids); id != "" {
			args.DeviceID = id
		} else {
			unavailable[LiveDeviceID] = "tailnet contains no device with an id — " +
				"nothing to address /api/v2/device/{id} with"
		}
	}

	services, err := c.Services(ctx)
	switch {
	case err != nil:
		unavailable[LiveServiceName] = fmt.Sprintf("listing tailnet services failed: %v", err)
	default:
		names := make([]string, 0, len(services))
		for _, s := range services {
			names = append(names, s.Name)
		}
		if name := lowestNonEmpty(names); name != "" {
			args.ServiceName = name
		} else {
			unavailable[LiveServiceName] = "tailnet has no Tailscale Services (VIP services) configured — " +
				"nothing to address …/services/{name}/devices with"
		}
	}

	return args, unavailable
}

// lowestNonEmpty returns the lowest-sorting non-empty string in vals, or "" if
// there is none. Deterministic selection is the point: see ResolveLiveArgs.
func lowestNonEmpty(vals []string) string {
	out := ""
	for _, v := range vals {
		if v == "" {
			continue
		}
		if out == "" || v < out {
			out = v
		}
	}
	return out
}
