# Work Anywhere Desktop

This module contains the Windows-first Go bridge and local daemon for the official ChatGPT Codex desktop app. One executable has three roles:

```text
work-anywhere.exe app-server ...    # transparent desktop bridge
work-anywhere.exe app-server --standalone-ws # experimental compatibility diagnostic
work-anywhere.exe daemon run        # resident local daemon
work-anywhere.exe <command>         # local control CLI
```

The bridge preserves Codex App Server JSONL bytes over a per-user ACL-protected Named Pipe. The daemon owns one official App Server process per stable logical Codex profile, multiplexes desktop/control requests, and keeps the process alive when the desktop window closes.

The normal path uses the official stdio transport. `--standalone-ws` is retained only for diagnostics against Codex's experimental WebSocket transport; it is not used by the daemon architecture.

Shared Bridge/daemon logic lives under `internal/`; operating-system adapters live under `internal/platform/`, and installer/signing assets belong under `packaging/<platform>/`. Windows x64 is the only currently supported runtime target. See the [platform support design](docs/platform-support/design.md) for the current capability matrix.

## Design documentation

The current design, feature ownership, protocol boundaries, and documentation maintenance rules are indexed in [`docs/README.md`](docs/README.md). Functional changes must update the affected design documents in the same change; those documents describe only the latest implemented state and are not changelogs.

## Build

The repository pins Go 1.26.5. Place the Windows x64 SDK at `.tools\go-sdk\go`, then run:

```powershell
.\scripts\build.ps1 -Version 0.1.0
```

Output: `dist\windows-amd64\work-anywhere.exe`.

To create a per-user Windows installer, install Inno Setup 6 and run:

```powershell
.\scripts\build-installer.ps1 -Version 0.1.0
```

The installer is written to `dist\windows-amd64\work-anywhere-setup-0.1.0.exe`. It installs under `%LOCALAPPDATA%\Programs\WorkAnywhere`, writes the user-level `CODEX_CLI_PATH`, preserves the previous value for uninstall, and prompts you to fully restart ChatGPT Desktop when setup finishes.

## Try it without installing

```powershell
$env:WORK_ANYWHERE_CODEX_EXE = 'C:\path\to\official\resources\codex.exe'
.\dist\windows-amd64\work-anywhere.exe self-test
.\dist\windows-amd64\work-anywhere.exe daemon start
.\dist\windows-amd64\work-anywhere.exe status
.\dist\windows-amd64\work-anywhere.exe ui
```

## Install for ChatGPT Desktop

```powershell
.\dist\windows-amd64\work-anywhere.exe install
```

This backs up the current user-level `CODEX_CLI_PATH`, points it at the Go bridge, and broadcasts the Windows environment change. Restart each ChatGPT Desktop instance afterward. To restore the previous value:

```powershell
.\dist\windows-amd64\work-anywhere.exe uninstall
```

Data is kept under `%LOCALAPPDATA%\WorkAnywhere`. Normalized sensitive content is encrypted with AES-256-GCM; its master key is protected by current-user Windows DPAPI. Full wire traffic is not recorded by default.

## Useful commands

```text
daemon start|status|stop|force-stop
instance list|get|restart
conversation list [instance-id]
conversation read <instance-id> <thread-id>
turn start <instance-id> <thread-id> <text>
turn interrupt <instance-id> <thread-id> <turn-id>
action list [instance-id]
action resolve <instance-id> <request-id> <json-result>
catalog models|permissions [instance-id]
traffic on|off <instance-id>
call <control-method> [json-params]
ui
self-test
```

The previous C# WebSocket bridge remains in `codex-stdio-ws-bridge` as a rollback implementation and is not changed by the Go build.
