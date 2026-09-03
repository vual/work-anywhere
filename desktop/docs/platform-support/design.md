# Desktop platform support

## Goal and scope

Desktop 使用一套共享 Bridge/daemon 业务代码，并通过 `internal/platform` 的小型适配包隔离操作系统能力。平台目录不是三套产品分支；任何协议、会话、Provider 或存储业务规则都应保持单一实现。

## Current support matrix

| 能力 | Windows x64 | macOS | Linux |
| --- | --- | --- | --- |
| 构建目标 | 支持 | 可编译骨架，未发布 | 可编译骨架，未发布 |
| 本机 IPC | 当前 SID ACL Named Pipe | 未实现 Unix Domain Socket | 未实现 Unix Domain Socket |
| 单实例/实例 lease | Named Mutex | 未实现 file lock | 未实现 file lock |
| 子进程清理 | Job Object | 未实现完整 process-group 清理 | 未实现完整 process-group/PDEATHSIG 清理 |
| 主密钥保护 | Current-user DPAPI | 未实现 Keychain | 未实现 Secret Service/keyring |
| 托盘 | `Shell_NotifyIcon` | 无操作占位 | 无操作占位 |
| URL 打开 | `rundll32` | `open` 适配已存在 | `xdg-open` 适配已存在 |
| ChatGPT 父进程发现 | Win32 process inspection | 未实现 | 未实现 |
| Bridge 安装 | 用户级 `CODEX_CLI_PATH` | 未实现 | 未实现 |
| 发布打包 | Windows x64 EXE | 未实现签名/公证 | 未实现包与桌面集成 |

只有 Windows x64 可以标记为可运行。macOS/Linux 当前能复用并编译大部分共享包，但 daemon 会在缺失的平台能力处返回明确错误，不能被宣传为受支持。

## Current architecture and data flow

```text
shared business packages
  bridge / daemon / codex / store / cli
                  │
                  ▼
internal/platform
  identity       user identity and leases
  localipc       local listener/dialer
  paths          data directories, endpoint names, canonical paths
  process        child group, launch flags, URL opener
  processinfo    parent ChatGPT discovery
  secrets        current-user key protection
  tray           native tray behind a Controller interface
  installation   platform bridge installation
```

`packaging/<platform>/` 只保存安装、签名、图标和发行配置，不承载运行时业务代码。Windows 构建生成的 `.syso` 必须位于 Go command package，因此它是由构建脚本产生的中间资源，不代表平台逻辑回流到共享层。

## Interfaces

- 共享层可以导入 `internal/platform/*` 的窄接口。
- 平台适配包不得导入 `internal/daemon`、`internal/bridge` 等业务包；托盘通过中立的 `Controller` 接口回调。
- 操作系统 API、build tags、系统命令和安全存储 API 只能出现在平台适配或 packaging/build 文件中。
- 能跨 Unix 共享的实现应使用 `*_unix.go`；确有差异时使用 `*_darwin.go` 和 `*_linux.go`，不能用一个含糊的 `*_other.go` 假装完整支持。

## State and persistence

平台适配层不拥有业务数据库。它只管理进程句柄、OS lock、IPC endpoint、系统密钥封装、托盘窗口和安装状态。平台返回的身份与 endpoint 由共享层用于实例和连接管理。

## Security and failure handling

- 新平台在安全密钥存储、本机 IPC 权限、进程清理和单实例锁全部实现前，daemon 必须 fail closed。
- 禁止为了“能启动”而把 DPAPI 替换成明文密钥、把受保护 IPC 替换成任意用户可连接的 TCP 端口，或忽略子进程清理。
- 平台命令失败必须返回可诊断错误；不能静默降级到更弱安全模型。
- packaging 中不得提交签名私钥、证书密码或生成的发布产物。

## Invariants and non-goals

- 一套共享业务逻辑，不建立 `windows/daemon`、`macos/daemon`、`linux/daemon` 三份副本。
- build tag 文件只解决 OS 接口差异，不复制 JSON-RPC、会话或控制面实现。
- 目录存在不等于平台受支持；支持状态只由本矩阵和对应 E2E 验收决定。
- 当前工作不实现 macOS/Linux 的完整运行能力。

## Code ownership

- 平台接口与实现：`internal/platform/`
- 平台发布配置：`packaging/`
- 共享业务：`internal/bridge`, `internal/daemon`, `internal/codex`, `internal/store`, `internal/cli`
- Windows 构建：`scripts/build.ps1`

## Verification

- Windows：`go test ./...`、`go vet ./...`、发布构建、`self-test` 和真实 App Server E2E。
- macOS/Linux 骨架：至少执行对应 `GOOS`/`GOARCH` 的 `go build ./...`，确保共享层没有意外导入 Windows API。
- 新增平台支持前，必须增加该平台的 identity、IPC、process cleanup、secrets、tray、process discovery、installation 单元/集成测试及真实 Desktop E2E。

## Documentation update triggers

新增平台、改变支持状态、移动平台 API、增加 OS 依赖、改变 packaging/签名/安装方式或完成矩阵中的任何能力时，必须更新本文档、Desktop README、构建兼容文档和受影响功能设计。
