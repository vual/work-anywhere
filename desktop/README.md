# Work Anywhere Desktop

This module contains the Windows-first Go bridge and local daemon for the official ChatGPT Codex desktop app. One executable has three roles:

```text
work-anywhere.exe app-server ...    # transparent desktop bridge
work-anywhere.exe app-server --standalone-ws # experimental compatibility diagnostic
work-anywhere.exe daemon run        # resident local daemon
work-anywhere.exe <command>         # local control CLI
```

The bridge preserves Codex App Server JSONL bytes over a per-user ACL-protected Named Pipe. The daemon owns one official App Server process per stable logical Codex profile, multiplexes desktop/control requests, and keeps the process alive when the desktop window closes.

The supervised path only handles recognized long-running stdio invocations. Startup policy checks (`app-server --print-startup-config`, including `--startup-sign-out`), help/version, schema generation, daemon/proxy, non-stdio transports and unknown syntax pass through unchanged to the official CLI without starting the WAW daemon. The bridge recognizes `app-server` even when ChatGPT prepends global Codex options such as `-c key=value`. `--standalone-ws` is retained only for diagnostics against Codex's experimental WebSocket transport; it is not used by the daemon architecture.

The daemon also contains an optional outbound Relay connector. It performs device enrollment, explicit QR-URI pairing, Noise end-to-end encryption, remote scope enforcement, paginated Mobile conversation synchronization, event forwarding, and generic push hints without exposing the local Pipe or administration listener.

Shared Bridge/daemon logic lives under `internal/`; operating-system adapters live under `internal/platform/`, and installer/signing assets belong under `packaging/<platform>/`. Windows x64 is the only currently supported runtime target. See the [platform support design](docs/platform-support/design.md) for the current capability matrix.

## Design documentation

The current design, feature ownership, protocol boundaries, and documentation maintenance rules are indexed in [`docs/README.md`](docs/README.md). Functional changes must update the affected design documents in the same change; those documents describe only the latest implemented state and are not changelogs.

## Build

The repository pins Go 1.27.3. Place the Windows x64 SDK at `.tools\go-sdk\go`, then run:

```powershell
.\scripts\build.ps1 -Version 0.1.0
```

Output: `dist\windows-amd64\work-anywhere.exe`.

When the installed executable is running, build a reviewable candidate without replacing it:

```powershell
.\scripts\build.ps1 -OutputDirectory "$PWD\dist\windows-amd64\candidate"
```

The override must be an absolute directory. Run both self-tests on the candidate; replace the installed executable only after all turns/actions are idle and the old daemon has stopped.

To create a per-user Windows installer, install Inno Setup 6 and run:

```powershell
.\scripts\build-installer.ps1 -Version 0.1.0
```

The installer is written to `dist\windows-amd64\work-anywhere-setup-0.1.0.exe`. It installs under `%LOCALAPPDATA%\Programs\WorkAnywhere`, adds a Start menu "Work Anywhere" entry (opens the admin page and starts the daemon), enables login autostart by default, and optionally integrates ChatGPT Codex Desktop by writing the user-level `CODEX_CLI_PATH` (the previous value is preserved for uninstall, and you are prompted to fully restart ChatGPT Desktop). The daemon runs on its own, so Claude Code-only setups do not need ChatGPT Desktop. In Task Manager both the daemon and any ChatGPT-launched bridge appear as `work-anywhere.exe`. See the [installer design](docs/windows-installer/design.md).

Installed copies check the signed update manifest from GitHub Releases automatically. When a new version is available, the admin page and tray offer "Update now"; the daemon then downloads and verifies the installer, upgrades silently, and restarts on the new version. Portable copies only report new versions. See [Desktop self-update](docs/self-update/design.md).

Login autostart can also be managed from the admin page or the CLI:

```powershell
.\dist\windows-amd64\work-anywhere.exe autostart enable|disable|status
.\dist\windows-amd64\work-anywhere.exe daemon start --background
```

## Try it without installing

```powershell
$env:WORK_ANYWHERE_CODEX_EXE = 'C:\path\to\official\resources\codex.exe'
.\dist\windows-amd64\work-anywhere.exe self-test
.\dist\windows-amd64\work-anywhere.exe self-test --protocol
.\dist\windows-amd64\work-anywhere.exe daemon start
.\dist\windows-amd64\work-anywhere.exe status
.\dist\windows-amd64\work-anywhere.exe ui
```

管理页直接提供 Relay 地址、Desktop 注册、重新连接、生成/复制移动端配对 URI、批准/拒绝配对请求和撤销已授权设备。CLI 命令仍保留，适合脚本、无界面环境和诊断。

Relay 使用 `self_service` 模式时，点击“启用远程访问”自动登记并连接，无需 token；`invite` 模式会显示邀请码输入框，填写后再次点击“启用远程访问”提交。旧 Relay 可主动使用“旧版邀请码注册”，不会自动降级。CLI 使用 `remote configure <origin>` 后自动登记，可通过 `remote register [display-name]` 重试；邀请码可通过本机 `remote.register` 的 `token` 参数提交。配置切换或身份损坏时必须明确重置，`remote configure <origin> --reset-identity` 会清除远程身份和全部手机配对，随后需要重新扫码。

## Install for ChatGPT Desktop

```powershell
.\dist\windows-amd64\work-anywhere.exe install
```

This backs up the current user-level `CODEX_CLI_PATH`, points it at the Go bridge, and broadcasts the Windows environment change. Restart each ChatGPT Desktop instance afterward. This step is only needed for Codex; Claude Code works without it. To restore the previous value (only applied while `CODEX_CLI_PATH` still points at this executable):

```powershell
.\dist\windows-amd64\work-anywhere.exe uninstall
```

Runtime files and logs stay under `%LOCALAPPDATA%\WorkAnywhere`. Relay identity, pairings, and thread settings use `%USERPROFILE%\.work-anywhere\config.json` with current-user DPAPI protection. Conversation content and runtime projections are not persisted. Full wire traffic is not recorded by default.

Remote streaming uses bounded semantic `conversation.delta` batches and per-session high/low priority queues. A daemon restart changes `syncEpoch`; clients recover gaps and terminal state from authoritative conversation snapshots.

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
remote status
remote configure <http-or-https-origin> [off] [--reset-identity]
remote register [display-name]
remote enroll <token> [display-name]
remote pair start [ttl]|list|approve|reject|cancel|revoke
remote reconnect
update status|check|apply [--yes] [--interrupt]|cancel
traffic on|off <instance-id>
call <control-method> [json-params]
ui
self-test [--protocol]
```

The previous C# WebSocket bridge remains in `codex-stdio-ws-bridge` as a rollback implementation and is not changed by the Go build.

`self-test` is a basic discovery check, not a protocol check (`protocolVerified: false`). Opt into `self-test --protocol` to validate the real startup entrypoint with closed stdin and the current account/network configuration, then perform an isolated initialize/initialized/read-only handshake. It never requests a model response or signs out. Failures produce `ok: false` and a nonzero exit code. The current protocol fixture and comparison commands are documented in [compatibility design](docs/build-install-and-compatibility/design.md).
