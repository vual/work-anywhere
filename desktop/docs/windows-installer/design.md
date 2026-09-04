# Windows installer

## Goal and scope

为 Windows x64 生成可分发的单用户安装包，将 `work-anywhere.exe` 安装到当前用户目录，并在安装阶段把用户级 `CODEX_CLI_PATH` 指向已安装的 Bridge。安装完成后必须明确提示用户完全重启 ChatGPT Desktop。

本功能只负责 Desktop 安装、升级和卸载，不负责 Relay、Mobile、登录自启动或系统级安装。

## Current architecture and data flow

安装包由 Inno Setup 6 生成，输入是 `desktop/dist/windows-amd64/work-anywhere.exe`，输出是 Windows x64 `work-anywhere-setup-<version>.exe`。Windows EXE 构建会将根目录 `waw-app-icon.svg`复制到 Web 静态资源，并生成包含该图形的 Windows icon resource。

```text
build.ps1
    │
    ▼
work-anywhere.exe
    │  bundled into
    ▼
Inno Setup installer
    │
    ├─ %LOCALAPPDATA%\Programs\WorkAnywhere\work-anywhere.exe
    ├─ work-anywhere.exe install
    │      ├─ backup current HKCU\Environment\CODEX_CLI_PATH
    │      ├─ set HKCU\Environment\CODEX_CLI_PATH to installed EXE
    │      └─ broadcast WM_SETTINGCHANGE
    └─ finish page: restart ChatGPT Desktop prompt
```

安装目录使用 `{localappdata}\Programs\WorkAnywhere`，`PrivilegesRequired=lowest`，因此不需要管理员权限。环境变量写入、原值备份和卸载恢复由 `internal/platform/installation` 的现有实现负责，安装器不重复实现这些规则。

## Interfaces

构建安装包：

```powershell
cd desktop
.\scripts\build-installer.ps1 -Version 0.1.0
```

可通过 `-IsccPath` 指定 `ISCC.exe` 路径，也可以使用 `-SkipBuild` 复用已经生成的 Desktop EXE。

安装器行为：

- 安装前保留当前用户的 `CODEX_CLI_PATH` 备份。
- 安装后调用 `<安装目录>\work-anywhere.exe install`。
- 安装成功时提示完全退出并重新启动所有 ChatGPT Desktop 实例。
- 安装失败时显示错误，并保留已安装文件，便于用户手动执行 `work-anywhere.exe install` 重试。
- 开始卸载时先尝试停止 daemon，再调用 `work-anywhere.exe uninstall` 恢复原环境变量。
- 卸载不删除 `%LOCALAPPDATA%\WorkAnywhere`，避免误删会话、附件、日志和环境变量备份。

## State and persistence

安装器自身不保存会话数据。`work-anywhere.exe install` 使用 `%LOCALAPPDATA%\WorkAnywhere\install\codex-cli-path-backup.json` 保存原始用户级环境变量，卸载时读取该文件恢复原值。

## Security and failure handling

- 安装器是当前用户安装包，不申请管理员权限。
- `CODEX_CLI_PATH` 写入 HKCU，不修改系统级环境变量。
- 安装目录不应写入证书、私钥、生成的发布二进制或用户会话数据。
- 环境变量变更通过 `WM_SETTINGCHANGE` 广播，但已运行的 ChatGPT Desktop 不会自动重新读取变量，必须完全退出并重新启动。
- 安装器不强制关闭 ChatGPT Desktop；重启提示由安装完成页显示。
- daemon 或 App Server 正在运行时，更新文件可能被 Windows 延迟处理；升级前建议退出 ChatGPT Desktop，并在需要时执行 `daemon stop`。

## Invariants and non-goals

- 安装后的用户级 `CODEX_CLI_PATH` 必须指向安装目录中的 `work-anywhere.exe`。
- 安装包只支持 Windows x64；macOS/Linux 目录仅保留未来发行配置边界。
- 安装包不包含官方 ChatGPT/Codex 二进制，不替代官方 Desktop，也不配置登录自启动。
- 安装器完成页的“重启 ChatGPT”是用户操作提示，不代表安装器能够刷新已有 Desktop 进程。

## Code ownership

- Inno Setup 定义：[`desktop/packaging/windows/installer.iss`](../../packaging/windows/installer.iss)
- 安装包构建入口：[`desktop/scripts/build-installer.ps1`](../../scripts/build-installer.ps1)
- EXE 构建入口：[`desktop/scripts/build.ps1`](../../scripts/build.ps1)
- 环境变量安装/卸载：[`desktop/internal/platform/installation/installation_windows.go`](../../internal/platform/installation/installation_windows.go)
- 使用说明：[`desktop/README.md`](../../README.md)、仓库 [`README.md`](../../../README.md) 和 [`README.en.md`](../../../README.en.md)

## Verification

在安装了 Inno Setup 6 的 Windows x64 环境中：

```powershell
cd desktop
.\scripts\build-installer.ps1 -Version 0.1.0
```

验证安装包后检查：

```powershell
$wa = "$env:LOCALAPPDATA\Programs\WorkAnywhere\work-anywhere.exe"
& $wa version
[Environment]::GetEnvironmentVariable('CODEX_CLI_PATH', 'User')
& $wa self-test
```

再完全退出并重新打开 ChatGPT Desktop，确认 Bridge 连接到 daemon。卸载后确认 `CODEX_CLI_PATH` 恢复为安装前值，并确认 `%LOCALAPPDATA%\WorkAnywhere` 未被删除。

## Documentation update triggers

修改安装目录、安装权限、环境变量备份格式、安装/卸载命令、重启提示、Inno Setup 参数或发布输出路径时，必须同步更新本文档、Windows packaging README、Desktop README 和仓库中英文 README。
