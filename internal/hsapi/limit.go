package hsapi

import (
	"io"

	"github.com/rknightion/tailscale2otel/v3/internal/jsonbudget"
)

// Byte budget for a single successful (200) Headscale API JSON response body,
// operator-tunable via headscale.max_response_bytes (#488).
//
// The old flat 32 MiB cap was a line-for-line copy of the pattern #474 replaced
// in internal/tsapi: an io.LimitedReader whose exhaustion was checked only AFTER
// encoding/json had already materialized the value. Measured on an endless body,
// that allocated 128.0 MiB with 96.6 MiB still live when the error came back —
// ~38% of the 256Mi memory limit the Helm chart ships by default, spent on a
// body being rejected.
//
// Why 4 MiB and not 32 MiB: every endpoint this client calls (client.go) is a
// SNAPSHOT resource — /api/v1/node, /api/v1/user, /api/v1/preauthkey,
// /api/v1/apikey, /api/v1/policy. Headscale exposes no bulk log-pull equivalent
// of Tailscale's logging/network or logging/configuration, so the larger log
// tier has nothing to cover here and the snapshot tier is the right default.
// Sizing: a fully-populated node record measures ~700 B on the wire
// (TestHSDecodeBudget_RepresentativeNodeListAccepted logs the exact figure), so
// 4 MiB covers roughly 6,000 nodes. TUNING CONSTRAINT: these endpoints are not
// paginated, so a bigger Headscale deployment is a bigger single body — past
// ~5,000 nodes raise headscale.max_response_bytes (and the container memory
// limit). The error names the key.
const defaultMaxResponseBytes = 4 << 20 // 4 MiB

// budgetSource prefixes every budget error raised by this package.
const budgetSource = "hsapi"

// cfgKeyMaxResponseBytes is the config key behind the byte budget, quoted back
// in BudgetError so an operator who hits the limit is told what to raise.
const cfgKeyMaxResponseBytes = "headscale.max_response_bytes"

// BudgetError reports which decode budget a Headscale response blew. It carries
// no response content — only the control that tripped, its limit and, for the
// byte budget, the config key to raise.
type BudgetError = jsonbudget.Error

// Budget limit names reported by BudgetError.Limit.
const (
	BudgetLimitBytes         = jsonbudget.LimitBytes
	BudgetLimitDepth         = jsonbudget.LimitDepth
	BudgetLimitString        = jsonbudget.LimitString
	BudgetLimitArrayElements = jsonbudget.LimitArrayElements
)

// ErrResponseTooLarge is returned when a successful response body exceeds the
// byte budget. Callers can errors.Is against it to distinguish "upstream sent
// too much" from an ordinary malformed-JSON decode error.
var ErrResponseTooLarge = jsonbudget.ErrTooLarge

// ErrResponseTooComplex is returned when a successful response body stays under
// the byte budget but violates a structural budget (nesting depth, single-string
// length, or array element count). Kept distinct from ErrResponseTooLarge
// because the remedy differs: a too-large body may just be a big Headscale
// deployment (raise the key named in the error), whereas a too-complex one is
// not shaped like anything Headscale emits. Headscale's own worst realistic
// single string is GET /api/v1/policy's "policy" field — a whole ACL document —
// which the 4 MiB string budget clears by orders of magnitude.
var ErrResponseTooComplex = jsonbudget.ErrTooComplex

// budgetOf builds a decode budget for an explicit byte ceiling, using the shared
// structural constants.
func budgetOf(maxBytes int64) jsonbudget.Budget {
	return jsonbudget.Of(budgetSource, maxBytes, cfgKeyMaxResponseBytes)
}

// decodeJSONLimited decodes a single JSON value from r into out under the shared
// structural budgets and an explicit byte ceiling. Every budget is enforced on
// the READ path, so an abusive body is rejected before it is allocated — that is
// the whole point of #488; see internal/jsonbudget.
func decodeJSONLimited(r io.Reader, limit int64, out any) error {
	return jsonbudget.Decode(r, budgetOf(limit), out)
}
