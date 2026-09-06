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

daemon 将控制方法路由到实例 supervisor；Codex driver/能力描述用于隔离未来不同 Provider 的功能差异。事件订阅使用有界队列，慢订阅者会被断开，客户端应使用 conversation cursor 补齐状态。

## Interfaces

当前固定控制方法：

- 系统：`system.status`, `system.ui_url`, `system.control.pause`, `system.control.resume`, `system.shutdown`
- 实例：`instance.list`, `instance.get`, `instance.restart`
- 会话：`conversation.list`, `conversation.read`, `conversation.subscribe`
- Turn：`turn.start`, `turn.interrupt`
- action：`action.list`, `action.resolve`
- catalog：`catalog.models`, `catalog.permissionProfiles`
- 附件：`attachment.begin`, `attachment.chunk`, `attachment.commit`
- 流量：`traffic.enable`, `traffic.disable`
- Remote：`remote.status`, `remote.configure`, `remote.enroll`, `remote.reconnect`, `remote.pair.start/list/resolve/cancel/revoke`

`turn.start` 当前接受 `instanceId`、`threadId`、文字、attachment IDs、model、reasoning effort、approval policy、sandbox policy、client message ID 和 expected revision。附件中的图片被转换为官方 App Server 支持的输入项；调用必须指向已有 thread。

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
- V1 不实现网络配对、推送通知或移动端身份。

## Code ownership

- `internal/daemon/daemon.go`（控制路由）
- `internal/daemon/admin.go`（HTTP/WebSocket 与授权）
- `internal/cli/cli.go`
- `internal/provider/`
- `internal/protocol/types.go`

## Verification

- CLI 查看实例/会话、启动和中断 Turn、列出/解决 action、读取 catalog。
- 附件 begin/chunk/commit 后可用于图片输入。
- cursor 重连补发和 snapshot fallback 测试。
- Origin、CSRF、一次性 token、控制暂停和慢订阅者测试。

## Documentation update triggers

增加/删除/重命名控制方法、字段、事件、错误码、Provider capability、附件协议、认证方式或 CLI 命令时，必须更新本文档以及消费该接口的模块文档。
