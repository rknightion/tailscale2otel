package tsapi

import (
	"io"

	"github.com/rknightion/tailscale2otel/v4/internal/jsonbudget"
)

// The decode-budget machinery itself lives in internal/jsonbudget so
// internal/hsapi enforces byte-for-byte the same controls (#488). This file is
// the tsapi-flavored facade over it: same names, same semantics, same
// errors.Is/errors.As behavior every existing caller and test already relies on.

// budgetSource prefixes every budget error raised by this package.
const budgetSource = "tsapi"

// Budget limit names reported by BudgetError.Limit. They are stable, low-
// cardinality strings so a caller can log or label which control tripped
// without ever touching response content.
const (
	BudgetLimitBytes         = jsonbudget.LimitBytes
	BudgetLimitDepth         = jsonbudget.LimitDepth
	BudgetLimitString        = jsonbudget.LimitString
	BudgetLimitArrayElements = jsonbudget.LimitArrayElements
)

// BudgetError reports which decode budget a response blew. It carries no
// response content — only the name of the control, the limit it exceeded and,
// for the operator-tunable byte budget, the config key to raise. It unwraps to
// ErrResponseTooLarge (byte budget) or ErrResponseTooComplex (structural
// budgets) so callers can keep using errors.Is against those sentinels.
type BudgetError = jsonbudget.Error

// decodeBudget bounds one JSON response decode. MaxBytes is the operator-tunable
// wire-byte ceiling; the other three are structural controls that stop a
// degenerate-but-syntactically-valid body from forcing a large allocation well
// before the byte ceiling is reached.
type decodeBudget = jsonbudget.Budget

// budgetOf builds a decodeBudget for a byte ceiling and the config key that
// sets it, using the shared structural constants (limit.go).
func budgetOf(maxBytes int64, configKey string) decodeBudget {
	return jsonbudget.Of(budgetSource, maxBytes, configKey)
}

// decodeJSONBudgeted decodes a single JSON value from r into out under budget.
func decodeJSONBudgeted(r io.Reader, budget decodeBudget, out any) error {
	return jsonbudget.Decode(r, budget, out)
}
