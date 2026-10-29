package bridge

import (
	"reflect"
	"testing"
)

func TestStandaloneFlagDoesNotConsumeOptionValues(t *testing.T) {
	args := []string{"-c", "--standalone-ws", "app-server", "-c", "--standalone-ws", "--cloud-skills-snapshot", "--standalone-ws"}
	if got, enabled := standaloneArguments(args); enabled || !reflect.DeepEqual(got, args) {
		t.Fatalf("option value became diagnostic flag: %q %v", got, enabled)
	}
	if got, enabled := standaloneArguments(append(append([]string(nil), args...), "--standalone-ws")); !enabled || !reflect.DeepEqual(got, args) {
		t.Fatalf("explicit diagnostic flag not removed: %q %v", got, enabled)
	}
	args = []string{"app-server", "--print-startup-config", "--standalone-ws"}
	if got, enabled := standaloneArguments(args); enabled || !reflect.DeepEqual(got, args) {
		t.Fatal("one-shot arguments were changed")
	}
}
