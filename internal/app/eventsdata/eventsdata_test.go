package eventsdata_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/app/eventsdata"
)

// TestPageDTOCannotCarryACredential mirrors flowhtml's
// TestPageDTOCannotCarryACredential (#322): the events page's state
// round-trips through the URL, and URLs land in history, logs, referrers and
// pasted messages, so nothing credential-shaped may reach this struct. A
// string scan of the rendered page would miss a credential-shaped FIELD
// added here (it would only ever see the value, not the field name), so this
// walks the struct's own fields by reflection.
func TestPageDTOCannotCarryACredential(t *testing.T) {
	t.Parallel()
	assertNoCredentialFields(t, reflect.TypeOf(eventsdata.Page{}))
}

// TestResponseDTOCannotCarryACredential applies the same check to Response:
// it is serialized straight to JSON and served over /api/events.json, so the
// same rule applies even though it never passes through a URL.
func TestResponseDTOCannotCarryACredential(t *testing.T) {
	t.Parallel()
	assertNoCredentialFields(t, reflect.TypeOf(eventsdata.Response{}))
	assertNoCredentialFields(t, reflect.TypeOf(eventsdata.Filters{}))
}

func assertNoCredentialFields(t *testing.T, rt reflect.Type) {
	t.Helper()
	for i := range rt.NumField() {
		f := rt.Field(i)
		name := strings.ToLower(f.Name)
		for _, bad := range []string{"token", "secret", "password", "auth", "credential", "key"} {
			if strings.Contains(name, bad) {
				t.Errorf("%s.%s looks like a credential (%q); nothing credential-shaped may reach this struct.",
					rt.Name(), f.Name, bad)
			}
		}
		if f.Type.String() == "config.Secret" {
			t.Errorf("%s.%s is a config.Secret; no credential may reach this struct", rt.Name(), f.Name)
		}
	}
}
