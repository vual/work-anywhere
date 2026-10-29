# Desktop architecture

## Goal and scope

Desktop 模块让官方 ChatGPT Codex Desktop 继续作为桌面显示层，同时把官方 Codex App Server 的会话能力暴露给本机控制客户端和出站 Relay connector。当前实现面向 Windows x64；Codex 通过 App Server 投影提供完整会话能力，本机 Claude Code 通过 `provider.Backend` 提供浏览与交互能力。

## Current architecture and data flow

同一个 `work-anywhere.exe` 根据参数承担三种角色：

```text
ChatGPT Desktop
      │ stdio JSONL
      ▼
Bridge shim ── WAI1 Named Pipe ──► daemon ── stdio JSONL ──► official Codex App Server
                                      │
                                      ├─ local WebSocket control API
                                      ├─ bounded in-memory projections
                                      ├─ outbound E2EE Relay connector
                                      └─ tray and local admin page
```

- `app-server ...` 或 `bridge ...` 进入 Bridge 模式。
- `daemon run` 启动当前 Windows 用户唯一的 daemon。
- 已知控制命令进入 CLI；其他参数透明执行真实 `codex.exe`。
- daemon 是它托管的 App Server 的唯一真实 stdio 客户端。Desktop Bridge 和本机控制客户端是被多路复用的 frontend。
- daemon 启动时按配置注册非 Codex 的 `provider.Backend`（当前为 [Claude Code provider](../claude-code-provider/design.md)），它读取 Claude Code 的本机转录目录，并为发送的会话启动隐藏的 headless `claude.exe` 子进程（加入 daemon job object），不经过 Bridge。Mobile 请求按 `agentId` 路由到 Codex 实例或后端。

## Interfaces

- Desktop 与 Bridge：官方 stdio JSONL，不改变 Desktop 的调用模型。
- Bridge 与 daemon：当前用户 ACL 保护的版本化 WAI1 Named Pipe 帧。
- daemon 与 Codex：官方 App Server stdio JSONL。
- daemon 与控制客户端：本机 WebSocket 上的版本化 request/response/event JSON。
- daemon 与 Relay：出站 WSS 内的 Noise E2EE control，Relay 不接触业务明文。
- Provider 边界：`internal/provider` 定义共享 Mobile DTO、`Capabilities` 和 `Backend` 接口。Codex 仍由 daemon 的 App Server 投影直接产出这些 DTO；Claude Code 后端实现 `Backend`。

## State and persistence

daemon 内存中持有运行时进程、连接、ID 映射、初始化缓存、活跃 Turn、待处理 action 和有界事件回放。官方 Codex session 是 Codex 会话、Turn 和 Timeline 的唯一权威来源；Claude Code 会话的权威来源是 CLI 自身的转录目录。Desktop 不保存消息数据库；Relay、身份、配对和 thread 设置写入 `%USERPROFILE%\.work-anywhere\config.json`，运行状态在 daemon 重启后从 App Server 重新读取。

## Security and failure handling

- Named Pipe 只允许当前 SID；管理 HTTP/WebSocket 只监听随机 `127.0.0.1` 端口。
- 小型设置经 current-user DPAPI 保护后写入用户 JSON 配置；消息和事件正文不持久化。完整流量默认不记录。
- daemon 无法连接时，尚未向 daemon 发送请求的 Bridge 可直通真实 Codex，保证 Desktop 可用。
- daemon 中途断开后 Bridge 退出，不在未完成请求上热切换。

## Invariants and non-goals

- 每个 Windows 用户最多一个 daemon。
- 每个稳定逻辑实例最多一个由 daemon 托管的 App Server。
- runtime 可升级，但 runtime 路径和版本不参与逻辑实例 ID。
- Desktop 模块不实现自建桌面聊天 UI。
- Desktop 不实现 Mobile UI 和离线消息队列；Claude Code 后端不支持运行中排队和 steer，也不接管电脑端正在使用的会话。

## Code ownership

- 进程入口：`cmd/work-anywhere/main.go`, `internal/app/app.go`
- Provider 边界：`internal/provider/`
- 共享协议类型：`internal/protocol/`
- daemon 编排：`internal/daemon/`
- 操作系统适配：`internal/platform/`
- 安装与发行配置：`packaging/`

## Verification

- `go test ./...` 和 `go vet ./...`。
- `work-anywhere.exe self-test` 能发现非递归的官方 runtime 并计算实例身份。
- 真实 E2E 中 Desktop initialize 经 Bridge/daemon 到达官方 App Server。

## Documentation update triggers

修改模式分派、增加 Provider、改变任意跨进程边界、调整权威数据来源或扩展支持平台时，必须更新本文档、平台支持文档以及所有受影响功能文档。
