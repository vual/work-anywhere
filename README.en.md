<div align="center">
  <br />
  <img src="waw-app-icon.svg" alt="Work Anywhere icon" width="80" />
  <h2>Work Anywhere (WAW)</h2>
  <p><strong>English</strong> | <a href="README.md">简体中文</a></p>
</div>

Work Anywhere (WAW) aims to let you interact with ChatGPT Codex Desktop on your computer in real time from your phone, so you can monitor and control running tasks or issue new instructions whenever you are away from your computer.

Who is it for?

- People who prefer the official ChatGPT Codex Desktop but either do not have a ChatGPT account or have an account but frequently cannot connect to the official Remote feature because of network issues.
- People who need to monitor ChatGPT Codex tasks on their computer from a phone and dispatch new tasks while away.

The official ChatGPT Codex Desktop remains the only desktop presentation layer; this project does not introduce a separate desktop chat UI.

The complete product is organized into three independent modules:

- `desktop`: integrates with the official ChatGPT Codex Desktop, supervises official Codex App Server processes, and exposes local control capabilities.
- `relay`: will relay encrypted connections, task state, and notifications between Desktop daemons and mobile clients.
- `mobile`: will display task progress, handle approvals and questions, receive notifications, and continue conversations.

Only **Desktop V1** is currently implemented. Relay and Mobile are not implemented, so the deployment instructions in this document apply only to Windows Desktop.

## Current capabilities

Desktop V1 provides:

- Integration with the official ChatGPT Codex Desktop through `CODEX_CLI_PATH` without replacing the official UI.
- Automatic discovery of the official `codex.exe` used by Desktop, with runtime upgrades switched at a safe boundary.
- One daemon per Windows user and one supervised official App Server per logical Codex instance.
- Multiple ChatGPT Desktop instances, isolated by Windows SID, `CODEX_HOME`, Electron user-data, and an optional instance name.
- Instance, conversation, Turn-state, and incremental-event observation.
- Local control for starting text/image Turns and selecting a model, reasoning effort, approval policy, and sandbox policy.
- Mid-run approval handling, user-question responses, and Turn interruption.
- Continued App Server ownership after a Desktop Bridge disconnects, with initialize state restored when Desktop reconnects.
- SQLite conversation projections, attachment storage, structured logs, and explicitly enabled full-protocol traffic diagnostics.
- A system tray, local administration page, and command-line control interface.
- Pre-request fallback to the official Codex executable when the daemon cannot be reached, prioritizing Desktop availability.

## Architecture

```text
ChatGPT Desktop A ─► Bridge A ─┐
                               │  per-user protected Named Pipe
ChatGPT Desktop B ─► Bridge B ─┼──────────────────────────────┐
                               │                              ▼
Local CLI / Admin page ────────┘                       Desktop daemon
                                                             │
                                      ┌──────────────────────┼─────────────────────┐
                                      ▼                      ▼                     ▼
                              Codex App Server A      Codex App Server B      Encrypted SQLite
                                      │                      │
                                      └──── official Codex sessions ────┘

Future Mobile ◄──── encrypted connection / notifications ────► Future Relay ◄────► daemon
```

`work-anywhere.exe` is a single binary with multiple modes:

```text
work-anywhere.exe app-server ...   Transparent Bridge invoked by ChatGPT Desktop
work-anywhere.exe daemon run       Resident daemon
work-anywhere.exe <command>        Local control CLI
other Codex arguments              Passed through to the real codex.exe
```

The production path uses the official App Server stdio JSONL transport. Bridge and daemon communicate through the versioned, length-prefixed WAI1 Named Pipe protocol. The daemon owns JSON-RPC ID isolation, initialize virtualization, approval arbitration, App Server lifecycle, and the local control API.

## Repository layout

