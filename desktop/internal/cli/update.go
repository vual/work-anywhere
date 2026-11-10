package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update"
)

type controlError struct {
	Code    string
	Message string
}

func (e *controlError) Error() string { return e.Code + ": " + e.Message }

const updateUsage = "usage: update <status|check|apply [--yes] [--interrupt]|cancel>"

func runUpdate(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(updateUsage)
	}
	var yes, interrupt bool
	if args[0] == "apply" {
		for _, option := range args[1:] {
			switch option {
			case "--yes":
				yes = true
			case "--interrupt":
				interrupt = true
			default:
				return errors.New(updateUsage)
			}
		}
	} else if len(args) != 1 {
		return errors.New(updateUsage)
	}
	method := map[string]string{"status": "system.update.status", "check": "system.update.check", "apply": "system.update.check", "cancel": "system.update.cancel"}[args[0]]
	if method == "" {
		return errors.New(updateUsage)
	}
	conn, err := dial(ctx, false)
	if err != nil {
		return err
	}
	defer conn.Close()
	status, err := callUpdate(conn, method, nil)
	if err != nil {
		return err
	}
	if args[0] != "apply" {
		return printJSON(status)
	}
	if status.Latest == nil || status.State == update.StateUpToDate {
		fmt.Fprintf(out, "Work Anywhere %s is up to date\n", status.CurrentVersion)
		return nil
	}
	if !status.CanApply {
		return fmt.Errorf("%s: this copy cannot update itself (install kind %q); download %s manually", update.CodeUnsupportedInstall, status.InstallKind, status.Latest.NotesURL)
	}
	if !yes {
		fmt.Fprintf(out, "Update Work Anywhere %s -> %s (%s)? [y/N] ", status.CurrentVersion, status.Latest.Version, formatBytes(status.Latest.Size))
		if !confirmed(in) {
			return errors.New("update cancelled")
		}
	}
	params, _ := json.Marshal(map[string]any{"version": status.Latest.Version, "confirmInterrupt": interrupt})
	status, err = callUpdate(conn, "system.update.apply", params)
	var coded *controlError
	if errors.As(err, &coded) && coded.Code == update.CodeActiveTurns {
		return fmt.Errorf("%s; rerun with --interrupt to stop them and update", err)
	}
	if err != nil {
		return err
	}
	return waitForInstaller(ctx, conn, status, out)
}

// waitForInstaller polls until the daemon hands over to the installer.
func waitForInstaller(ctx context.Context, conn net.Conn, status update.Status, out io.Writer) error {
	lastPercent := -1
	for {
		switch status.State {
		case update.StateApplying:
			fmt.Fprintf(out, "installer started for %s; the daemon restarts after the upgrade\n", status.Latest.Version)
			return nil
		case update.StateError:
			if status.Error != nil {
				return fmt.Errorf("%s: %s", status.Error.Code, status.Error.Message)
			}
			return errors.New("update failed")
		case update.StateDownloading, update.StateDownloaded:
			if status.Progress != nil && status.Progress.Total > 0 {
				percent := int(status.Progress.Received * 100 / status.Progress.Total)
				if percent/10 != lastPercent/10 {
					fmt.Fprintf(out, "downloading %d%%\n", percent)
					lastPercent = percent
				}
			}
		default:
			return fmt.Errorf("unexpected update state %q", status.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		next, err := callUpdate(conn, "system.update.status", nil)
		if err != nil {
			// The daemon exits right after launching the installer.
			if status.State == update.StateDownloaded {
				fmt.Fprintln(out, "installer started; the daemon restarts after the upgrade")
				return nil
			}
			return err
		}
		status = next
	}
}

func callUpdate(conn net.Conn, method string, params json.RawMessage) (update.Status, error) {
	if params == nil {
		params = json.RawMessage(`{}`)
	}
	result, err := callDaemon(conn, method, params)
	if err != nil {
		return update.Status{}, err
	}
	data, _ := json.Marshal(result)
	var status update.Status
	if err := json.Unmarshal(data, &status); err != nil {
		return update.Status{}, fmt.Errorf("decode update status: %w", err)
	}
	return status, nil
}

func confirmed(in io.Reader) bool {
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func formatBytes(size int64) string {
	if size <= 0 {
		return "unknown size"
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
}
