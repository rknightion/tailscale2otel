// Package jsonbudget bounds the memory cost of decoding one JSON response body
// from an upstream control-plane API.
//
// It exists because a flat byte cap applied AFTER encoding/json has run is not a
// memory control at all: the value is already materialized by the time the cap
// is consulted. That was the #474 bug in internal/tsapi (measured 896 MiB live
// heap against a 256 MiB container limit) and, independently, the #488 bug in
// internal/hsapi (measured 96.6 MiB live heap against a 32 MiB cap). Two copies
// of the same guard drifted apart, so the guard lives here once and both API
// clients call it.
//
// Decode enforces its budgets on the READ path with a byte-level JSON lexer, so
// an abusive body is rejected after at most one buffer-fill past the violation
// and nothing large is ever allocated.
package jsonbudget

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Budget limit names reported by Error.Limit. They are stable, low-cardinality
// strings so a caller can log or label which control tripped without ever
// touching response content.
const (
	LimitBytes         = "bytes"
	LimitDepth         = "depth"
	LimitString        = "string_bytes"
	LimitArrayElements = "array_elements"
)

// Default structural budgets, applied by Of. These are deliberately NOT
// operator-tunable: they exist to stop a degenerate-but-syntactically-valid body
// from forcing a large allocation long before the byte ceiling is reached, and
// every one of them is orders of magnitude above anything a real control-plane
// API emits.
//
//   - DefaultMaxDepth: the deepest live payload measured is the Tailscale rich
//     device list at 7 levels (.capture/devices-rich-live-20260713.json); 64 is
//     ~9x that and bounds a `[[[[...` nesting bomb.
//   - DefaultMaxStringBytes: the largest realistic single string is a whole
//     tailnet/Headscale policy document carried as one JSON string (a Tailscale
//     audit old/new value, or Headscale's GET /api/v1/policy "policy" field);
//     4 MiB is far past any hand-written ACL. Live captures top out at 645 bytes.
//   - DefaultMaxArrayElements: bounds the cheapest attack per byte — `[0,0,0,…]`
//     is 2 bytes per element on the wire but ~16 bytes per element decoded, so
//     without this a 32 MiB body expands to a ~256 MiB slice.
const (
	DefaultMaxDepth         = 64
	DefaultMaxStringBytes   = 4 << 20 // 4 MiB
	DefaultMaxArrayElements = 500_000
)

// ErrTooLarge is returned when a response body exceeds its byte budget. Callers
// errors.Is against it to distinguish "upstream sent too much" from an ordinary
// malformed-JSON decode error.
//
// It is one sentinel shared by every client package: internal/tsapi and
// internal/hsapi both re-export it, so errors.Is against either package's alias
// matches. That is harmless — a single Client only ever talks to one control
// plane — and it is what keeps the two guards from drifting again.
var ErrTooLarge = errors.New("response body exceeds maximum allowed size")

// ErrTooComplex is returned when a body stays under its byte budget but violates
// a structural budget (nesting depth, single-string length, or array element
// count). Kept distinct from ErrTooLarge because the remedy differs: a too-large
// body may just be a big tailnet (raise the key named in the error), whereas a
// too-complex one is not shaped like anything a real API emits.
var ErrTooComplex = errors.New("response JSON exceeds structural decode budget")

// Error reports which decode budget a response blew. It carries no response
// content — only the name of the control, the limit it exceeded and, for the
// operator-tunable byte budget, the config key to raise. It unwraps to
// ErrTooLarge (byte budget) or ErrTooComplex (structural budgets).
type Error struct {
	// Source is the short subsystem name used as the message prefix
	// ("tsapi", "hsapi"). Empty is allowed and simply drops the prefix.
	Source string
	Limit  string // one of the Limit* constants
	Max    int64  // the limit that was exceeded
	// ConfigKey is the dotted config key an operator can raise, or "" when the
	// control is a compile-time structural constant with no knob.
	ConfigKey string
	sentinel  error
}

