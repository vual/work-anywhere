# Windows installer

## Goal and scope

为 Windows x64 生成可分发的单用户安装包，将 `work-anywhere.exe` 安装到当前用户目录，默认配置登录自启动，并可选地把用户级 `CODEX_CLI_PATH` 指向已安装的 Bridge（ChatGPT Codex Desktop 集成）。只使用 Claude Code 时无需 ChatGPT Desktop 即可运行。

本功能只负责 Desktop 安装、升级和卸载，不负责 Relay、Mobile 或系统级安装。

## Current architecture and data flow

安装包由 Inno Setup 6 生成，输入是 `desktop/dist/windows-amd64/work-anywhere.exe`，输出是 Windows x64 `work-anywhere-setup-<version>.exe`（Inno Setup 自带的 EXE 安装器）。

```text
Inno Setup installer
    │
    ├─ PrepareToInstall: existing work-anywhere.exe daemon stop
    ├─ %LOCALAPPDATA%\Programs\WorkAnywhere\work-anywhere.exe
    ├─ Start menu "Work Anywhere" → work-anywhere.exe ui
    ├─ task autostart (default checked) → work-anywhere.exe autostart enable
    │                       (unchecked)  → work-anywhere.exe autostart disable
    ├─ task codex (optional) → work-anywhere.exe install
    │      ├─ backup current HKCU\Environment\CODEX_CLI_PATH
    │      ├─ set HKCU\Environment\CODEX_CLI_PATH to installed EXE
    │      └─ broadcast WM_SETTINGCHANGE + restart ChatGPT Desktop prompt
    └─ finish page "立即启动 Work Anywhere" → work-anywhere.exe daemon start --background
```

安装目录使用 `{localappdata}\Programs\WorkAnywhere`，`PrivilegesRequired=lowest`，因此不需要管理员权限。自启动键值由 [`internal/platform/autostart`](../../internal/platform/autostart/) 管理，环境变量写入、原值备份和卸载恢复由 `internal/platform/installation` 负责，安装器只调用 EXE 命令，不重复实现这些规则。

## Interfaces

构建安装包：

```powershell
cd desktop
.\scripts\build-installer.ps1 -Version 0.1.0
```

可通过 `-IsccPath` 指定 `ISCC.exe` 路径，也可以使用 `-SkipBuild` 复用已经生成的 Desktop EXE。

安装器任务（`[Tasks]`）：

- `autostart`：“登录 Windows 时启动 Work Anywhere”，默认勾选。勾选时执行 `autostart enable`，取消时执行 `autostart disable`。
- `codex`：“集成 ChatGPT Codex Desktop”。首次安装且检测到 `%LOCALAPPDATA%\OpenAI\Codex` 时在任务页预先勾选，否则默认不勾；升级沿用上次选择。勾选时执行 `install` 并提示完全重启 ChatGPT Desktop；不勾选时不修改 `CODEX_CLI_PATH`。静默安装不显示任务页，因此只启用默认勾选的 `autostart`。

其他行为：

- 开始菜单 “Work Anywhere” 运行 `work-anywhere.exe ui`，daemon 未运行时自动启动并打开管理页。
- 完成页 “立即启动 Work Anywhere” 默认勾选，运行 `daemon start --background`。
- 任一步骤失败时汇总显示错误和可手动重试的命令，已安装文件保留。
- 卸载顺序：`daemon stop` → `autostart disable` → `uninstall`。`uninstall` 只在 `CODEX_CLI_PATH` 仍指向该 EXE 时恢复备份值（无备份则删除），否则保持不变。
- 卸载不删除 `%LOCALAPPDATA%\WorkAnywhere`，避免误删会话、附件、日志和环境变量备份。

## State and persistence

安装器自身不保存会话数据，只在 Inno Setup 卸载信息中记录 `CodexTaskChosen`，用于区分首次安装与升级。`work-anywhere.exe install` 使用 `%LOCALAPPDATA%\WorkAnywhere\install\codex-cli-path-backup.json` 保存原始用户级环境变量，`uninstall` 恢复后删除该文件。自启动保存在 HKCU `Software\Microsoft\Windows\CurrentVersion\Run` 的 `WorkAnywhere` 值。

## Security and failure handling

- 安装器是当前用户安装包，不申请管理员权限；自启动与 `CODEX_CLI_PATH` 均写入 HKCU。
- 环境变量变更通过 `WM_SETTINGCHANGE` 广播，但已运行的 ChatGPT Desktop 不会自动重新读取变量，必须完全退出并重新启动。
- 安装器不强制关闭 ChatGPT Desktop。升级前先停止 daemon；若 ChatGPT Desktop 拉起的 Bridge 仍占用 EXE，Inno Setup 显示文件占用的重试对话框，用户退出 ChatGPT Desktop 后重试。
- 卸载不会覆盖不属于 Work Anywhere 的 `CODEX_CLI_PATH`。

## Invariants and non-goals

- 安装后只有一个 `work-anywhere.exe`；Bridge 与 daemon 在任务管理器中均显示为 `work-anywhere.exe`。
- 勾选 `codex` 后用户级 `CODEX_CLI_PATH` 必须指向安装目录中的 `work-anywhere.exe`。
- 安装包只支持 Windows x64；不包含官方 ChatGPT/Codex 或 Claude Code 二进制，不提供 MSI/MSIX 或 Windows Service。
- 完成页的“重启 ChatGPT”是用户操作提示，不代表安装器能够刷新已有 Desktop 进程。

## Code ownership

- Inno Setup 定义：[`desktop/packaging/windows/installer.iss`](../../packaging/windows/installer.iss)
- 安装包构建入口：[`desktop/scripts/build-installer.ps1`](../../scripts/build-installer.ps1)
- EXE 构建入口：[`desktop/scripts/build.ps1`](../../scripts/build.ps1)
- 环境变量安装/卸载：[`desktop/internal/platform/installation/installation_windows.go`](../../internal/platform/installation/installation_windows.go)
- 登录自启动：[`desktop/internal/platform/autostart/`](../../internal/platform/autostart/)
- 使用说明：[`desktop/README.md`](../../README.md)、仓库 [`README.md`](../../../README.md) 和 [`README.en.md`](../../../README.en.md)

## Verification

在安装了 Inno Setup 6 的 Windows x64 环境中：

```powershell
cd desktop
.\scripts\build-installer.ps1 -Version 0.1.0
```

安装后检查：

```powershell
$wa = "$env:LOCALAPPDATA\Programs\WorkAnywhere\work-anywhere.exe"
& $wa version
& $wa autostart status
[Environment]::GetEnvironmentVariable('CODEX_CLI_PATH', 'User')
& $wa self-test
```

人工 E2E：

1. 默认选项安装，不运行 ChatGPT Desktop：完成页启动后任务管理器出现一个 `work-anywhere.exe`；开始菜单 “Work Anywhere” 打开管理页并显示 Claude Code 实例；注销重登后 daemon 自动运行。
2. 勾选 Codex 集成并重启 ChatGPT Desktop：出现 Bridge 与 daemon 两个 `work-anywhere.exe`，管理页同时显示 Codex 与 Claude Code。
3. 卸载后 Run 键被删除、`CODEX_CLI_PATH` 恢复为安装前值，`%LOCALAPPDATA%\WorkAnywhere` 保留。

## Documentation update triggers

修改安装目录、安装权限、安装任务及默认值、自启动键值、环境变量备份格式、安装/卸载命令或顺序、重启提示、Inno Setup 参数或发布输出路径时，必须同步更新本文档、Windows packaging README、Desktop README 和仓库中英文 README。
