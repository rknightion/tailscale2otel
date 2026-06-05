package tsapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevicesRich_DecodesTailnetLock(t *testing.T) {
	fixture := `{"devices":[
	  {"id":"1","nodeId":"n1","hostname":"h1","tailnetLockKey":"tlpub:aaa","tailnetLockError":""},
	  {"id":"2","nodeId":"n2","hostname":"h2","tailnetLockKey":"tlpub:bbb","tailnetLockError":"node is not signed"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatalf("DevicesRich: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("len = %d, want 2", len(devs))
	}
	if devs[0].TailnetLockKey != "tlpub:aaa" {
		t.Errorf("device 0 TailnetLockKey = %q", devs[0].TailnetLockKey)
	}
	if devs[0].TailnetLockError != "" {
		t.Errorf("device 0 TailnetLockError = %q, want empty", devs[0].TailnetLockError)
	}
	if devs[1].TailnetLockError != "node is not signed" {
		t.Errorf("device 1 TailnetLockError = %q", devs[1].TailnetLockError)
	}
}
