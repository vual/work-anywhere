package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/appinfo"
	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/identity"
	"github.com/hurf/work-anywhere/desktop/internal/platform/localipc"
	"github.com/hurf/work-anywhere/desktop/internal/platform/paths"
	platformprocess "github.com/hurf/work-anywhere/desktop/internal/platform/process"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
)

func IsCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "daemon", "status", "instance", "conversation", "turn", "action", "catalog", "traffic", "call", "ui", "self-test", "install", "uninstall", "version":
		return true
	default:
		return false
	}
}

func Run(ctx context.Context, args []string, build appinfo.BuildInfo) error {
	if len(args) == 0 {
		return usage()
	}
	if args[0] == "install" {
		return installBridge()
	}
	if args[0] == "uninstall" {
		return uninstallBridge()
	}
	if args[0] == "self-test" {
		return selfTest()
	}
	if args[0] == "version" {
		fmt.Printf("work-anywhere %s (%s, %s)\n", build.Version, build.Commit, build.Date)
		return nil
	}
	if args[0] == "daemon" && len(args) > 1 && args[1] == "start" {
		if _, err := dial(ctx, false); err == nil {
			fmt.Println("daemon is already running")
			return nil
		}
		if err := startDaemon(); err != nil {
			return err
		}
		_, err := waitForDaemon(ctx)
		if err == nil {
			fmt.Println("daemon started")
		}
		return err
	}
	method, params, start, err := parse(args)
	if err != nil {
		return err
	}
	conn, err := dial(ctx, start)
	if err != nil {
		return err
	}
	defer conn.Close()
	request := protocol.ControlRequest{Type: "request", ID: fmt.Sprintf("cli-%d", time.Now().UnixNano()), Method: method, Params: params}
	data, _ := json.Marshal(request)
	if err = ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindControlRequest, Payload: data}); err != nil {
		return err
	}
	for {
		frame, readErr := ipc.ReadFrame(conn)
		if readErr != nil {
			return readErr
		}
		if frame.Kind != ipc.KindControlResponse {
			continue
		}
		var response protocol.ControlResponse
		if json.Unmarshal(frame.Payload, &response) != nil || response.ID != request.ID {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%s: %s", response.Error.Code, response.Error.Message)
		}
		if args[0] == "ui" {
			data, _ := json.Marshal(response.Result)
			var result struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(data, &result) != nil || result.URL == "" {
				return errors.New("daemon did not return a management URL")
			}
			if err := platformprocess.OpenURL(result.URL); err != nil {
				return err
			}
			fmt.Println(result.URL)
			return nil
		}
		return printJSON(response.Result)
	}
}

