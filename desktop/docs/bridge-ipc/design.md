# Desktop Bridge and Named Pipe IPC

## Goal and scope

Bridge 保持 ChatGPT Desktop 看到的接口仍是官方 App Server stdio JSONL，同时把原始消息转送给 daemon。它负责启动协调和安全 fallback，不负责解释业务事件或持久化会话。官方 Desktop 可能在 `app-server` 前追加 Codex 全局选项（例如 `-c key=value`）；Bridge 和 daemon 都按子命令位置识别并保留这些选项。

## Current architecture and data flow

```text
Desktop stdin ─► bounded JSONL reader ─► desktop_frame ─► daemon
Desktop stdout ◄──────────────────────── server_frame ◄─ daemon
```

daemon 向每个 Bridge 连接使用独立的有界发送队列（最多 128 帧或 8 MiB），由单独 writer 顺序写入。队列满、连接写入失败或写入持续超过 10 秒时，该连接会被关闭并从实例订阅者中移除；慢连接不能阻塞其他 Desktop 的响应和流式通知。

Bridge 先发现 runtime/instance，再连接当前用户管理 Pipe。连接失败时隐藏启动 `work-anywhere.exe daemon run` 并在最多五秒内重试。若在任何 Desktop frame 发出前仍失败，则持有实例级 lease 并直通 `codex.exe app-server`；发出过 frame 后连接中断则直接退出，不热切换。

`app-server --standalone-ws` 是诊断兼容模式：Bridge 在随机 loopback 端口启动官方实验性 WebSocket App Server，并在 stdio JSONL 与 WebSocket text frame 之间转发。daemon 正式链路不使用该模式。普通桌面启动即使带有 `-c ... app-server ...` 形式的全局选项，也会进入同一 stdio Bridge 路径。

## Interfaces

WAI1 帧头固定为 16 字节：

```text
magic[4] = "WAI1"
version uint16 (big endian)
kind uint8
flags uint8
metadataLength uint32 (big endian)
payloadLength uint32 (big endian)
```

其后依次是 metadata JSON 和原始 payload。当前帧类型包括：

- `hello`, `hello_ack`
- `desktop_frame`, `server_frame`
- `desktop_eof`, `server_exit`
- `error`, `ping`, `pong`
- `control_request`, `control_response`

单帧 metadata 与 payload 合计最大 64 MiB。JSONL 输入使用有界读取，超过限制时拒绝，避免无界内存增长。Named Pipe 名称包含当前用户 SID，并使用当前 SID ACL。

## State and persistence

Bridge 只保存本连接需要的 runtime、instance、连接 ID 和“是否已发出 frame”状态，不写业务数据库。完整环境通过 `hello` 内存传输，不落盘。

## Security and failure handling

- 子进程继承 Desktop 本次环境，但 `CODEX_CLI_PATH` 被改成真实 Codex 路径。
- fallback 的实例级互斥锁防止 daemon 同时为同一实例创建第二个 App Server。
- stdio EOF 转换为 `desktop_eof`；daemon 的 `server_exit` 或 Pipe 错误导致 Bridge 结束。
- 未完成请求期间禁止从 daemon 热切到 fallback，以免重复执行工具或写操作。
- 正常输出使用有界队列处理背压；同一连接中已接受的帧保持顺序，不改变 WAI1 帧格式。

## Invariants and non-goals

- payload 中的 JSONL 字节在 IPC 层保持原样；ID 改写发生在 daemon，不发生在 Bridge。
- Bridge 不保存会话、action 或附件。
- Bridge 不暴露公网监听端口。
- WebSocket 模式仅用于兼容诊断，不是可靠主链路。

## Code ownership

- `internal/bridge/`
- `internal/ipc/frame.go`, `line.go`
- `internal/platform/localipc/`
- `internal/platform/process/`
- `internal/protocol/types.go`
- `internal/codex/invocation.go`（识别全局选项后的 `app-server` 并规范化监听参数）

## Verification

- 帧 round-trip、错误 magic/version、超大帧和超长 JSONL 单元测试。
- daemon 不运行时验证按需启动；启动失败时验证 pre-request fallback。
- 真实 stdio 和 `--standalone-ws` initialize E2E。

## Documentation update triggers

修改帧格式、帧类型、大小限制、IPC 权限/命名、fallback 条件、daemon 启动等待或 standalone 诊断路径时，必须更新本文档，并同步检查平台支持、App Server 和 JSON-RPC 文档。
