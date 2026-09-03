package jsonrpc

import (
	"encoding/json"
	"testing"
)

func TestStringAndNumberIDs(t *testing.T) {
	for _, input := range []string{`{"id":"desktop:abc","method":"thread/list"}`, `{"id":12,"method":"thread/list"}`} {
		message, err := Parse([]byte(input))
		if err != nil || !message.IsRequest() {
			t.Fatalf("parse %s: %#v %v", input, message, err)
		}
		rewritten, err := RewriteID([]byte(input), "wa:d:1:1")
		if err != nil {
			t.Fatal(err)
		}
		restored, err := RewriteRawID(rewritten, message.ID)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(restored, &object); err != nil {
			t.Fatal(err)
		}
		if string(object["id"]) != string(message.ID) {
			t.Fatalf("id was not restored: %s", object["id"])
		}
	}
}
