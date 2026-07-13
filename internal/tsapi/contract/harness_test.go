package contract_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/tsapi/contract"
)

func TestDecode_DevicesRich_Clean(t *testing.T) {
	op, ok := contract.ByID("listTailnetDevices")
	if !ok {
		t.Fatal("listTailnetDevices not in manifest")
	}
	raw := []byte(`{"devices":[{"id":"1","nodeId":"n1CNTRL","name":"a","hostname":"a","os":"linux"}]}`)
	rep := contract.Decode(op, raw)
	if rep.Err != nil {
		t.Fatalf("Decode err = %v, want nil", rep.Err)
	}
	if len(rep.UnknownTopLevelKeys) != 0 {
		t.Fatalf("unknown keys = %v, want none", rep.UnknownTopLevelKeys)
	}
}

func TestDecode_FlagsUnknownTopLevelKey(t *testing.T) {
	op, _ := contract.ByID("listTailnetDevices")
	rep := contract.Decode(op, []byte(`{"devices":[],"newWrapper":{}}`))
	if rep.Err != nil {
		t.Fatalf("Decode err = %v, want nil", rep.Err)
	}
	if len(rep.UnknownTopLevelKeys) != 1 || rep.UnknownTopLevelKeys[0] != "newWrapper" {
		t.Fatalf("unknown = %v, want [newWrapper]", rep.UnknownTopLevelKeys)
	}
}

func TestDecode_SurfacesDecodeError(t *testing.T) {
	op, _ := contract.ByID("listTailnetDevices")
	rep := contract.Decode(op, []byte(`{"devices":"not-an-array"}`))
	if rep.Err == nil || !strings.Contains(rep.Err.Error(), "cannot unmarshal") {
		t.Fatalf("Decode err = %v, want unmarshal error", rep.Err)
	}
}
