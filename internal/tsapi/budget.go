package tsapi

import (
	"encoding/json"
	"fmt"
	"io"
)

// Budget limit names reported by BudgetError.Limit. They are stable, low-
// cardinality strings so a caller can log or label which control tripped
// without ever touching response content.
const (
	BudgetLimitBytes         = "bytes"
	BudgetLimitDepth         = "depth"
	BudgetLimitString        = "string_bytes"
	BudgetLimitArrayElements = "array_elements"
)

// BudgetError reports which decode budget a response blew. It carries no
// response content — only the name of the control, the limit it exceeded and,
// for the operator-tunable byte budget, the config key to raise. It unwraps to
// ErrResponseTooLarge (byte budget) or ErrResponseTooComplex (structural
// budgets) so callers can keep using errors.Is against those sentinels.
type BudgetError struct {
	Limit string // one of the BudgetLimit* constants
	Max   int64  // the limit that was exceeded
	// ConfigKey is the dotted config key an operator can raise, or "" when the
	// control is a compile-time structural constant with no knob.
	ConfigKey string
	sentinel  error
}

func (e *BudgetError) Error() string {
	if e.ConfigKey != "" {
		return fmt.Sprintf("tsapi: response exceeds the %s decode budget of %d (raise %s if this is legitimate traffic)",
			e.Limit, e.Max, e.ConfigKey)
	}
	return fmt.Sprintf("tsapi: response exceeds the %s decode budget of %d", e.Limit, e.Max)
}

func (e *BudgetError) Unwrap() error { return e.sentinel }

// decodeBudget bounds one JSON response decode. MaxBytes is the operator-tunable
// wire-byte ceiling; the other three are structural controls that stop a
// degenerate-but-syntactically-valid body from forcing a large allocation well
// before the byte ceiling is reached.
type decodeBudget struct {
	MaxBytes         int64
	MaxDepth         int
	MaxStringBytes   int64
	MaxArrayElements int
	// ConfigKey names the config key behind MaxBytes, quoted back in the error.
	ConfigKey string
}

// budgetOf builds a decodeBudget for a byte ceiling and the config key that
// sets it, using the shared structural constants (limit.go).
func budgetOf(maxBytes int64, configKey string) decodeBudget {
	return decodeBudget{
		MaxBytes:         maxBytes,
		MaxDepth:         maxDecodeDepth,
		MaxStringBytes:   maxDecodeStringBytes,
		MaxArrayElements: maxDecodeArrayElements,
		ConfigKey:        configKey,
	}
}

// budgetReader is an io.Reader that lexes the JSON byte stream as it flows
// through and fails the read the instant a budget is exceeded.
//
// Why a byte-level scanner rather than json.Decoder.Token(): Token materializes
// each string token as a Go string before it can be measured, and Decode buffers
// the whole top-level value before unmarshalling it — both of which allocate the
// thing we are trying to prevent. Enforcing on the READ path means an abusive
// body is rejected after at most one buffer-fill past the violation, so peak
// memory is a function of the structural limits, not of the byte ceiling.
//
// The scanner deliberately does NOT validate JSON — encoding/json still does
// that. It tracks only what the budgets need: string state (so braces, commas
// and brackets inside string literals are not mistaken for structure), nesting
// depth, per-string length, and per-array element counts.
type budgetReader struct {
	r      io.Reader
	budget decodeBudget

	n   int64 // wire bytes pulled from r so far
	err error // sticky: first budget violation

	inString bool
	escaped  bool
	strLen   int64
	stack    []container
}

// container is one open {...} or [...] on the nesting stack. elems counts the
// commas seen directly inside an array, so the element count is elems+1.
type container struct {
	isArray bool
	elems   int
}

func (b *budgetReader) fail(limit string, max int64, sentinel error) error {
	key := ""
	if limit == BudgetLimitBytes {
		key = b.budget.ConfigKey
	}
	b.err = &BudgetError{Limit: limit, Max: max, ConfigKey: key, sentinel: sentinel}
	return b.err
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	// Never pull more than MaxBytes+1 bytes in total: reading exactly one byte
	// past the ceiling is what proves the body is over it, and no more than that
	// is ever buffered.
	if remaining := b.budget.MaxBytes + 1 - b.n; remaining <= 0 {
		return 0, b.fail(BudgetLimitBytes, b.budget.MaxBytes, ErrResponseTooLarge)
	} else if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.r.Read(p)
	b.n += int64(n)
	if serr := b.scan(p[:n]); serr != nil {
		return 0, serr
	}
	if b.n > b.budget.MaxBytes {
		return 0, b.fail(BudgetLimitBytes, b.budget.MaxBytes, ErrResponseTooLarge)
	}
	return n, err
}

// scan advances the lexer over one freshly-read chunk, returning the first
// budget violation it finds. It allocates only when the nesting stack grows,
// which MaxDepth bounds.
func (b *budgetReader) scan(p []byte) error {
	for _, c := range p {
		if b.inString {
			switch {
			case b.escaped:
				b.escaped = false
				b.strLen++
			case c == '\\':
				b.escaped = true
				b.strLen++
			case c == '"':
				b.inString = false
				continue
			default:
				b.strLen++
			}
			if b.strLen > b.budget.MaxStringBytes {
				return b.fail(BudgetLimitString, b.budget.MaxStringBytes, ErrResponseTooComplex)
			}
			continue
		}
		switch c {
		case '"':
			b.inString = true
			b.strLen = 0
		case '{', '[':
			if len(b.stack) >= b.budget.MaxDepth {
				return b.fail(BudgetLimitDepth, int64(b.budget.MaxDepth), ErrResponseTooComplex)
			}
			b.stack = append(b.stack, container{isArray: c == '['})
		case '}', ']':
			if len(b.stack) > 0 {
				b.stack = b.stack[:len(b.stack)-1]
			}
		case ',':
			if len(b.stack) == 0 {
				continue
			}
			top := &b.stack[len(b.stack)-1]
			if !top.isArray {
				continue
			}
			top.elems++
			if top.elems+1 > b.budget.MaxArrayElements {
				return b.fail(BudgetLimitArrayElements, int64(b.budget.MaxArrayElements), ErrResponseTooComplex)
			}
		}
	}
	return nil
}

// decodeJSONBudgeted decodes a single JSON value from r into out under budget.
// A budget violation always wins over whatever incidental io/json error the
// truncated read produced (typically io.ErrUnexpectedEOF, which is
// indistinguishable from ordinary malformed JSON and must not be relied on to
// signal an over-budget body).
func decodeJSONBudgeted(r io.Reader, budget decodeBudget, out any) error {
	br := &budgetReader{r: r, budget: budget}
	err := json.NewDecoder(br).Decode(out)
	if br.err != nil {
		return br.err
	}
	return err
}
