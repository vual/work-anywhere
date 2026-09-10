# Local control API, CLI, and Provider boundary

## Goal and scope

本机控制面为 Relay/Mobile 提供不绑定 Codex 私有 JSON-RPC 细节的稳定入口。V1 只监听本机，并提供 CLI 和诊断管理页；Remote connector 复用相同 manager 语义后主动出站，不把本机 listener 暴露到公网。

## Current architecture and data flow

控制客户端先通过 WAI1 管理 Pipe 或管理页授权流程接入 daemon。WebSocket 消息统一为：

```json
{"type":"request","id":"...","method":"turn.start","params":{}}
{"type":"response","id":"...","result":{}}
{"type":"event","sequence":123,"event":"turn.started","payload":{}}
```

Mobile 请求使用独立 contract discriminator：`{"contract":"mobile-control-v1"}`。响应回显相同 contract；未知非空值返回 `unsupported_contract`，未带 contract 的本机 CLI/管理页保持既有 raw control 语义。

daemon 将控制方法路由到实例 supervisor；Codex driver/能力描述用于隔离未来不同 Provider 的功能差异。事件订阅使用有界队列，慢订阅者会被断开，客户端应使用 conversation cursor 补齐状态。

## Interfaces

当前固定控制方法：

- 系统：`system.status`, `system.ui_url`, `system.control.pause`, `system.control.resume`, `system.shutdown`
- 实例：`instance.list`, `instance.get`, `instance.restart`
- Mobile navigation：`agent.list`, `project.list`（要求 `mobile-control-v1`）
- 会话：`conversation.list`, `conversation.read`, `conversation.timeline.list`, `conversation.content.read`, `conversation.subscribe`
- Turn：`turn.start`, `turn.interrupt`
- action：`action.list`, `action.resolve`
- catalog：`catalog.models`, `catalog.permissionProfiles`
- 附件：`attachment.begin`, `attachment.chunk`, `attachment.commit`
- 流量：`traffic.enable`, `traffic.disable`
- Remote：`remote.status`, `remote.configure`, `remote.enroll`, `remote.reconnect`, `remote.pair.start/list/resolve/cancel/revoke`

`turn.start` 当前接受 `instanceId`、`threadId`、文字、attachment IDs、model、reasoning effort、approval policy、sandbox policy、client message ID 和 expected revision。附件中的图片被转换为官方 App Server 支持的输入项；调用必须指向已有 thread。

`mobile-control-v1` 接受 `agentId` 作为 `instanceId` 的产品化别名，并把 `query` 转为 App Server `searchTerm`。`agent.list` 返回 Codex Agent summary；`project.list` 与 `conversation.list` 返回 `{items,nextCursor}` 和稳定 summary。项目过滤的正常路径使用 App Server `thread/list(projectId)`；若结果为空，Desktop 会根据同一 Project ID 解析根目录并以 `thread/list(cwd)` 回退，以兼容项目迁移前仍只保存旧项目关联或 session cwd 的历史线程。`conversation.read` 使用 `thread/read(includeTurns=false)` 读取 metadata，再用 `thread/items/list(sortDirection=desc)` 返回最近 30 项（最大 50）的稳定 `ConversationSnapshot`；归一化器解包官方 `ThreadItemEntry` 的 `{item,turnId}` 结构，并兼容旧的扁平 fixture。`conversation.timeline.list` 原样续传 App Server opaque cursor 读取更早页。单 block 超过 64 KiB 时仅内联 UTF-8 前 16 KiB，并返回 15 分钟无访问过期、绑定 agent/thread/item/block 的随机 `contentRef`；`conversation.content.read` 每次最多返回 128 KiB。引用只保存 provider 页定位，不缓存正文。

Mobile control 响应序列化后上限为 512 KiB。时间线超预算时以同一 cursor 按 `30→15→7→3→1` 缩小 provider page；limit 为 1 仍超预算则进一步引用大 block。订阅事件只作为失效提示发送，不携带 provider 正文；待追赶事件超过有界窗口时返回 `snapshot=true`，由 Mobile 重新读取分页 snapshot。超限保护返回 `response_too_large`，不会把超大业务响应交给 Relay。Mobile control 结构化日志只记录 method、耗时、响应字节数和稳定错误码。

CLI 对应提供 daemon、instance、conversation、turn、action、catalog、traffic、通用 `call`、`ui`、`self-test`、install/uninstall 和 version 命令。CLI 通过本机受保护管理 Pipe 调用，不自行启动第二个 App Server。

Provider capabilities 当前包含 history、live stream、text/image input、approval、question、interrupt、steer、model/reasoning selection、permission mode 和 desktop mirror。Codex 是唯一 driver；部分能力标志表示协议边界，不代表所有 provider 已实现。

## State and persistence

控制连接和 subscriber 队列在内存中。客户端通过 conversation sequence/cursor 恢复事件；附件先分块收集，commit 后加密保存并获得 attachment ID。控制暂停只阻止本机控制操作，不改变 Desktop 原始连接。

## Security and failure handling

- 控制服务仅绑定随机 `127.0.0.1` 端口；管理 Pipe 受当前 SID ACL 保护。
- Web 管理入口使用一次性 fragment token 换取 SameSite/HttpOnly session，并校验 Origin 和 CSRF。
- `expectedRevision` 防止基于陈旧状态开始 Turn。
- 控制面暂停、实例未 ready、会话正在运行或等待 action 时拒绝不安全操作。

## Invariants and non-goals

- 本机控制协议使用版本化 envelope，业务错误使用稳定 code/message，而不是泄露内部 panic。
- Relay 出站连接器通过 scope allowlist 复用本控制语义，不绕过 daemon 直接操作 App Server stdio；当前实现见 [`remote-relay-connector`](../remote-relay-connector/design.md)。
- 管理页实现 daemon/Relay/配对管理，不实现聊天、Timeline 或 Prompt UI。
- 本机 control 不直接实现网络身份；网络配对和 Mobile 身份由 outbound Relay connector 负责。push provider 与 action/catalog 的完整稳定 DTO 尚未完成。

## Code ownership

- `internal/daemon/daemon.go`（控制路由）
- `internal/daemon/mobile_contract.go`（Mobile DTO、时间线分页和大内容引用）
- `internal/daemon/admin.go`（HTTP/WebSocket 与授权）
- `internal/cli/cli.go`
- `internal/provider/`
- `internal/protocol/types.go`

## Verification

- CLI 查看实例/会话、启动和中断 Turn、列出/解决 action、读取 catalog。
- 附件 begin/chunk/commit 后可用于图片输入。
- cursor 重连补发和 snapshot fallback 测试。
- metadata/timeline DTO、opaque cursor、自适应页大小、UTF-8 内容分块和引用越权/过期测试。
- Origin、CSRF、一次性 token、控制暂停和慢订阅者测试。

## Documentation update triggers

增加/删除/重命名控制方法、字段、事件、错误码、Provider capability、附件协议、认证方式或 CLI 命令时，必须更新本文档以及消费该接口的模块文档。
