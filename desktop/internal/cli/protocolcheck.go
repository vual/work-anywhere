package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/codex"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

type protocolCheck struct {
	Stage  string `json:"stage"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type cappedOutput struct {
	bytes.Buffer
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("diagnostic output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func checkProtocol(ctx context.Context, self string, runtime protocol.RuntimeDescriptor) ([]protocolCheck, error) {
	checks := []protocolCheck{}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// This exercises the real executable entrypoint with closed stdin, just like
	// Desktop. Never run --startup-sign-out or substitute fake policy data.
	cmd := exec.CommandContext(startupCtx, self, "-c", "features.code_mode_host=true", "app-server", "--print-startup-config", "--startup-client-info", `{"name":"codex_desktop","title":"Codex Desktop","version":"waw-protocol-check"}`)
	platformprocess.ConfigureManagedChild(cmd)
	stdout := &cappedOutput{limit: 4 << 20}
	stderr := &cappedOutput{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	stop, err := startProbe(cmd)
	if err == nil {
		err = cmd.Wait()
		stop()
	}
	if startupCtx.Err() != nil {
		err = startupCtx.Err()
	}
	if err == nil {
		err = validateStartupConfiguration(stdout.Bytes())
	}
	check := protocolCheck{Stage: "startup_config_via_bridge_entry", OK: err == nil}
	if err != nil {
		check.Detail = err.Error()
		checks = append(checks, check)
		return checks, fmt.Errorf("protocol startup check: %w", err)
	}
	checks = append(checks, check)
	err = checkHandshake(ctx, runtime)
	check = protocolCheck{Stage: "isolated_initialize", OK: err == nil}
	if err != nil {
		check.Detail = err.Error()
	}
	checks = append(checks, check)
	return checks, err
}

func validateStartupConfiguration(raw []byte) error {
	var value struct {
		AppServerVersion   string                     `json:"appServerVersion"`
		ConfigRequirements map[string]json.RawMessage `json:"configRequirements"`
		Error              json.RawMessage            `json:"error"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("startup stdout is not valid configuration JSON")
	}
	if len(value.Error) > 0 && string(value.Error) != "null" {
		return errors.New("official startup policy/configuration check returned an error")
	}
	if value.AppServerVersion == "" {
		return errors.New("startup response has no appServerVersion")
	}
	if _, ok := value.ConfigRequirements["requirements"]; !ok {
		return errors.New("startup response has no configRequirements.requirements")
	}
	return nil
}

func checkHandshake(parent context.Context, runtime protocol.RuntimeDescriptor) error {
	home, err := os.MkdirTemp("", "waw-protocol-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runtime.RealExecutable, "app-server")
	cmd.Dir = home
	for _, entry := range codex.ChildEnvironment(os.Environ(), runtime.RealExecutable) {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "CODEX_HOME") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home)
	platformprocess.ConfigureManagedChild(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	stop, err := startProbe(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close(); stop(); _ = cmd.Wait() }()
	_, err = io.WriteString(stdin, "{\"id\":1,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"waw_protocol_check\",\"version\":\"1\"},\"capabilities\":{\"experimentalApi\":true,\"requestAttestation\":false}}}\n")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	expectedID := "1"
	for {
		line, readErr := ipc.ReadBoundedLine(reader, 4<<20)
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("initialize response: %w", readErr)
		}
		var response struct {
			ID     json.RawMessage            `json:"id"`
			Result map[string]json.RawMessage `json:"result"`
			Error  json.RawMessage            `json:"error"`
		}
		if json.Unmarshal(line, &response) != nil {
			return errors.New("initialize response is not JSON")
		}
		if string(response.ID) != expectedID {
			continue
		}
		if len(response.Error) > 0 && string(response.Error) != "null" {
			return errors.New("official initialize returned an error")
		}
		if expectedID == "2" {
			if _, ok := response.Result["data"]; !ok {
				return errors.New("post-initialize query has no data")
			}
			return nil
		}
		if _, ok := response.Result["userAgent"]; !ok {
			return errors.New("initialize response has no userAgent")
		}
		_, err = io.WriteString(stdin, "{\"method\":\"initialized\"}\n{\"id\":2,\"method\":\"permissionProfile/list\",\"params\":{}}\n")
		if err != nil {
			return err
		}
		expectedID = "2"
	}
}

// All probe descendants belong to a temporary job. Killing only the wrapper on
// timeout would otherwise leave its official CLI holding the output pipes open.
func startProbe(cmd *exec.Cmd) (func(), error) {
	group, err := platformprocess.NewGroup()
	if err != nil {
		return nil, err
	}
	var once sync.Once
	closeGroup := func() { once.Do(func() { _ = group.Close() }) }
	cmd.Cancel = func() error { closeGroup(); return cmd.Process.Kill() }
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		closeGroup()
		return nil, err
	}
	if err = group.Add(cmd); err != nil {
		closeGroup()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return func() { closeGroup(); _ = cmd.Process.Kill() }, nil
}
