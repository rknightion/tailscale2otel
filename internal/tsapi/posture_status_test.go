package tsapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Synthetic names and assertions preserve the observed object-of-verdicts shape.
func TestDevicesRichPostureStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "all" {
			t.Error("rich list must request fields=all")
		}
		_, _ = w.Write([]byte(`{"devices":[{"id":"synthetic-device","postureStatus":{"posture:managed":{"passing":true,"impacting":false,"failingAssertions":[]},"posture:recommended":{"passing":false,"impacting":false,"failingAssertions":["node:os == 'synthetic'"]},"posture:required":{"passing":false,"impacting":true,"failingAssertions":["custom:managed == true"]}}},{"id":"empty","postureStatus":{}},{"id":"absent"}]}`))
	}))
	defer srv.Close()
	devs, err := newClient(t, srv.URL).DevicesRich(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(devs[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var status map[string]struct {
		Passing           bool
		Impacting         bool
		FailingAssertions []string
	}
	if err := json.Unmarshal(decoded["PostureStatus"], &status); err != nil {
		t.Fatalf("postureStatus was discarded: %v", err)
	}
	if len(status) != 3 || !status["posture:managed"].Passing || status["posture:recommended"].Impacting || !status["posture:required"].Impacting || len(status["posture:required"].FailingAssertions) != 1 {
		t.Fatalf("wrong posture verdicts: %+v", status)
	}
	for _, d := range devs[1:] {
		raw, _ := json.Marshal(d)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		var empty map[string]any
		_ = json.Unmarshal(m["PostureStatus"], &empty)
		if len(empty) != 0 {
			t.Fatal("empty/absent status gained verdicts")
		}
	}
}