```text
work-anywhere/
├─ AGENTS.md                    Development and documentation rules
├─ go.work                      Go workspace
└─ desktop/
   ├─ cmd/work-anywhere/        Single-binary entry point
   ├─ internal/
   │  ├─ bridge/                Desktop stdio Bridge
   │  ├─ daemon/                App Server supervisor and control plane
   │  ├─ codex/                 Codex runtime, instance, and argument handling
   │  ├─ jsonrpc/               JSON-RPC envelopes and IDs
   │  ├─ provider/              Extensible Provider interface
   │  ├─ store/                 Encrypted SQLite projection
   │  └─ platform/              Windows/macOS/Linux platform boundary
   ├─ packaging/                Platform installation, signing, and release assets
   ├─ docs/                     Current-state design documentation
   ├─ scripts/                  Build scripts
   └─ testdata/                 Official App Server schema fixtures
```

Shared Bridge and daemon business logic is not copied per operating system. OS-specific behavior lives under `desktop/internal/platform/`, while release configuration lives under `desktop/packaging/<platform>/`. Windows x64 is the only runnable target today. macOS and Linux currently provide cross-compilable adapter skeletons only; see the [platform support design](desktop/docs/platform-support/design.md).

## Installation and deployment

### Requirements

- Windows x64.
- A working installation of the official ChatGPT Codex Desktop.
- Go is not required when using an existing binary. Building from source requires the Go 1.26.5 Windows x64 SDK.
- The current user must be able to write to `%LOCALAPPDATA%\WorkAnywhere`.

### 1. Obtain or build the executable

When a repository build is already available, use:

```text
desktop\dist\windows-amd64\work-anywhere.exe
```

To build from source:

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\scripts\build.ps1
```

The build script uses the pinned Go 1.26.5 toolchain under `desktop\.tools\go-sdk\go`, runs the tests, and produces a single Windows x64 executable.

### 2. Run pre-installation checks

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\dist\windows-amd64\work-anywhere.exe version
.\dist\windows-amd64\work-anywhere.exe self-test
```

`self-test` should report `"ok": true` and show the discovered official Codex path, version, SHA256, `CODEX_HOME`, and user-data directory. If the official executable cannot be found, override it for the current environment:

```powershell
$env:WORK_ANYWHERE_CODEX_EXE = 'C:\path\to\ChatGPT\resources\codex.exe'
.\dist\windows-amd64\work-anywhere.exe self-test
```

### 3. Integrate with ChatGPT Desktop

```powershell
.\dist\windows-amd64\work-anywhere.exe install
```

The installer command:

1. Backs up the current user-level `CODEX_CLI_PATH`.
2. Points the user-level `CODEX_CLI_PATH` to this executable.
3. Broadcasts a Windows environment change notification.

Fully restart every ChatGPT Desktop instance after installation so each process inherits the new environment. Multiple Desktop instances can share one `work-anywhere.exe`; the daemon separates them by `CODEX_HOME` and user-data.

When different Desktop profiles are launched through external scripts, you may also set:

```powershell
$env:CODEX_HOME = 'C:\Users\me\.codex-work'
$env:WORK_ANYWHERE_USER_DATA_DIR = 'C:\Users\me\AppData\Local\OpenAI\CodexProfiles\work'
$env:WORK_ANYWHERE_INSTANCE_NAME = 'work'
```

### 4. Verify the running installation

After opening ChatGPT Desktop:

```powershell
$wa = 'D:\CodeSpace\work-anywhere\desktop\dist\windows-amd64\work-anywhere.exe'
& $wa daemon status
& $wa instance list
& $wa ui
```

The daemon starts on demand. It does not configure Windows login startup and does not exit automatically when Desktop closes or becomes idle. `ui` opens a local administration page bound only to `127.0.0.1`.

## Usage

The following examples assume:

```powershell
$wa = 'D:\CodeSpace\work-anywhere\desktop\dist\windows-amd64\work-anywhere.exe'
```

### Daemon and instances

```powershell
& $wa daemon start
& $wa daemon status
& $wa instance list
& $wa instance get <instance-id>
& $wa instance restart <instance-id>
```

### Conversations and Turns

```powershell
& $wa conversation list <instance-id>
& $wa conversation read <instance-id> <thread-id>
& $wa turn start <instance-id> <thread-id> 'Continue the current task'
& $wa turn interrupt <instance-id> <thread-id> <turn-id>
```

A local control client may start a new Turn only while the conversation is idle. Original requests from Desktop remain transparent and the official App Server decides whether they conflict with concurrent work.

