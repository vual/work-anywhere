package mobilecore

import (
	"encoding/json"
	"testing"
)

func TestCallRejectsInvalidJSONWithoutNetwork(t *testing.T) {
	core := NewCore()
	defer core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Call("agent.list", "{", 10)), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "invalid_params" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	core := NewCore()
	core.Close()
	core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Status()), &result); err != nil || !result.OK {
		t.Fatalf("unexpected status: %#v err=%v", result, err)
	}
}

func TestRestoreRejectsIncompleteCredentials(t *testing.T) {
	core := NewCore()
	defer core.Close()
	var result resultEnvelope
	if err := json.Unmarshal([]byte(core.Restore(`{"pairingId":"invalid"}`)), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "restore_failed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