func (e *Error) Error() string {
	prefix := ""
	if e.Source != "" {
		prefix = e.Source + ": "
	}
	if e.ConfigKey != "" {
		return fmt.Sprintf("%sresponse exceeds the %s decode budget of %d (raise %s if this is legitimate traffic)",
			prefix, e.Limit, e.Max, e.ConfigKey)
	}
	return fmt.Sprintf("%sresponse exceeds the %s decode budget of %d", prefix, e.Limit, e.Max)
}

func (e *Error) Unwrap() error { return e.sentinel }

// Budget bounds one JSON response decode. MaxBytes is the operator-tunable
// wire-byte ceiling; the other three are structural controls.
type Budget struct {
	// Source prefixes the error message; see Error.Source.
	Source           string
	MaxBytes         int64
	MaxDepth         int
	MaxStringBytes   int64
	MaxArrayElements int
	// ConfigKey names the config key behind MaxBytes, quoted back in the error.
	ConfigKey string
}

// Of builds a Budget for a byte ceiling and the config key that sets it, using
// the shared structural defaults. source is the subsystem name used as the error
// prefix (e.g. "tsapi", "hsapi").
func Of(source string, maxBytes int64, configKey string) Budget {
	return Budget{
		Source:           source,
		MaxBytes:         maxBytes,
		MaxDepth:         DefaultMaxDepth,
		MaxStringBytes:   DefaultMaxStringBytes,
		MaxArrayElements: DefaultMaxArrayElements,
		ConfigKey:        configKey,
	}
}

// ByteCeilingError builds the byte-budget violation for b without decoding
// anything. It is how a caller rejects a response whose declared Content-Length
// is already over budget, before a single body byte is read.
func (b Budget) ByteCeilingError() *Error {
	return &Error{Source: b.Source, Limit: LimitBytes, Max: b.MaxBytes, ConfigKey: b.ConfigKey, sentinel: ErrTooLarge}
}

// reader is an io.Reader that lexes the JSON byte stream as it flows through and
// fails the read the instant a budget is exceeded.
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
type reader struct {
	r      io.Reader
	budget Budget

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

func (b *reader) fail(limit string, max int64, sentinel error) error {
	key := ""
	if limit == LimitBytes {
		key = b.budget.ConfigKey
	}
	b.err = &Error{Source: b.budget.Source, Limit: limit, Max: max, ConfigKey: key, sentinel: sentinel}
	return b.err
}

func (b *reader) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	// Never pull more than MaxBytes+1 bytes in total: reading exactly one byte
	// past the ceiling is what proves the body is over it, and no more than that
	// is ever buffered.
	if remaining := b.budget.MaxBytes + 1 - b.n; remaining <= 0 {
		return 0, b.fail(LimitBytes, b.budget.MaxBytes, ErrTooLarge)
	} else if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.r.Read(p)
	b.n += int64(n)
	if serr := b.scan(p[:n]); serr != nil {
		return 0, serr
	}
	if b.n > b.budget.MaxBytes {
		return 0, b.fail(LimitBytes, b.budget.MaxBytes, ErrTooLarge)
	}
	return n, err
}

// scan advances the lexer over one freshly-read chunk, returning the first
// budget violation it finds. It allocates only when the nesting stack grows,
// which MaxDepth bounds.
func (b *reader) scan(p []byte) error {
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
				return b.fail(LimitString, b.budget.MaxStringBytes, ErrTooComplex)
			}
			continue
		}
		switch c {
		case '"':
			b.inString = true
			b.strLen = 0
		case '{', '[':
			if len(b.stack) >= b.budget.MaxDepth {
				return b.fail(LimitDepth, int64(b.budget.MaxDepth), ErrTooComplex)
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
				return b.fail(LimitArrayElements, int64(b.budget.MaxArrayElements), ErrTooComplex)
			}
		}
	}
	return nil
}

// Decode decodes a single JSON value from r into out under budget. A budget
// violation always wins over whatever incidental io/json error the truncated
// read produced (typically io.ErrUnexpectedEOF, which is indistinguishable from
// ordinary malformed JSON and must not be relied on to signal an over-budget
// body).
func Decode(r io.Reader, budget Budget, out any) error {
	br := &reader{r: r, budget: budget}
	err := json.NewDecoder(br).Decode(out)
	if br.err != nil {
		return br.err
	}
	return err
}
