package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const ackResponseLimit = 1 << 20

var (
	errACKResponse = errors.New("OTLP response validation failed")
	errACKRejected = errors.New("OTLP response rejected items")
	errACKTLS      = errors.New("OTLP HTTP TLS configuration failed")
)

// httpExportRejection carries only the actual response status, never a backend
// reason/body. Returning it through the HTTP client preserves identity through
// the SDK's wrapping, so server text cannot choose the destructive drop policy.
type httpExportRejection struct {
	code   int
	signal string
}

func (e *httpExportRejection) Error() string {
	return "failed to upload " + e.signal + ": " + strconv.Itoa(e.code)
}

func permanentHTTPStatus(code int) bool {
	return code >= 400 && code < 500 && code != 401 && code != 403 && code != 408 && code != 429
}

// Every leaf must be a confirmed permanent HTTP rejection. Joined transient,
// cancellation, serialization, partial-success and unknown errors fail closed.
// In particular, never classify a destructive loss by arbitrary error text.
func permanentExportRejection(err error) bool {
	if err == nil {
		return false
	}
	switch e := err.(type) { //nolint:errorlint // Inspect one node, not one matching descendant of a join.
	case *httpExportRejection:
		return permanentHTTPStatus(e.code)
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !permanentExportRejection(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return permanentExportRejection(e.Unwrap())
	default:
		return false
	}
}

type exportResult struct {
	ack     bool
	warning bool
	err     error
}

// Only the exact pinned SDK leaf types may turn an error into a warning ACK.
// Unknown wrappers without an unwrap contract and every unknown leaf fail closed.
func classifyExportResult(err error, signal string) exportResult {
	result := exportResult{ack: true}
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		if recognized, rejected := sdkPartialSuccessLeaf(e, signal); recognized {
			if rejected == 0 {
				result.warning = true
				return
			}
			result.ack = false
			result.err = errors.Join(result.err, e)
			return
		}
		// Inspect this node, not errors.As: searching descendants here could
		// bypass sibling leaves in a joined error and falsely acknowledge it.
		switch v := e.(type) { //nolint:errorlint // Intentional one-level unwrap traversal.
		case interface{ Unwrap() []error }:
			children := v.Unwrap()
			if len(children) > 0 {
				for _, child := range children {
					visit(child)
				}
				return
			}
		case interface{ Unwrap() error }:
			if child := v.Unwrap(); child != nil {
				visit(child)
				return
			}
		}
		result.ack = false
		result.err = errors.Join(result.err, e)
	}
	visit(err)
	return result
}

func sdkPartialSuccessLeaf(err error, signal string) (bool, int64) {
	if err == nil {
		return false, 0
	}
	value := reflect.ValueOf(err)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false, 0
		}
		value = value.Elem()
	}
	t := value.Type()
	if t.Kind() != reflect.Struct || t.Name() != "PartialSuccess" || t.NumField() != 3 {
		return false, 0
	}
	var expected string
	switch signal {
	case "metrics":
		switch t.PkgPath() {
		case "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp/internal", "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc/internal":
			expected = "metric data points"
		}
	case "logs":
		switch t.PkgPath() {
		case "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp/internal", "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc/internal":
			expected = "logs"
		}
	}
	if expected == "" {
		return false, 0
	}
	for name, fieldType := range map[string]reflect.Type{"RejectedItems": reflect.TypeFor[int64](), "RejectedKind": reflect.TypeFor[string](), "ErrorMessage": reflect.TypeFor[string]()} {
		field, ok := t.FieldByName(name)
		if !ok || field.PkgPath != "" || field.Type != fieldType {
			return false, 0
		}
	}
	if value.FieldByName("RejectedKind").String() != expected {
		return false, 0
	}
	return true, value.FieldByName("RejectedItems").Int()
}

type validatingACKTransport struct {
	base   http.RoundTripper
	signal string
}

