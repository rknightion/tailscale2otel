package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPprofGuardBoundsDurationBeforeHandler(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status int
		called bool
	}{
		{name: "cpu profile default", path: "/debug/pprof/profile", status: http.StatusOK, called: true},
		{name: "cpu profile max", path: "/debug/pprof/profile?seconds=60", status: http.StatusOK, called: true},
		{name: "cpu profile over max", path: "/debug/pprof/profile?seconds=61", status: http.StatusBadRequest},
		{name: "trace over max", path: "/debug/pprof/trace?seconds=60.1", status: http.StatusBadRequest},
		{name: "named delta over max", path: "/debug/pprof/heap?seconds=61", status: http.StatusBadRequest},
		{name: "duplicate", path: "/debug/pprof/profile?seconds=1&seconds=2", status: http.StatusBadRequest},
		{name: "nan", path: "/debug/pprof/trace?seconds=NaN", status: http.StatusBadRequest},
		{name: "infinity", path: "/debug/pprof/trace?seconds=Inf", status: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = "127.0.0.1:8080"
			w := httptest.NewRecorder()
			pprofGuard(next)(w, req)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if called != tc.called {
				t.Fatalf("handler called = %v, want %v", called, tc.called)
			}
		})
	}
}

func TestPprofGuardBoundsSymbolPostAndMethods(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		called bool
	}{
		{name: "symbol bounded post", method: http.MethodPost, path: "/debug/pprof/symbol", body: strings.Repeat("1+", 100), status: http.StatusOK, called: true},
		{name: "symbol oversized post", method: http.MethodPost, path: "/debug/pprof/symbol", body: strings.Repeat("1", (64<<10)+1), status: http.StatusRequestEntityTooLarge},
		{name: "profile post", method: http.MethodPost, path: "/debug/pprof/profile", status: http.StatusMethodNotAllowed},
		{name: "symbol put", method: http.MethodPut, path: "/debug/pprof/symbol", status: http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Host = "127.0.0.1:8080"
			w := httptest.NewRecorder()
			pprofGuard(next)(w, req)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if called != tc.called {
				t.Fatalf("handler called = %v, want %v", called, tc.called)
			}
		})
	}
}

func TestPprofGuardRejectsCrossSiteBrowserRequest(t *testing.T) {
	called := false
	next := func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Host = "admin.example.internal"
	req.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	pprofGuard(next)(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if called {
		t.Fatal("cross-site browser request reached pprof handler")
	}
}
