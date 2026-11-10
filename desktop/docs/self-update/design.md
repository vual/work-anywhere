# Desktop self-update

## Goal and scope

已用 [Windows 安装包](../windows-installer/design.md) 安装的 Desktop 能自动发现新的正式版本。用户在管理页、托盘或 CLI 中点击“立即更新”后，daemon 依次下载安装包、校验，再静默升级；升级完成后 daemon 自动以新版本重新启动，并保留用户的安装选项。

范围：

- 只支持 Windows x64，以及安装在 `%LOCALAPPDATA%\Programs\WorkAnywhere\work-anywhere.exe` 的副本。
- 更新来源是 [更新清单协议](manifest.md) 定义的签名清单，平台键为 `windows-amd64-setup`。
- 更新必须由用户确认；daemon 只会自动检查和提示，从不自行安装。

## Current architecture and data flow

```text
daemon start
   │ 30 s 后首次检查，之后每 24 h ± 1 h 抖动；手动检查不受限制
   ▼
internal/update.Checker ── HTTPS ──► update-manifest.json（GitHub 或镜像）
   │ 验签 → 解析 → version > appinfo.Version ?
   ▼
state = available ──► 管理页横幅 / 托盘通知 / system.update.changed 事件
   │ 用户点击“立即更新”（system.update.apply）
   ▼
Downloader ── HTTPS ──► %LOCALAPPDATA%\WorkAnywhere\updates\<version>\work-anywhere-setup.exe.part
   │ size + SHA-256 校验 → 原子重命名为 work-anywhere-setup.exe
   ▼
Preflight
   ├─ 不是安装版副本            → update_unsupported_install
   ├─ 有 Codex Desktop Bridge 连接 → update_blocked_by_bridge
   └─ 有活跃 Turn                → 需要用户二次确认（同托盘退出）
   ▼
写入 updates\pending.json → 立即前再次校验 SHA-256 → 以独立进程启动安装器 → daemon 正常退出
   ▼
Inno Setup（静默）：等待 /WAWPID 退出 → daemon stop → 等待 EXE 可写（≤15 s） → 替换文件 → [Run] daemon start --background
   ▼
新 daemon 启动：读取 pending.json → 判定成功或失败 → 清理下载目录 → 管理页提示结果
```

### Version comparison

当前版本取自 `appinfo.BuildInfo.Version`（由 `build.ps1` 的 `-X main.version` 注入）。按 SemVer 2.0 比较：

- 只有清单的 `version` 严格大于当前版本时才进入 `available`。
- 当前版本无法按 SemVer 解析（例如本地开发构建）时，自动检查关闭，手动检查只显示最新版本，不提供“立即更新”。

### Silent installer invocation

```text
work-anywhere-setup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /NOCANCEL
                        /WAWUPDATE=1 /WAWPID=<daemon pid> /LOG="<updates>\<version>\install.log"
                        /TASKS="<autostart|!autostart>,<codex|!codex>"
```

- `/TASKS` 由 daemon 按当前状态显式生成，两个任务总是同时给出（`!` 表示取消）。原因是静默模式下不会执行安装器任务页的 `CurPageChanged` 默认逻辑；不显式传参，升级就可能丢掉用户之前的选择。
  - 自启动：`autostart.Get()` 显示 Run 键已存在且指向当前 EXE 时选 `autostart`，否则选 `!autostart`。
  - Codex 集成：`installation.BridgeStatus.Installed` 为 true 时选 `codex`，否则选 `!codex`。
- `/WAWUPDATE=1` 让安装器进入更新模式：不弹任何消息框，失败时恢复旧 daemon（见“Security and failure handling”）。
- `/WAWPID` 是启动安装器的 daemon PID。安装器先等待该进程退出（最长 30 秒），再执行 `daemon stop`，并轮询直到安装目录 EXE 可以独占写入（最长 15 秒）；超时则在更新模式下中止安装。
- 安装器进程不能加入 daemon 管理的 Job 集合，必须以独立进程（`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`、隐藏窗口）启动，否则 daemon 退出时会把安装器一起结束。

### Post-install verification

