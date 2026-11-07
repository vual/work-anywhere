# Build, installation, and Codex compatibility

## Goal and scope

该功能生成可复现的 Windows x64 单文件发布产物，将其安全配置为 ChatGPT Desktop 的 `CODEX_CLI_PATH`，并尽量在官方 Codex App Server 增加未知字段和方法时保持透明兼容。

## Current architecture and data flow

Desktop 是独立 Go module：`github.com/hurf/work-anywhere/desktop`。仓库根 `go.work` 引用该 module。构建脚本固定使用 `desktop/.tools/go-sdk/go` 中的 Go 1.27.3（该目录不存在时改用 `PATH` 上的 `go.exe`，供 CI 中的 `actions/setup-go` 使用；版本仍必须是 go1.27.3 windows/amd64），设置 `CGO_ENABLED=0`、`GOOS=windows`、`GOARCH=amd64`，运行测试、生成 Windows version/manifest resource，再输出：

```text
desktop/dist/windows-amd64/work-anywhere.exe
```

`scripts/build.ps1 -OutputDirectory <绝对目录>` 可把同样的测试、资源和构建流程输出到候选目录，默认输出位置不变。正在使用的 EXE 不覆盖；候选自检及隔离测试通过后，在所有 Turn/action 空闲且旧 daemon 正常停止后替换，保留原 EXE 供回滚。

一个 EXE 同时包含 Bridge、daemon 和 CLI。版本、commit 和 UTC build date 通过 linker flags 写入。`build.ps1` 生成 `winres/winres.json` 并调用 `go-winres make`，写入 icon、version 与 manifest resource：version resource 的 `FileDescription` 为 `work-anywhere.exe`（任务管理器中 Bridge 与 daemon 都显示该名称，不含 Codex 字样），`ProductName` 为 `Work Anywhere`；manifest 来自 [`packaging/windows/work-anywhere.manifest`](../../packaging/windows/work-anywhere.manifest)，声明 `asInvoker`、supportedOS 和 `consoleAllocationPolicy=detached`。Windows 11 24H2 / Server 2025 起，资源管理器、开始菜单和登录自启动不会为 EXE 分配控制台，终端中运行时仍继承控制台、ChatGPT Desktop 管道启动的 Bridge stdio 不受影响；旧系统忽略该元素，自启动时会短暂闪现控制台窗口。

Windows 分发安装包由 `scripts/build-installer.ps1` 调用 Inno Setup 6 生成。安装包采用当前用户模式，默认目录为 `%LOCALAPPDATA%\Programs\WorkAnywhere`，不申请管理员权限；安装器在文件复制完成后按所选任务调用同一个 EXE 的 `autostart enable` 与可选的 `install`，卸载时依次调用 `daemon stop`、`autostart disable`、`uninstall`，详见 [Windows installer](../windows-installer/design.md)。

## Installation interfaces

```powershell
work-anywhere.exe self-test
work-anywhere.exe self-test --protocol
work-anywhere.exe install
work-anywhere.exe uninstall
```

`install` 备份当前用户级 `CODEX_CLI_PATH`，把它指向当前 EXE，并广播 Windows environment change。`uninstall` 仅在该变量仍指向当前 EXE 时恢复备份值（无备份则删除），否则保持不变。只使用 Claude Code 时不需要 `install`。正在运行的 ChatGPT Desktop 不会自动继承新环境，安装/卸载后需要重启相关 Desktop 实例。

安装包构建接口为：

```powershell
scripts/build-installer.ps1 -Version 0.1.0
scripts/build-installer.ps1 -Version 0.1.0 -SkipBuild
```

该接口要求 Windows 环境安装 Inno Setup 6；已安装到非默认目录时通过 `-IsccPath` 指定 `ISCC.exe`。

