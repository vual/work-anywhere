# Desktop architecture

## Goal and scope

Desktop 模块让官方 ChatGPT Codex Desktop 继续作为唯一桌面显示层，同时把官方 Codex App Server 的会话能力暴露给本机控制客户端。V1 面向 Windows x64，不包含 Relay、手机端或其他 Provider 的具体实现。

## Current architecture and data flow

同一个 `work-anywhere.exe` 根据参数承担三种角色：

```text
ChatGPT Desktop
      │ stdio JSONL
      ▼
Bridge shim ── WAI1 Named Pipe ──► daemon ── stdio JSONL ──► official Codex App Server
                                      │
                                      ├─ local WebSocket control API
                                      ├─ SQLite encrypted projection
                                      └─ tray and local admin page
```

- `app-server ...` 或 `bridge ...` 进入 Bridge 模式。
- `daemon run` 启动当前 Windows 用户唯一的 daemon。
- 已知控制命令进入 CLI；其他参数透明执行真实 `codex.exe`。
- daemon 是它托管的 App Server 的唯一真实 stdio 客户端。Desktop Bridge 和本机控制客户端是被多路复用的 frontend。

## Interfaces

- Desktop 与 Bridge：官方 stdio JSONL，不改变 Desktop 的调用模型。
- Bridge 与 daemon：当前用户 ACL 保护的版本化 WAI1 Named Pipe 帧。
- daemon 与 Codex：官方 App Server stdio JSONL。
- daemon 与控制客户端：本机 WebSocket 上的版本化 request/response/event JSON。
- Provider 抽象：`Driver` 定义历史、输入、中断、审批和 catalog 能力；当前只有 Codex 实现。

## State and persistence

daemon 内存中持有运行时进程、连接、ID 映射、初始化缓存、活跃 Turn 和待处理 action。SQLite 保存实例、运行时历史、会话投影、Turn、Timeline、action、附件和设置。官方 Codex session 仍由官方 Codex 管理，本数据库不是其权威副本。

## Security and failure handling

- Named Pipe 只允许当前 SID；管理 HTTP/WebSocket 只监听随机 `127.0.0.1` 端口。
- 敏感规范化内容加密后入库；完整流量默认不记录。
- daemon 无法连接时，尚未向 daemon 发送请求的 Bridge 可直通真实 Codex，保证 Desktop 可用。
- daemon 中途断开后 Bridge 退出，不在未完成请求上热切换。

## Invariants and non-goals

- 每个 Windows 用户最多一个 daemon。
- 每个稳定逻辑实例最多一个由 daemon 托管的 App Server。
- runtime 可升级，但 runtime 路径和版本不参与逻辑实例 ID。
- Desktop 模块不实现自建桌面聊天 UI。
- V1 不实现 Relay、Mobile 和其他 Provider driver。

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