### Approvals and questions

```powershell
& $wa action list <instance-id>
& $wa action resolve <instance-id> <request-id> '<json-result>'
```

`json-result` must follow the official response shape for the corresponding action. Desktop and local control clients may receive an action simultaneously, but only the first valid resolution is sent to App Server. Later responses return `already_resolved`.

### Models, permissions, and diagnostics

```powershell
& $wa catalog models <instance-id>
& $wa catalog permissions <instance-id>
& $wa traffic on <instance-id>
& $wa traffic off <instance-id>
& $wa call <control-method> '<json-params>'
```

Full protocol traffic is disabled by default because it can contain prompts, tool output, file content, and credentials. Enable it only for short-lived diagnostics and disable it immediately afterward.

## Data and security

Runtime data is stored under:

```text
%LOCALAPPDATA%\WorkAnywhere
```

- Instance, conversation, Turn, Timeline, pending-action, attachment, and settings projections are stored in SQLite WAL.
- Message bodies, tool payloads, diffs, actions, and attachment content are encrypted with AES-256-GCM.
- The AES master key is protected by current-user Windows DPAPI.
- The Bridge/daemon Named Pipe is protected by a current-user SID ACL.
- The administration page binds to a random loopback port and uses a one-time token, SameSite session, Origin checks, and CSRF protection.
- Full process environments are not persisted, and complete JSONL traffic is not recorded by default.
- Official Codex session files remain the authoritative source for complete conversations. The Work Anywhere database is a control-plane projection and must not overwrite official sessions if damaged.

## Stopping and uninstalling

Stop the daemon normally:

```powershell
& $wa daemon stop
```

Normal shutdown is rejected while a Turn is active. To explicitly interrupt active work, use the tray confirmation or:

```powershell
& $wa daemon force-stop
```

Uninstall the Bridge:

```powershell
& $wa uninstall
```

Uninstall restores the user-level `CODEX_CLI_PATH` backed up during installation. Fully restart ChatGPT Desktop afterward.

## Troubleshooting

### The official codex.exe cannot be found

Run `self-test` first and set `WORK_ANYWHERE_CODEX_EXE` if necessary. See [runtime and instance design](desktop/docs/runtime-and-instance/design.md) for discovery order and multi-instance identity rules.

### The daemon is not running

```powershell
& $wa daemon start
& $wa daemon status
```

Structured logs are stored under `%LOCALAPPDATA%\WorkAnywhere\logs`.

### A conversation reports an active writer

One official session cannot be owned by two independent App Server writers simultaneously. Only instances created or connected through this Bridge can be fully controlled by the daemon. Work Anywhere does not forcibly take over sessions already owned by another Codex process.

### Desktop behaves unexpectedly after an upgrade

Run `self-test` again and inspect the discovered path and version. Idle instances switch to a new runtime immediately; active Turns switch at a safe boundary. If the official application removes or changes `CODEX_CLI_PATH`, the Desktop integration layer will need to be updated.

## Development and verification

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\.tools\go-sdk\go\bin\go.exe test ./...
.\.tools\go-sdk\go\bin\go.exe vet ./...
.\scripts\build.ps1
.\dist\windows-amd64\work-anywhere.exe self-test
```

See [`AGENTS.md`](AGENTS.md) for repository development rules and [`desktop/docs/README.md`](desktop/docs/README.md) for current Desktop design documentation. Functional changes must update every affected design document in the same change. Design documents describe only the current state and are not changelogs.

## Current limitations

- Runtime support is limited to Windows x64.
- Relay, Mobile, remote pairing, and mobile push notifications are not implemented.
- Codex is the only Provider. Claude Code, OpenCode, Hermes, Pi, and other drivers are not implemented.
- `CODEX_CLI_PATH` is the currently available Desktop integration point, but it is not guaranteed to remain compatible forever.
- Experimental WebSocket transport is available only through `app-server --standalone-ws` diagnostics. The production daemon path always uses stdio.
- `desktop/codex-stdio-ws-bridge` is the previous C# rollback implementation and is not part of the current Go build.
