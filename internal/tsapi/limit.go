package tsapi

import (
	"io"

	"github.com/rknightion/tailscale2otel/v5/internal/jsonbudget"
)

// Byte budgets for a single successful (2xx) Tailscale API JSON response body,
// tiered by endpoint class and both operator-tunable (#474).
//
// The old single 256 MiB cap was checked only AFTER encoding/json had
// materialized the value, so an abusive body allocated ~1 GiB (measured live
// heap 896 MiB) before ErrResponseTooLarge came back — four times the 256 MiB
// memory limit the Helm chart ships by default, i.e. the guard OOM-killed the
// process it was supposed to protect.
//
// Sizing evidence:
//   - Bulk log pulls (logging/network, logging/configuration) are legitimately
//     multi-MB. A representative flow-log record with srcNode/dstNodes
//     identities and four traffic slices measures ~2.6 KiB on the wire
//     (internal/tsapi/budget_test.go), so 32 MiB covers ~12,000 records in a
//     single poll window — far past what a one-to-five minute window produces on
//     a large tailnet, and 8x below the Helm memory limit.
//   - Every other endpoint is a snapshot resource. The heaviest is the rich
//     device list: a live capture (.capture/devices-rich-live-20260713.json)
//     measures 39,056 bytes for 22 devices, i.e. ~1.8 KiB/device, so 4 MiB
//     covers roughly 2,400 devices. TUNING CONSTRAINT: tailnets much past ~2,000
//     devices must raise tailscale.max_response_bytes — the API has no
//     pagination for these endpoints, so a bigger tailnet is a bigger single
//     body. The error names the key to raise.
const (
	defaultMaxResponseBytes    = 4 << 20  // 4 MiB — snapshot endpoints (devices, keys, dns, services, ...)
	defaultMaxLogResponseBytes = 32 << 20 // 32 MiB — bulk log pulls (flow logs, audit logs)
)

// Structural decode budgets. These are NOT operator-tunable and are shared with
// internal/hsapi via internal/jsonbudget — see that package for the per-limit
// sizing evidence. maxDecodeArrayElements at 500,000 is ~40x the ~12,000
// flow-log records the byte budget allows.
const (
	maxDecodeDepth         = jsonbudget.DefaultMaxDepth
	maxDecodeStringBytes   = jsonbudget.DefaultMaxStringBytes
	maxDecodeArrayElements = jsonbudget.DefaultMaxArrayElements
)

// Config keys behind the two byte budgets, quoted back in BudgetError so an
// operator who hits a limit is told exactly what to raise.
const (
	cfgKeyMaxResponseBytes    = "tailscale.max_response_bytes"
	cfgKeyMaxLogResponseBytes = "tailscale.max_log_response_bytes"
)

// ErrResponseTooLarge is returned when a successful response body exceeds the
// endpoint's byte budget. Callers can errors.Is against it to distinguish
// "upstream sent too much" from an ordinary malformed-JSON decode error.
//
// It is the shared jsonbudget sentinel, so errors.Is keeps working unchanged for
// every existing caller; the *BudgetError wrapping it still names "tsapi" and
// the config key to raise.
var ErrResponseTooLarge = jsonbudget.ErrTooLarge

// ErrResponseTooComplex is returned when a successful response body stays under
// its byte budget but violates a structural budget (nesting depth, single-string
// length, or array element count). Kept distinct from ErrResponseTooLarge
// because the remedy differs: a too-large body may just be a big tailnet
// (raise the key named in the error), whereas a too-complex one is not shaped
// like anything the Tailscale API emits.
var ErrResponseTooComplex = jsonbudget.ErrTooComplex

// decodeJSONLimited decodes a single JSON value from r into out under the
// default structural budgets and an explicit byte ceiling. It is the
// byte-budget-only entry point kept for callers (and tests) that care about a
// single number; getJSON uses the Client's tiered budgets instead.
func decodeJSONLimited(r io.Reader, limit int64, out any) error {
	return decodeJSONBudgeted(r, budgetOf(limit, cfgKeyMaxResponseBytes), out)
}
