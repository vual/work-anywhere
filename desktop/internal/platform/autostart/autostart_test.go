package autostart

import "testing"

func TestCommandRoundTrip(t *testing.T) {
	executable := `C:\Users\Example User\AppData\Local\Programs\WorkAnywhere\work-anywhere.exe`
	command := Command(executable)
	if command != `"`+executable+`" daemon start --background` {
		t.Fatalf("unexpected command %q", command)
	}
	if got := commandExecutable(command); got != executable {
		t.Fatalf("commandExecutable(%q) = %q", command, got)
	}
}

func TestCommandExecutableUnquoted(t *testing.T) {
	if got := commandExecutable(`C:\tools\work-anywhere.exe daemon start`); got != `C:\tools\work-anywhere.exe` {
		t.Fatalf("unexpected executable %q", got)
	}
	if got := commandExecutable(`  C:\tools\work-anywhere.exe  `); got != `C:\tools\work-anywhere.exe` {
		t.Fatalf("unexpected executable %q", got)
	}
}
