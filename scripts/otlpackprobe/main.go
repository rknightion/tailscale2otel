// Command otlpackprobe reports OTLP/HTTP acknowledgement shapes without printing
// credentials, endpoint URLs, response bodies, or detailed transport errors.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logdata "go.opentelemetry.io/proto/otlp/logs/v1"
	metricdata "go.opentelemetry.io/proto/otlp/metrics/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func main() { os.Exit(run(os.Getenv, os.Stdout)) }

func run(getenv func(string) string, out io.Writer) int {
	endpoint := getenv("OTLP_ACK_PROBE_ENDPOINT")
	user := getenv("OTLP_ACK_PROBE_USER")
	tokenFile := getenv("OTLP_ACK_PROBE_TOKEN_FILE")
	if endpoint == "" || user == "" || tokenFile == "" {
		_, _ = fmt.Fprintln(out, "probe: required environment missing")
		return 1
	}
	base, err := url.Parse(endpoint)
	// Credentials belong exclusively to the environment/file, never the URL.
	if err != nil || base.Host == "" || (!allowedTransport(base)) || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		_, _ = fmt.Fprintln(out, "probe: invalid endpoint")
		return 1
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		_, _ = fmt.Fprintln(out, "probe: token file unavailable or empty")
		return 1
	}
	token := strings.TrimSpace(string(data))
	res := &resource.Resource{Attributes: []*common.KeyValue{{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "tailscale2otel-ackprobe"}}}}}
	now := uint64(time.Now().UnixNano()) //nolint:gosec // Current Unix timestamp is positive.
	signals := []struct {
		name     string
		request  proto.Message
		response proto.Message
	}{
		{"logs", &logs.ExportLogsServiceRequest{ResourceLogs: []*logdata.ResourceLogs{{Resource: res, ScopeLogs: []*logdata.ScopeLogs{{LogRecords: []*logdata.LogRecord{{TimeUnixNano: now, Body: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "OTLP ACK probe"}}}}}}}}}, &logs.ExportLogsServiceResponse{}},
		{"metrics", &metrics.ExportMetricsServiceRequest{ResourceMetrics: []*metricdata.ResourceMetrics{{Resource: res, ScopeMetrics: []*metricdata.ScopeMetrics{{Metrics: []*metricdata.Metric{{Name: "tailscale2otel.ackprobe", Data: &metricdata.Metric_Gauge{Gauge: &metricdata.Gauge{DataPoints: []*metricdata.NumberDataPoint{{TimeUnixNano: now, Value: &metricdata.NumberDataPoint_AsInt{AsInt: 1}}}}}}}}}}}}, &metrics.ExportMetricsServiceResponse{}},
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	// Even an untrusted Content-Type must not echo our credentials.
	encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	redact := strings.NewReplacer("Basic "+encoded, "[redacted]", encoded, "[redacted]", token, "[redacted]", user, "[redacted]", endpoint, "[redacted]")
	exit := 0
	for _, signal := range signals {
		payload, err := proto.Marshal(signal.request)
		if err != nil {
			_, _ = fmt.Fprintln(out, "probe: request encoding failed")
			return 1
		}
		target := *base
		target.Path = strings.TrimRight(base.Path, "/") + "/v1/" + signal.name
		target.RawPath = ""
		req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(payload))
		if err != nil {
			_, _ = fmt.Fprintln(out, "probe: request creation failed")
			return 1
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("Accept", "application/x-protobuf")
		req.SetBasicAuth(user, token)
		resp, err := client.Do(req)
		if err != nil {
			_, _ = fmt.Fprintln(out, "probe: request failed")
			exit = 1
			continue
		}
		const maxBody = 1024 * 1024
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		_ = resp.Body.Close()
		readFailed := readErr != nil || len(body) > maxBody
		decoded := !readFailed && proto.Unmarshal(body, signal.response) == nil
		_, _ = fmt.Fprintf(out, "%s status=%d content-type=%q body-length=%d protobuf-decode=%t\n", signal.name, resp.StatusCode, redact.Replace(resp.Header.Get("Content-Type")), len(body), decoded)
		// An auth rejection stops this credential immediately, even when the
		// response body is truncated or over the bounded read limit.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return 1
		}
		if readFailed {
			_, _ = fmt.Fprintln(out, "probe: response read failed or exceeded limit")
			exit = 1
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			exit = 1
		}
	}
	return exit
}

// HTTP is allowed only for literal loopback fixture addresses, never names
// whose resolution could change. All other endpoints require HTTPS.
func allowedTransport(endpoint *url.URL) bool {
	if endpoint.Scheme == "https" {
		return true
	}
	if endpoint.Scheme != "http" {
		return false
	}
	address, err := netip.ParseAddr(endpoint.Hostname())
	return err == nil && address.IsLoopback() && address.Zone() == ""
}