func parse(args []string) (string, json.RawMessage, bool, error) {
	empty := json.RawMessage(`{}`)
	switch args[0] {
	case "status":
		return "system.status", empty, false, nil
	case "daemon":
		if len(args) < 2 {
			return "", nil, false, usage()
		}
		switch args[1] {
		case "status":
			return "system.status", empty, false, nil
		case "stop":
			return "system.shutdown", empty, false, nil
		case "force-stop":
			return "system.shutdown", json.RawMessage(`{"force":true}`), false, nil
		}
	case "instance":
		if len(args) < 2 {
			return "", nil, false, usage()
		}
		if args[1] == "list" {
			return "instance.list", empty, true, nil
		}
		if len(args) < 3 {
			return "", nil, false, errors.New("instance id is required")
		}
		params, _ := json.Marshal(map[string]string{"instanceId": args[2]})
		if args[1] == "get" {
			return "instance.get", params, true, nil
		}
		if args[1] == "restart" {
			return "instance.restart", params, true, nil
		}
	case "conversation":
		if len(args) < 2 {
			return "", nil, false, usage()
		}
		params := map[string]any{}
		if len(args) > 2 {
			params["instanceId"] = args[2]
		}
		encoded, _ := json.Marshal(params)
		if args[1] == "list" {
			return "conversation.list", encoded, true, nil
		}
		if args[1] == "read" {
			if len(args) < 4 {
				return "", nil, false, errors.New("conversation read requires instance id and thread id")
			}
			encoded, _ = json.Marshal(map[string]any{"instanceId": args[2], "threadId": args[3], "includeTurns": true})
			return "conversation.read", encoded, true, nil
		}
	case "turn":
		if len(args) < 2 {
			return "", nil, false, usage()
		}
		if args[1] == "start" {
			if len(args) < 5 {
				return "", nil, false, errors.New("turn start requires instance id, thread id, and text")
			}
			encoded, _ := json.Marshal(map[string]any{"instanceId": args[2], "threadId": args[3], "text": strings.Join(args[4:], " ")})
			return "turn.start", encoded, true, nil
		}
		if args[1] == "interrupt" {
			if len(args) < 5 {
				return "", nil, false, errors.New("turn interrupt requires instance id, thread id, and turn id")
			}
			encoded, _ := json.Marshal(map[string]any{"instanceId": args[2], "threadId": args[3], "turnId": args[4]})
			return "turn.interrupt", encoded, true, nil
		}
	case "action":
		if len(args) >= 2 && args[1] == "list" {
			params := map[string]any{}
			if len(args) > 2 {
				params["instanceId"] = args[2]
			}
			encoded, _ := json.Marshal(params)
			return "action.list", encoded, true, nil
		}
		if len(args) >= 5 && args[1] == "resolve" {
			id := json.RawMessage(args[3])
			if !json.Valid(id) {
				id, _ = json.Marshal(args[3])
			}
			result := json.RawMessage(strings.Join(args[4:], " "))
			if !json.Valid(result) {
				return "", nil, false, errors.New("action result must be valid JSON")
			}
			encoded, _ := json.Marshal(map[string]any{"instanceId": args[2], "id": id, "result": result})
			return "action.resolve", encoded, true, nil
		}
	case "catalog":
		if len(args) >= 2 {
			params := map[string]any{}
			if len(args) > 2 {
				params["instanceId"] = args[2]
			}
			encoded, _ := json.Marshal(params)
			if args[1] == "models" {
				return "catalog.models", encoded, true, nil
			}
			if args[1] == "permissions" {
				return "catalog.permissionProfiles", encoded, true, nil
			}
		}
	case "traffic":
		if len(args) < 3 {
			return "", nil, false, errors.New("traffic requires on|off and instance id")
		}
		params, _ := json.Marshal(map[string]string{"instanceId": args[2]})
		if args[1] == "on" {
			return "traffic.enable", params, true, nil
		}
		if args[1] == "off" {
			return "traffic.disable", params, true, nil
		}
	case "call":
		if len(args) < 2 {
			return "", nil, false, errors.New("call requires a control method")
		}
		params := empty
		if len(args) > 2 {
			params = json.RawMessage(strings.Join(args[2:], " "))
			if !json.Valid(params) {
				return "", nil, false, errors.New("call params must be valid JSON")
			}
		}
		return args[1], params, true, nil
	case "ui":
		return "system.ui_url", empty, true, nil
	}
	return "", nil, false, usage()
}

func dial(ctx context.Context, start bool) (net.Conn, error) {
	sid, err := identity.CurrentUser()
	if err != nil {
		return nil, err
	}
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	conn, err := localipc.DialPipe(short, paths.LocalEndpoint(sid))
	cancel()
	if err != nil && start {
		if startErr := startDaemon(); startErr != nil {
			return nil, startErr
		}
		conn, err = waitForDaemon(ctx)
	}
	if err != nil {
		return nil, errors.New("daemon is not running")
	}
	hello := protocol.Hello{Role: "control", Version: protocol.APIRevision}
	if err = ipc.WriteFrame(conn, ipc.Frame{Kind: ipc.KindHello, Metadata: ipc.Metadata(hello)}); err != nil {
		conn.Close()
		return nil, err
	}
	frame, err := ipc.ReadFrame(conn)
	if err != nil || frame.Kind != ipc.KindHelloAck {
		conn.Close()
		return nil, errors.New("daemon handshake failed")
	}
	return conn, nil
}

func waitForDaemon(ctx context.Context) (net.Conn, error) {
	sid, err := identity.CurrentUser()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		short, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, dialErr := localipc.DialPipe(short, paths.LocalEndpoint(sid))
		cancel()
		if dialErr == nil {
			return conn, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("daemon did not start within 5 seconds")
}

func startDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "daemon", "run")
	platformprocess.ConfigureDetached(cmd)
	return cmd.Start()
}

func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func usage() error {
	return errors.New("usage: work-anywhere daemon <run|start|status|stop|force-stop> | status | instance <list|get|restart> | conversation list | action list | catalog <models|permissions> | call <method> [json] | ui | install | uninstall | self-test | version")
}
