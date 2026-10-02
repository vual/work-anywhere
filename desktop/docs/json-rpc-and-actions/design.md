# JSON-RPC multiplexing, conversations, and actions

## Goal and scope

该功能在一个真实 Codex App Server 连接上隔离多个 Desktop/control frontend 的请求 ID，维护会话运行投影，并让审批、问题等 server request 可以由任一已授权 frontend exactly-once 地解决。

## Current architecture and data flow

daemon 解析 JSON-RPC envelope，但对未知方法和字段保持透明。Desktop 请求 ID 改写为：

```text
wa:d:<desktop-connection>:<sequence>
```

控制请求 ID 使用：

```text
wa:c:<control-client>:<sequence>
```

映射表保留原始 `json.RawMessage` ID，因此字符串和数字 ID 都能原样恢复。App Server response 到达后按前缀路由给原 frontend 并恢复原始 ID；notification 广播给相关 frontend 和控制订阅者。

Turn 通知驱动会话状态：`idle`、`running`、`waiting_action`、`completed`、`failed`、`interrupted`。每个会话分别维护单调 `sequence`（全部会话事件）和 `revision`（业务状态变化）。已识别的 Turn 生命周期、审批、设置、队列、历史和停止状态更新递增 revision；MCP 启动进度、流式 delta、遥测和未知通知只递增 sequence，原始协议仍透明转发。控制客户端按 sequence cursor 读取增量，游标不可用时返回 snapshot。会话归属包含 `threadId`、`conversationId` 和嵌套 `thread.id`；审批解决前保留 action 的 thread 归属。

控制端写操作使用会话级 mutex；开始 Turn 在实例状态锁内同时验证 revision 和可操作状态并预留 pending。无设置的 Turn 同样有 pending 标记；成功确认转为 active，取消、写入失败、无有效 Turn ID、超时或进程停止后释放标记。停止清理 pending origin 并使受影响会话失效，重新初始化后重新判断读取后端。

## Initialize virtualization

- 第一个 Desktop `initialize` 发送给真实 App Server，缓存参数和响应；对应 `initialized` 正常转发。
- 后续 Desktop 重连的 `initialize` 使用缓存 response 本地回答，`initialized` 被 daemon 吸收。
- capability 参数不同且实例空闲时重启 App Server；运行中则保存最新参数，在安全边界后重启。
- 初始化兼容判断只比较 `params.capabilities`；缺失、`null` 和空对象视为相同，client name/version 变化不会触发重启。
- 多个 frontend 同时等待首次 initialize 时合并到同一次真实 initialize，并分别恢复各自 ID。

## Pending actions

App Server 发出的 server request 被规范化为 `PendingAction`，保留原 method、params 和 request ID，同时发往 Desktop frontend 和本机控制订阅者。Desktop 原始 response 或 `action.resolve` 的第一个有效结果通过锁内状态比较成为唯一结果：

- 首个结果写回 App Server，action 标记 resolved，并广播 `action.resolved`。
- 后续结果得到 `already_resolved`，不会再次写回 App Server。
- `serverRequest/resolved`、Turn 完成或中断会清除相关未完成 action。

原始 `PendingAction.params` 只用于 daemon 内部、Desktop 透明转发和加密持久化。`mobile-control-v1` 的 `action.list` 使用稳定投影：通用请求标记为 `approval`，`item/tool/requestUserInput` 标记为 `userInput` 并只暴露 questions/options 所需字段，不向 Mobile 透传其他 provider 参数。与 thread 关联的未完成 action 同时投影为 `ConversationSummary.needsAttention=true`。问题回答沿用官方 `answers[questionId].answers` 结构；命令审批继续接受 allow/deny 对应的 decision response。

## Interfaces

已识别的核心官方方法/通知包括 initialize、thread/turn 生命周期、`turn/start`、`turn/interrupt`、model catalog、permission profile 和 server request。未知请求和通知仍转发；未知 notification 只记录 method 名，不因 schema 不认识而断开连接。

## State and persistence

请求 ID 映射和 initialize waiter 只存在内存。会话状态、Turn、timeline event 和 pending action 投影到 SQLite；payload 使用加密列。官方 App Server/官方 session 仍是完整会话的权威来源。

控制请求取消、超时、stdin 写入失败或 App Server 退出时会清理内存中的 request mapping；内部调用收到终止错误，迟到 response 会被丢弃。

## Security and failure handling

- 控制客户端只有在实例 ready、会话 idle 且没有阻塞 action 时才能开始新 Turn。
- Desktop 原始请求保持透明，由官方 App Server 决定并发冲突。
- frontend 断开时移除其未完成 ID 路由；不会把 response 错送给其他连接。
- 无法识别的 JSON-RPC envelope 返回/记录协议错误，未知业务 method 不作为协议错误。

## Invariants and non-goals

- request ID 在真实 App Server 连接上全局唯一，返回 frontend 时保持原类型和值。
- action 最多向 App Server提交一次有效 resolution。
- sequence 和 revision 对同一实例/会话单调递增。
- 当前不保证把任意未来官方 server request 自动转换为专用 UI；它仍可作为通用 action 转发。

## Code ownership

- `internal/daemon/instance.go`
- `internal/jsonrpc/message.go`
- `internal/protocol/types.go`
- `internal/store/store.go`

## Verification

- 字符串/数字 ID、乱序 response、Desktop/control 命名空间隔离测试。
- 首次 initialize、重连虚拟化和并发 waiter 测试。
- 审批抢答、重复 resolution、Turn 完成/中断清理测试。
- 未知方法/字段 passthrough 测试。

## Documentation update triggers

修改 ID 格式、initialize 规则、会话状态、sequence/revision、action 仲裁、已识别官方方法或未知消息处理时，必须更新本文档及控制面、存储、Bridge 文档。
