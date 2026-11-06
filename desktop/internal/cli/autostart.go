package cli

import (
	"errors"

	"github.com/hurf/work-anywhere/desktop/internal/platform/autostart"
)

// parseDaemonStart parses the options after "daemon start". --background
// suppresses informational output for login and installer launches.
func parseDaemonStart(args []string) (bool, error) {
	switch {
	case len(args) == 0:
		return false, nil
	case len(args) == 1 && args[0] == "--background":
		return true, nil
	default:
		return false, errors.New("usage: daemon start [--background]")
	}
}

func runAutostart(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: autostart <enable|disable|status>")
	}
	switch args[0] {
	case "enable":
		if err := autostart.Enable(); err != nil {
			return err
		}
	case "disable":
		if err := autostart.Disable(); err != nil {
			return err
		}
	case "status":
	default:
		return errors.New("usage: autostart <enable|disable|status>")
	}
	status, err := autostart.Get()
	if err != nil {
		return err
	}
	return printJSON(status)
}