func (t *validatingACKTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err != nil || response == nil {
		return response, err
	}
	if permanentHTTPStatus(response.StatusCode) {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, &httpExportRejection{code: response.StatusCode, signal: t.signal}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response, nil
	}
	if response.Body == nil {
		return nil, errACKResponse
	}
	closed := false
	defer func() {
		if !closed {
			_ = response.Body.Close()
		}
	}()
	var reader io.Reader = response.Body
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		if !strings.EqualFold(encoding, "gzip") {
			return nil, errACKResponse
		}
		gz, readErr := gzip.NewReader(response.Body)
		if readErr != nil {
			return nil, errACKResponse
		}
		defer gz.Close()
		reader = gz
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, ackResponseLimit+1))
	if readErr != nil {
		// Preserve cancellation identity, never arbitrary transport/body-read text.
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if errors.Is(readErr, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(readErr, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, errACKResponse
	}
	if len(body) > ackResponseLimit {
		return nil, errACKResponse
	}
	closeErr := response.Body.Close()
	closed = true
	if closeErr != nil {
		return nil, errACKResponse
	}
	if len(body) > 0 {
		mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if parseErr != nil || mediaType != "application/x-protobuf" {
			return nil, errACKResponse
		}
		var rejected int64
		switch t.signal {
		case "metrics":
			var message metrics.ExportMetricsServiceResponse
			if proto.Unmarshal(body, &message) != nil {
				return nil, errACKResponse
			}
			rejected = message.GetPartialSuccess().GetRejectedDataPoints()
		case "logs":
			var message logs.ExportLogsServiceResponse
			if proto.Unmarshal(body, &message) != nil {
				return nil, errACKResponse
			}
			rejected = message.GetPartialSuccess().GetRejectedLogRecords()
		default:
			return nil, errACKResponse
		}
		if rejected != 0 {
			return nil, errACKRejected
		}
	}
	// The original body is always closed; hand only validated, bounded owned bytes
	// to the SAME SDK client so its warning/retry/error processing stays intact.
	response.Header = response.Header.Clone()
	response.Header.Set("Content-Type", "application/x-protobuf")
	response.Header.Del("Content-Encoding")
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	response.Uncompressed = true
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

// WithHTTPClient bypasses SDK client construction. Reproduce its effective
// timeout and TLS sources here; request headers, URL, retry and serialization
// remain owned by the SDK. Per-signal options have already been resolved.
func guardedHTTPClient(opts Options, signal string) (*http.Client, error) {
	timeout := 10 * time.Second
	for _, key := range []string{"OTEL_EXPORTER_OTLP_TIMEOUT", "OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal) + "_TIMEOUT"} {
		if raw := os.Getenv(key); raw != "" {
			if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms >= 0 && ms <= int64((1<<63-1)/time.Millisecond) {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}
	}
	if opts.Transport.Timeout > 0 {
		timeout = opts.Transport.Timeout
	}
	var config *tls.Config
	var err error
	if !opts.Insecure {
		config, err = effectiveTLSConfig(opts)
	}
	if err != nil {
		return nil, errACKTLS
	}
	if config == nil && opts.DynamicTLSConfig == nil {
		config, err = ackEnvTLS(signal)
		if err != nil {
			return nil, err
		}
	}
	// Match the pinned SDK transport's defaults, including environment proxy;
	// do not mutate or depend on an application-replaced DefaultTransport.
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second, TLSClientConfig: config}
	var base http.RoundTripper = transport
	if opts.DynamicTLSConfig != nil {
		// Reuse the established per-handshake rotation semantics, but retain the
		// transport defaults above and response guard outside header rotation.
		dynamic := dynamicHTTPClient(opts)
		base = dynamic.Transport
	} else if opts.DynamicHeaders != nil {
		base = &dynamicHeaderTransport{base: transport, headers: opts.DynamicHeaders}
	}
	return &http.Client{Transport: &validatingACKTransport{base: base, signal: signal}, Timeout: timeout}, nil
}

func ackEnvTLS(signal string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	configured := false
	// Signal-specific CA and paired client cert/key override the generic source.
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal) + "_"} {
		if path := os.Getenv(prefix + "CERTIFICATE"); path != "" {
			pem, err := safefile.ReadRegular(path, safefile.MaxPEMBytes, safefile.AllowSymlink)
			if err != nil {
				return nil, errACKTLS
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errACKTLS
			}
			config.RootCAs = pool
			configured = true
		}
		cert, key := os.Getenv(prefix+"CLIENT_CERTIFICATE"), os.Getenv(prefix+"CLIENT_KEY")
		if cert != "" || key != "" {
			if cert == "" || key == "" {
				return nil, errACKTLS
			}
			pair, err := safefile.LoadX509KeyPair(cert, key, safefile.MaxPEMBytes)
			if err != nil {
				return nil, errACKTLS
			}
			config.Certificates = []tls.Certificate{pair}
			configured = true
		}
	}
	if !configured {
		return nil, nil
	}
	return config, nil
}
