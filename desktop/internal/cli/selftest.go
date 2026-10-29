package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
)

func selfTest(ctx context.Context, verifyProtocol bool) error {
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
		"protocolVerified": false,
		"checks":           []string{"runtime_found", "runtime_not_recursive", "instance_identity", "dpapi_available_at_daemon_start"},
	}
	var protocolErr error
	if verifyProtocol {
		checks, checkErr := checkProtocol(ctx, self, runtime)
		result["protocol"] = checks
		result["ok"], result["protocolVerified"] = checkErr == nil, checkErr == nil
		protocolErr = checkErr
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return protocolErr
}
