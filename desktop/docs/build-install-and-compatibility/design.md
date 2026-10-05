# Build, installation, and Codex compatibility

## Goal and scope

该功能生成可复现的 Windows x64 单文件发布产物，将其安全配置为 ChatGPT Desktop 的 `CODEX_CLI_PATH`，并尽量在官方 Codex App Server 增加未知字段和方法时保持透明兼容。

## Current architecture and data flow

Desktop 是独立 Go module：`github.com/hurf/work-anywhere/desktop`。仓库根 `go.work` 引用该 module。构建脚本固定使用 `desktop/.tools/go-sdk/go` 中的 Go 1.26.5，设置 `CGO_ENABLED=0`、`GOOS=windows`、`GOARCH=amd64`，运行测试、生成 Windows version/manifest resource，再输出：

```text
desktop/dist/windows-amd64/work-anywhere.exe
```

一个 EXE 同时包含 Bridge、daemon 和 CLI。版本、commit 和 UTC build date 通过 linker flags 写入；PE resource 保存 file/product version。

Windows 分发安装包由 `scripts/build-installer.ps1` 调用 Inno Setup 6 生成。安装包采用当前用户模式，默认目录为 `%LOCALAPPDATA%\Programs\WorkAnywhere`，不申请管理员权限；安装器在文件复制完成后调用同一个 EXE 的 `install` 命令完成 `CODEX_CLI_PATH` 写入，卸载时调用 `uninstall` 恢复原值。

## Installation interfaces

```powershell
work-anywhere.exe self-test
work-anywhere.exe install
work-anywhere.exe uninstall
```

`install` 备份当前用户级 `CODEX_CLI_PATH`，把它指向当前 EXE，并广播 Windows environment change。`uninstall` 恢复备份值。正在运行的 ChatGPT Desktop 不会自动继承新环境，安装/卸载后需要重启相关 Desktop 实例。

安装包构建接口为：

```powershell
scripts/build-installer.ps1 -Version 0.1.0
scripts/build-installer.ps1 -Version 0.1.0 -SkipBuild
```

该接口要求 Windows 环境安装 Inno Setup 6；已安装到非默认目录时通过 `-IsccPath` 指定 `ISCC.exe`。

显式 runtime 覆盖使用 `WORK_ANYWHERE_CODEX_EXE`；`CODEX_REAL_EXE` 仅用于旧 Bridge 兼容。Bridge 启动真实 Codex 时覆盖子进程 `CODEX_CLI_PATH`，避免递归。

## Compatibility strategy

- 正式传输使用官方默认 stdio JSONL；实验性 WebSocket 只用于 `--standalone-ws` 诊断。
- JSON-RPC ID 使用 `json.RawMessage`，支持字符串和数字。
- 未知字段由 raw payload 保留；未知 request/notification 不因 schema 未识别而关闭代理。
- 只对 initialize、ID 路由、Turn、action 和 catalog 等需要控制的消息做最小解析。
- 官方 schema fixture 同时保留 `testdata/app-server-schema/0.151.0-alpha.7.2/` 和当前验证的 `testdata/app-server-schema/0.159.2/`，用于协议回归参考，不在运行时强制校验每条消息。

Mobile 的接口可用性按会话和进程代次判断，不以 schema 中的方法存在或版本号代替真实调用。原生与兼容历史共享归一化器，空会话、队列降级和恢复语义以 [本机控制设计](../local-control/design.md) 为准。对应实现和回归测试为 [`mobile_history.go`](../../internal/daemon/mobile_history.go)、[`mobile_history_test.go`](../../internal/daemon/mobile_history_test.go)。

真实 Codex 验证由 [`codex_mobile_integration_test.go`](../../internal/daemon/codex_mobile_integration_test.go) 提供 opt-in 测试：设置 `WORK_ANYWHERE_TEST_CODEX` 为官方 exe，`WORK_ANYWHERE_TEST_AUTH` 为授权使用的 auth.json 路径，再执行：

```powershell
.\.tools\go-sdk\go\bin\go.exe test ./internal/daemon -run '^TestCodexMobileLifecycleIntegration$' -v -timeout 240s
```

测试将鉴权材料复制到测试临时 CODEX_HOME，不打印凭据；创建临时项目，执行两次短文本模型请求，验证空快照、首次/后续发送、流式 delta、控制订阅重连和隔离 App Server 重启。测试结束清理临时配置与会话，不替换用户当前 daemon。默认单元测试跳过真实模型请求。

## State and persistence

安装备份属于用户级设置；发布产物和构建工具不写消息数据库。runtime 版本/SHA256 只在 daemon 进程内用于检测 Desktop 升级并安全切换。

## Security and failure handling

- install/uninstall 只修改当前用户环境，不修改系统级变量或其他用户配置。
- self-test 在安装前确认 runtime 可发现、路径不递归、实例身份可计算，并在 daemon 启动时检查 DPAPI。
- daemon 启动失败时 Bridge 可在 pre-request 阶段 fallback，避免官方 Desktop 完全不可用。
- 官方移除 `CODEX_CLI_PATH` 或不兼容地改变 App Server transport 时，需要替换接入层；控制模型和持久化不应与该入口耦合。

## Invariants and non-goals

- V1 发布目标只有 Windows x64。
- 构建不依赖 CGO 或系统已安装 Go；必须使用固定工具链。
- schema fixture 是测试资料，不代表未来官方版本白名单。
- 无法保证所有未来官方版本零修改兼容；兼容目标是未知内容透明转发和故障时 Desktop 可用。
- 旧 C# Bridge 位于 `desktop/codex-stdio-ws-bridge/`，只作为一个回滚版本，不由 Go 构建脚本编译。

## Code ownership

- `scripts/build.ps1`
- `packaging/`
- `cmd/work-anywhere/`
- `internal/platform/installation/`
- `internal/cli/selftest.go`
- `internal/codex/runtime.go`, `invocation.go`
- `testdata/app-server-schema/`

## Verification

- 从 `desktop/` 执行 `scripts/build.ps1`，确认测试通过并生成 Windows x64 EXE/SHA256。
- `work-anywhere.exe version` 与 PE file/product version 一致。
- `self-test` 使用当前安装的官方 Codex 成功。
- 至少使用旧 fixture 与当前 0.159.2 runtime 验证 initialize、未知字段/通知、慢连接隔离、请求清理和 fallback。
- install/uninstall 测试备份、恢复及多 Desktop 实例重启后的路径。
- 在 Inno Setup 6 环境构建安装包，验证 per-user 安装目录、安装后 `CODEX_CLI_PATH`、完成页重启提示和卸载恢复。

## Documentation update triggers

修改 Go 版本、module 路径、产物路径、linker/平台 metadata、安装注册方式、环境变量、支持平台、schema fixture、transport 或兼容策略时，必须更新本文档以及平台支持、模块 README 和受影响的 runtime/Bridge 文档。
