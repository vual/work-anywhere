//go:build windows

package autostart

import (
	"fmt"
	"os"
	"testing"
)

func TestRegistryEntryLifecycle(t *testing.T) {
	name := fmt.Sprintf("WorkAnywhereTest-%d", os.Getpid())
	executable := `C:\Example\work-anywhere.exe`
	t.Cleanup(func() { _ = disable(name) })

	status, err := get(name, executable)
	if err != nil || status.Enabled {
		t.Fatalf("initial status = %+v, %v", status, err)
	}
	if err = enable(name, executable); err != nil {
		t.Fatal(err)
	}
	status, err = get(name, executable)
	if err != nil || !status.Enabled || !status.Current || status.Command != Command(executable) {
		t.Fatalf("enabled status = %+v, %v", status, err)
	}
	status, err = get(name, `C:\Other\work-anywhere.exe`)
	if err != nil || !status.Enabled || status.Current {
		t.Fatalf("foreign status = %+v, %v", status, err)
	}
	if err = disable(name); err != nil {
		t.Fatal(err)
	}
	if err = disable(name); err != nil {
		t.Fatalf("second disable: %v", err)
	}
	status, err = get(name, executable)
	if err != nil || status.Enabled {
		t.Fatalf("disabled status = %+v, %v", status, err)
	}
}
