# Codex runtime discovery and instance identity

## Goal and scope

该功能为每次 Desktop Bridge 启动定位对应的官方 `codex.exe`，并把不同 `CODEX_HOME`、Electron user-data 和显式实例名隔离成稳定逻辑实例。ChatGPT Desktop 升级后应切换 runtime，而不是产生新的逻辑实例。

## Current architecture and data flow

Bridge 启动时计算 `InstanceDescriptor` 和 `RuntimeDescriptor`，通过 WAI1 `hello` 帧注册给 daemon。daemon 用实例 ID 选择或创建 supervisor，并比较 runtime SHA256 决定立即切换或延迟升级。

实例 ID 为以下规范化值连接后的 SHA-256：

```text
Windows SID
canonical CODEX_HOME
canonical Electron user-data
optional WORK_ANYWHERE_INSTANCE_NAME
```

runtime 路径、版本、SHA256 和 resources 只描述本次运行环境，不参与实例 ID。

## Interfaces

真实 Codex 的发现顺序：

1. 当前父级 ChatGPT 可执行文件旁的 `resources/codex.exe`。
2. `WORK_ANYWHERE_CODEX_EXE`。
3. 兼容变量 `CODEX_REAL_EXE`。
4. Bridge 同目录的 `codex-real.exe`。
5. `%LOCALAPPDATA%\OpenAI\Codex\bin\<revision>\codex.exe` 和其兼容的用户级缓存副本，按文件修改时间优先尝试较新的版本。
6. `%LOCALAPPDATA%` 下已知 Portable/ChatGPT 布局的版本扫描。

每个候选不仅要存在，还必须能够执行 `codex.exe --version`。这一步用于跳过 Microsoft Store `WindowsApps` 中普通进程无法直接启动的受保护副本；因此 Store Desktop 可以使用其父进程路径完成发现，但 daemon 会自动采用同版本的用户级可执行副本。

Electron user-data 的发现顺序：

1. `WORK_ANYWHERE_USER_DATA_DIR`。
2. `CODEX_ELECTRON_USER_DATA_PATH`。
3. `ELECTRON_USER_DATA_DIR`。
4. 父 ChatGPT 命令行的 `--user-data-dir`。
5. 默认 `%LOCALAPPDATA%\OpenAI\Codex`，缺失时回退到 `CODEX_HOME\electron`。

Bridge 将真实路径、版本、SHA256、resources、发现来源、参数、cwd 和原始环境交给 daemon。环境仅通过受保护 Pipe 在内存中传输；启动子 Codex 时统一把 `CODEX_CLI_PATH` 替换为真实 Codex；存在 `CODEX_APP_RUNTIME_CLI_PATH` 时也替换为同一路径，防止递归调用 Bridge。其余环境原样继承，不补造 Windows Core/sandbox 包身份。一次性命令和受管进程共享发现顺序；不以缓存修改时间证明其与当前 Desktop 同版。

## State and persistence

当前 runtime、候选 runtime 和已见 SHA256 只在 daemon 进程内使用，不写入配置。完整进程环境不持久化，也不进入结构化日志。

## Security and failure handling

- 候选路径必须存在、是文件、不能等于当前 Bridge 自身，并且必须能执行 `--version`。
- runtime SHA256 用于升级比较和诊断，不是代码签名验证。
- 未找到真实 Codex 时命令失败，并提示设置 `WORK_ANYWHERE_CODEX_EXE`。
- Windows 父进程命令行读取仅用于实例发现；V1 的该实现按 Windows x64 进程布局工作。

## Invariants and non-goals

- 同一个 SID、Codex home、user-data 和实例名始终得到同一实例 ID。
- ChatGPT 安装目录或 Codex 版本变化不能改变实例 ID。
- 不自动合并两个不同 user-data 或 `CODEX_HOME` 的实例。
- 当前不校验 Microsoft Store 或 Portable 安装包签名。

## Code ownership

- `internal/codex/runtime.go`
- `internal/platform/processinfo/`
- `internal/platform/identity/`
- `internal/platform/paths/`
- `internal/protocol/types.go`

## Verification

- 单元测试覆盖实例 ID 稳定性和 app-server 参数识别。
- `self-test` 输出发现来源、真实路径、版本、SHA256 和实例描述。
- 多实例 E2E 应使用不同 `CODEX_HOME`/user-data，确认实例、日志和会话不串线。

## Documentation update triggers

修改候选优先级、环境变量、user-data 发现、实例 ID 输入、runtime 元数据或升级判定时，必须更新本文档，并检查平台支持、App Server 监督和构建兼容文档。