GitHub Release 发布后，[Release workflow](../windows-installer/design.md#github-release-automation) 会在 `windows-latest` 上调用同一接口并上传产物。

显式 runtime 覆盖使用 `WORK_ANYWHERE_CODEX_EXE`；`CODEX_REAL_EXE` 仅用于旧 Bridge 兼容。Bridge 启动真实 Codex 时覆盖子进程 `CODEX_CLI_PATH`，避免递归。

## Compatibility strategy

- 正式传输使用官方默认 stdio JSONL；实验性 WebSocket 只用于 `--standalone-ws` 诊断。
- JSON-RPC ID 使用 `json.RawMessage`，支持字符串和数字。
- 未知字段由 raw payload 保留；未知 request/notification 不因 schema 未识别而关闭代理。
- 只对 initialize、ID 路由、Turn、action 和 catalog 等需要控制的消息做最小解析。
- 官方 schema fixture 保留 `0.151.0-alpha.7.2`、`0.159.2`，以及目标 runtime `0.165.0-alpha.1.2` 的 stable/experimental 两套导出。版本、哈希和开关见 [manifest](../../testdata/app-server-schema/0.165.0-alpha.1.2/manifest.json)。fixture 只用于回归，不在运行时强制校验消息。当前启动流程与协议适配边界见 [协议基线](protocol-baseline.md)。

`self-test` 只检查发现与实例身份，明确返回 `protocolVerified: false`。`self-test --protocol` 先通过正在执行的 WAW EXE 运行 `app-server --print-startup-config`（closed stdin，30 秒，stdout 最大 4 MiB，stderr 最大 64 KiB），校验完整 JSON 含 `appServerVersion` 与 `configRequirements.requirements`。该步骤使用当前账号/组织与网络配置，不自动 sign-out、不伪造策略；失败返回阶段诊断及非零退出码。通过后在临时 CODEX_HOME 启动官方 App Server，15 秒内完成 initialize、initialized、permissionProfile/list 只读查询。自检子树受临时 Job Object 约束，超时和结束时清理，不连接或替换用户 daemon，不发起模型请求。

协议比较使用 Python 3 标准库；必须比较相同导出模式，不能把 experimental 开关差异算作版本变化：

```powershell
python scripts/compare-schemas.py testdata/app-server-schema/0.159.2 testdata/app-server-schema/0.165.0-alpha.1.2/stable
python scripts/compare-schemas.py testdata/app-server-schema/0.151.0-alpha.7.2 testdata/app-server-schema/0.165.0-alpha.1.2/experimental
python scripts/test_compare_schemas.py
```

比较结果为 JSON，包含方法、文件、字段增删、required 及枚举变化；忽略描述文本与集合顺序。更新 fixture 时使用目标官方二进制重新执行 manifest 内两条导出命令，并记录实际 SHA256。

Mobile 的接口可用性按会话和进程代次判断，不以 schema 中的方法存在或版本号代替真实调用。原生与兼容历史共享归一化器，空会话、队列降级和恢复语义以 [本机控制设计](../local-control/design.md) 为准。对应实现和回归测试为 [`mobile_history.go`](../../internal/daemon/mobile_history.go)、[`mobile_history_test.go`](../../internal/daemon/mobile_history_test.go)。

真实 Codex 验证由 [`codex_mobile_integration_test.go`](../../internal/daemon/codex_mobile_integration_test.go) 提供 opt-in 测试：设置 `WORK_ANYWHERE_TEST_CODEX` 为官方 exe，`WORK_ANYWHERE_TEST_AUTH` 为授权使用的 auth.json 路径，再执行：

```powershell
.\.tools\go-sdk\go\bin\go.exe test ./internal/daemon -run '^TestCodexMobileLifecycleIntegration$' -v -timeout 240s
```

测试将鉴权材料复制到测试临时 CODEX_HOME，不打印凭据；创建临时项目，通过 Desktop HandleDesktop 初始化入口完成握手，frontend 不发送 initialized，测试不得直接设置 ready；执行两次短文本模型请求，验证空快照、首次/后续发送、流式 delta、控制订阅重连和隔离 App Server 重启。测试结束清理临时配置与会话，不替换用户当前 daemon。默认单元测试跳过真实模型请求。

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

- 首条发送回归必须覆盖真实 `thread/start` 设置响应、手机 `turn.start.settings`、初始快照与用户选择不一致、失败保留和重启后的元数据空会话。真实集成测试不能省略 settings；成功 Turn 后同时验证官方完整历史和 `thread/resume`，GUI/真机验证单独记录。
- 本地候选 EXE 与 APK 都通过测试后再更新。活动 Turn 或待处理交互存在时保留候选产物，不强制终止；Android 覆盖安装保留 pairing。无 C 工具链的 Windows 环境不得将 race 标记为通过。

- 从 `desktop/` 执行 `scripts/build.ps1`，确认测试通过并生成 Windows x64 EXE/SHA256。
- `work-anywhere.exe version` 与 PE file/product version 一致。
- `self-test` 使用当前安装的官方 Codex 成功。
- 使用旧 fixture 与当前 0.165.0-alpha.1.2 fixture 验证 initialize 成功/失败、initialized 每代一次、未知字段/通知、基础设施请求分类、慢连接隔离、请求清理和 fallback；真实 runtime 用 `self-test --protocol` 验证。基于 fixture 的通过不表示真机 E2E 已通过。
- 执行 `go test -race ./internal/daemon ./internal/app ./internal/codex`（需支持 Go race 的 C 编译器），验证初始化/请求/退出并发；执行上述 Python 比较工具测试。
- Windows process 回归覆盖独立根进程、显式 ParentProcess 继承、后代清理、宿主异常退出以及 Add/Close 并发。Claude 的真实无模型初始化验证见 [provider 设计](../claude-code-provider/design.md)。
- 发布前完整重启 Desktop，验证策略检查后进入 Bridge，并验证手机历史、文字/图片、队列、流式、审批/问题和中断。替换 EXE 前确认无活跃 Turn，正常停止 daemon 并备份旧 EXE；不能强制中断活跃会话以完成安装。
- install/uninstall 测试备份、恢复及多 Desktop 实例重启后的路径。
- 在 Inno Setup 6 环境构建安装包，验证 per-user 安装目录、安装后 `CODEX_CLI_PATH`、完成页重启提示和卸载恢复。

## Documentation update triggers

修改 Go 版本、module 路径、产物路径、linker/平台 metadata、安装注册方式、环境变量、支持平台、schema fixture、transport 或兼容策略时，必须更新本文档以及平台支持、模块 README 和受影响的 runtime/Bridge 文档。
