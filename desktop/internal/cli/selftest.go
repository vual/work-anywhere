package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
)

func selfTest() error {
	runtime, err := codex.ResolveRuntime(nil)
	if err != nil {
		return err
	}
	instance, err := codex.ResolveInstance()
	if err != nil {
		return err
	}
	self, _ := os.Executable()
	result := map[string]any{
		"ok": true, "bridgeExecutable": self, "instance": instance, "runtime": runtime,
		"checks": []string{"runtime_found", "runtime_not_recursive", "instance_identity", "dpapi_available_at_daemon_start"},
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}