`updates\pending.json` 记录 `{fromVersion, toVersion, startedAt, logPath}`。daemon 每次启动时检查该文件：

- 当前版本等于 `toVersion`：标记成功，删除 `updates\<toVersion>\`，管理页显示一次“已更新到 x.y.z”。
- 当前版本仍为 `fromVersion`：说明安装器失败并恢复了旧 daemon。状态置为 `error`，错误码 `update_install_failed`，并展示 `install.log` 路径；保留已下载的安装包，供重试使用。
- `startedAt` 超过 7 天：视为陈旧记录，直接删除。

## Interfaces

### Local control methods

以下方法只属于本机管理 contract，和 `system.autostart.set` 一样：Mobile contract 调用一律返回错误，也不加入 Relay scope 白名单。

| 方法 | 参数 | 结果/说明 |
| --- | --- | --- |
| `system.update.status` | 无 | 返回 `UpdateStatus` |
| `system.update.check` | 无 | 立即检查；返回 `UpdateStatus` |
| `system.update.apply` | `version`，可选 `confirmInterrupt` | 下载（如尚未下载）、校验、preflight 后启动安装器；成功表示安装器已启动，daemon 随后退出 |
| `system.update.cancel` | 无 | 取消进行中的下载；对已启动的安装器无效 |
| `system.update.skip` | `version` | 记录跳过的版本；之后只有更新的版本才会再次提示 |
| `system.update.settings` | 可选 `autoCheck`、`mirrorBase` | 修改并返回更新设置 |

`UpdateStatus`：

```json
{
  "state": "idle | checking | upToDate | available | downloading | downloaded | applying | error",
  "currentVersion": "0.2.0",
  "latest": { "version": "0.3.0", "notesUrl": "https://...", "size": 9123456 },
  "progress": { "received": 1048576, "total": 9123456 },
  "lastCheckedAt": "2026-11-10T08:00:00Z",
  "error": { "code": "update_install_failed", "message": "...", "logPath": "C:\\...\\install.log" },
  "installKind": "installed | portable",
  "canApply": true,
  "autoCheck": true,
  "skippedVersion": "",
  "mirrorBase": "",
  "updatedFrom": "0.1.0"
}
```

- `canApply` 只在当前版本能按 SemVer 解析且 `installKind=installed` 时为 true；管理页和 CLI 据此决定是否提供“立即更新”。
- `updatedFrom` 只在 `pending.json` 判定升级成功后的本次 daemon 运行中出现。
- `error.logPath` 只在 `update_install_failed` 时给出。
- `/api/status` 的 `update` 字段也返回同一个 `UpdateStatus`。

状态变化时通过 `m.publish` 广播 `system.update.changed` 事件（payload 为 `UpdateStatus`），下载进度最多每 500 ms 发送一次。`remote.forwardEventStream` 丢弃所有 `system.update.*` 事件，它们不会经 Relay 转发到手机。

### Admin page and tray

- 管理页顶部横幅：在 `available` 状态显示新版本号、“查看更新说明”（用系统浏览器打开 `notesUrl`）、“立即更新”和“跳过此版本”；下载中显示进度和“取消”。
- 用户点击“立即更新”后，如果遇到 `update_active_turns`，弹出与托盘退出相同语义的确认：确认后用 `confirmInterrupt=true` 重试；遇到 `update_blocked_by_bridge`，提示用户“完全退出 ChatGPT Desktop 后重试”。
- “启动设置”区有“自动检查更新”开关和可选的“更新镜像地址”。
- 托盘菜单有“检查更新”。检查结果为 `available` 时，托盘气泡通知只在每个版本上提示一次；点击通知打开管理页。

### CLI

```text
work-anywhere.exe update status
work-anywhere.exe update check
work-anywhere.exe update apply [--yes] [--interrupt]
work-anywhere.exe update cancel
```

CLI 通过管理 Pipe 调用上述控制方法，不自行下载。`apply` 不带 `--yes` 时，会先打印版本号和大小，等待交互确认；遇到 `update_active_turns` 时提示加 `--interrupt` 重试。

### Installer changes

[`installer.iss`](../../packaging/windows/installer.iss) 的更新模式规则如下，具体规则归属 [Windows 安装包设计](../windows-installer/design.md)：

- 识别 `/WAWUPDATE=1`。安装失败或被中止时，在 `DeinitializeSetup` 中执行 `{app}\work-anywhere.exe daemon start --background` 恢复旧 daemon。
- `[Run]` 中的 postinstall 项保持不带 `skipifsilent`，使静默升级后自动启动新 daemon。
- 静默安装时的 `codex` 任务会再次执行 `install`。`install` 在 `CODEX_CLI_PATH` 已指向本 EXE 时是幂等的，不会用本 EXE 路径覆盖安装前的原值备份。

## State and persistence

- 更新偏好保存在 [`config.json`](../persistence-and-security/design.md) 的受保护设置中，键为 `update.preferences`：`{autoCheck, mirrorBase, skippedVersion, notifiedVersion}`，其中 `autoCheck` 默认为 true。
- `lastCheckedAt`、最新清单和下载进度只保存在内存；daemon 重启后重新检查。
- 下载产物和 `pending.json` 放在 `%LOCALAPPDATA%\WorkAnywhere\updates\`，只包含公开发布的文件和版本元数据，不加密。目录内只保留当前目标版本，旧版本目录在下一次检查或成功更新后删除。
- 下载使用 `.part` 临时文件，支持 HTTP Range 续传；续传前要确认已有部分不超过清单 `size`。

## Security and failure handling

- 只信任通过签名校验的清单（见 [清单协议](manifest.md#client-verification)），镜像和下载主机都不被信任。
- 安装前立即重新计算一次 SHA-256，缩短“校验完成”到“执行”之间的窗口。下载文件的本地文件名固定为 `work-anywhere-setup.exe`，不使用清单中的 `name`，避免路径注入。
- 安装包没有 Authenticode 签名。Go HTTP 下载的文件不带 Mark-of-the-Web，SmartScreen 不会拦截，但杀毒软件仍可能拦截；此类失败通过 `install.log` 和 `update_install_failed` 暴露给用户。
- 安装器以当前用户权限运行（`PrivilegesRequired=lowest`），更新不申请管理员权限。
- 自动检查失败（网络、404、验签失败）只记录 `error` 状态和日志，不弹窗；手动检查失败会在页面显示错误。
- 遇到活跃 Turn 时默认拒绝（`update_active_turns`）；用户确认后，与托盘退出相同：先发送 interrupt，最多等待 10 秒，然后启动安装器并退出。
- Bridge 占用检测：启动安装器前检查各 Codex 实例快照的 `DesktopConnections`；只要有 ChatGPT Desktop 拉起的 Bridge 连接着 daemon，就返回 `update_blocked_by_bridge`。这些 Bridge 进程运行的就是安装目录 EXE，而静默安装时文件占用不会弹出重试框，只会直接失败。没有连接 daemon 的残留进程不在此检测范围内，由安装器的 15 秒可写等待兜底并以 `update_install_failed` 报告。
- 安装器失败或中止时，由 `/WAWUPDATE=1` 的恢复逻辑重新启动旧 daemon，下次启动时由 `pending.json` 报告失败。
- 日志只记录版本号、状态、错误码、字节数和耗时，不记录镜像地址以外的请求头或响应体。

稳定错误码：`update_manifest_unavailable`、`update_manifest_invalid`、`update_signature_invalid`、`update_hash_mismatch`、`update_download_failed`、`update_no_asset`、`update_unsupported_install`、`update_active_turns`、`update_blocked_by_bridge`、`update_in_progress`、`update_install_failed`。

## Invariants and non-goals

- 没有用户的明确确认，daemon 不下载安装包也不运行安装器；“跳过此版本”只影响提示，不影响手动检查。
- 版本只会前进，客户端不接受版本相同或更低的清单。
- 升级保留自启动和 Codex 集成选择，不修改 `%LOCALAPPDATA%\WorkAnywhere` 中的会话、配置和日志。
- 尚未支持：便携版（`installKind=portable`）自动替换，此类副本只提示新版本并提供下载页链接；被占用 EXE 的改名替换；prerelease/beta 通道；增量更新；强制更新或最低版本门槛；后台无人值守安装；由 Mobile 远程触发 Desktop 升级。

## Code ownership

- 清单解析、验签、SemVer 和内置公钥：[`internal/update/manifest/`](../../internal/update/manifest/)
- 状态机、调度与偏好：[`internal/update/service.go`](../../internal/update/service.go)、[`status.go`](../../internal/update/status.go)
- 下载与续传：[`internal/update/download.go`](../../internal/update/download.go)
- 安装形态、`/TASKS` 和安装器启动：[`internal/update/platform.go`](../../internal/update/platform.go)
- `pending.json`：[`internal/update/pending.go`](../../internal/update/pending.go)
- 控制方法、preflight 与退出：[`internal/daemon/update.go`](../../internal/daemon/update.go)；`/api/status` 在 [`admin.go`](../../internal/daemon/admin.go)
- 事件过滤：[`internal/remote/service.go`](../../internal/remote/service.go) 的 `forwardEventStream`
- 管理页：[`internal/daemon/web/`](../../internal/daemon/web/)
- 托盘菜单与气泡：[`internal/platform/tray/`](../../internal/platform/tray/)
- CLI：[`internal/cli/update.go`](../../internal/cli/update.go)
- 清单生成工具：[`internal/tools/updatemanifest/`](../../internal/tools/updatemanifest/)
- 安装器：`desktop/packaging/windows/installer.iss`
- Release：`.github/workflows/release.yml`

## Verification

单元测试：

- 用 [清单协议](manifest.md#test-vectors) 的 `vectors.json` 覆盖验签、篡改、未知 `keyId`、非法字段和版本比较（含无法解析的当前版本）。
- 下载：size/hash 不匹配、Range 续传、超大响应、HTTP 重定向到 HTTP 被拒绝、取消。
- `/TASKS` 生成：自启动和 Codex 集成四种组合。
- `pending.json` 的成功、失败和陈旧三种判定。
- 控制方法的 Mobile contract 拒绝，以及 Relay scope 白名单中不包含这些方法。

按仓库规则执行：

```powershell
cd desktop
.\.tools\go-sdk\go\bin\go.exe test ./...
.\.tools\go-sdk\go\bin\go.exe vet ./...
.\scripts\build.ps1
.\dist\windows-amd64\work-anywhere.exe self-test
```

人工 E2E（需要两个版本的安装包和一个用测试密钥签名的本地清单）：

1. 安装旧版本（勾选自启动、不勾选 Codex 集成）→ 管理页提示新版本 → 立即更新 → 新 daemon 自动启动，`version` 为新版本，Run 键仍存在，`CODEX_CLI_PATH` 未被写入。
2. 勾选 Codex 集成后升级：`CODEX_CLI_PATH` 仍指向安装目录，备份文件中保存的仍是安装前的原值。
3. ChatGPT Desktop 运行中（Bridge 占用 EXE）：返回 `update_blocked_by_bridge`，并且没有启动安装器；退出 ChatGPT Desktop 后重试成功。
4. 有活跃 Turn 时：先要求确认，确认后 Turn 被中断并完成升级。
5. 篡改清单或安装包：分别得到 `update_signature_invalid` 和 `update_hash_mismatch`，安装器未运行。
6. 模拟安装失败（例如手动锁住 EXE 后绕过 preflight）：旧 daemon 被恢复，管理页显示 `update_install_failed` 和日志路径。

## Documentation update triggers

修改检查周期、版本比较、控制方法/事件/错误码、`UpdateStatus` 字段、下载目录、安装器参数、`/TASKS` 生成规则、preflight 条件、`pending.json` 格式、管理页或托盘入口，或者支持范围时，必须更新本文档、[清单协议](manifest.md)、[Windows 安装包](../windows-installer/design.md)、[管理页与生命周期](../administration-and-lifecycle/design.md)、[本机控制](../local-control/design.md)，以及 Desktop README 和仓库中英文 README 中的升级说明。
