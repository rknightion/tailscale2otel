package httpguard

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestSameOriginRequiresMatchingExternallyObservedScheme(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		tls    bool
		want   bool
	}{
		{name: "http matches direct http", origin: "http://metrics.example:9090", want: true},
		{name: "https matches direct tls", origin: "https://metrics.example:9090", tls: true, want: true},
		{name: "https origin cannot claim direct http", origin: "https://metrics.example:9090", want: false},
		{name: "http origin cannot claim direct tls", origin: "http://metrics.example:9090", tls: true, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://metrics.example:9090/metrics", nil)
			req.Host = "metrics.example:9090"
			req.Header.Set("Origin", tc.origin)
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if got := SameOrigin(req); got != tc.want {
				t.Fatalf("SameOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}
