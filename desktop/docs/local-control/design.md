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
- 会话：`conversation.list`, `conversation.read`, `conversation.timeline.list`, `conversation.content.read`, `conversation.content.resolve`, `conversation.subscribe`, `conversation.create`, `conversation.settings.update`, `conversation.queue.add/update/delete/start`
- Turn：`turn.start`, `turn.interrupt`, `plan.respond`（后者要求 `mobile-control-v1`）
- action：`action.list`, `action.resolve`
- catalog：`catalog.models`, `catalog.permissionProfiles`
- 附件：`attachment.begin`, `attachment.chunk`, `attachment.commit`, `attachment.abort`
- 流量：`traffic.enable`, `traffic.disable`
- Remote：`remote.status`, `remote.configure`, `remote.enroll`, `remote.reconnect`, `remote.pair.start/list/resolve/cancel/revoke`

`turn.start` 当前接受 `instanceId`、`threadId`、文字、attachment IDs、model、reasoning effort、approval policy、sandbox policy、client message ID 和 expected revision。附件中的图片被转换为官方 App Server 支持的输入项；调用必须指向已有 thread。忙碌门禁按目标 thread 判断：同一 thread 已有活跃 Turn、等待该 thread 的 action，或 action 无法归属 thread 时拒绝；同一 Agent 的其他 thread 正在运行或等待自身 action 时不阻塞本 thread。实例仍汇总保存全部活跃 Turn，用于状态、升级和强制退出。

Desktop 从 `turn/start`、`thread/settings/update` 和 `thread/settings/updated` 投影各 thread 最近确认的 collaboration mode、model、reasoning effort 与稳定 permission mode，并使用现有加密 settings 存储按 instance/thread 持久化。Mobile 的 `conversation.read.settings` 固定返回 `mode`、`modelId`、`reasoningEffort` 与 `permissionModeId`。投影不保存 developer instructions 或计划正文；Turn 的 started/completed/failed/interrupted 通知用于把配置绑定到权威 Turn 并记录其是否成功完成。`ConversationSnapshot.planConfirmation` 仅在最新 Turn 成功完成、以 plan mode 启动、包含完成的 Plan item、当前 thread 空闲且无阻塞 action，并且 model/effort 足以安全启动下一 Turn 时返回 `{planItemId, turnId, revision}`。

`plan.respond` 接受 `agentId`、`threadId`、`planItemId`、`expectedRevision`、`decision=implement|revise` 和非空 `text`。它重新读取权威 snapshot 并校验计划身份、revision、空闲/action 状态和配置：`implement` 继承 model/effort/permission 并以 `collaborationMode.mode=default` 启动 Turn，`revise` 继承同一组设置并以 `mode=plan` 启动 Turn，两者都显式清空 `developer_instructions`。响应为 `{started:true, turnId?}`；`invalid_params`、`conversation_busy`、`revision_conflict`、`stale_plan`、`plan_mode_unavailable` 和 `provider_error` 保持稳定。该方法复用 `turn.start` pairing scope。

`catalog.models` 在 Mobile contract 下把 App Server catalog 归一化为模型 ID、名称、说明、默认标记、可选择标记、默认思考等级和受支持等级；隐藏的当前模型可以作为不可重新选择的当前值呈现。`catalog.permissionProfiles` 同时读取 permission profile 和配置约束，按可选 `modelId` 应用 auto-review 强制规则，固定输出 `default`、`autoReview`、`readOnly`、`fullAccess` 四项及可用性/原因，Mobile 不解析 provider 原始权限 JSON。四项分别映射到 `:workspace/on-request/user`、`:workspace/on-request/auto_review`、`:read-only/on-request/user` 和 `:danger-full-access/never/user`。

`conversation.settings.update(agentId, threadId, expectedRevision, settings)` 在目标 thread 空闲或运行中且无阻塞 action 时调用官方 `thread/settings/update`；运行中调用只更新后续 Turn，当前 Turn 已捕获的步骤保持不变。选择必须来自当前 catalog，错误稳定为 `invalid_selection`、`selection_unavailable`、`conversation_busy` 或 `revision_conflict`。只有 App Server 明确成功后才提交加密投影，失败或结果不明不进行自动重试。该方法复用 `turn.start` pairing scope。Mobile `turn.start.settings` 使用同一验证和映射，供尚未创建 thread 的草稿在首次发送时一次提交完整设置。

`conversation.read.queue` 通过官方 `thread/queue/list` 返回 App Server 权威排队消息，只投影 `{id,clientMessageId,text,attachmentCount}`，不暴露图片路径、mention 路径或其他 provider 输入。`conversation.queue.add` 复用图片附件解析并映射 `thread/queue/add`；`update` 在替换文字时保留现有非文字输入；`delete` 映射 `thread/queue/delete`；`start` 映射 `thread/queue/start`，用于把指定消息立即作为引导提交。四个方法都要求 `mobile-control-v1` 和 `turn.start` pairing scope；新增/立即引导在 App Server 可写且无阻塞 action 时允许当前 thread 正在运行。写入结果未知时客户端只刷新 queue 对账，不自动重试。

`attachment.begin` 在 Mobile contract 下接受 `agentId`、`projectId`、`conversationId`、`clientMessageId`、`mimeType`、文件名和大小。一次发送生成的 `clientMessageId` 同时用于 begin 和 `turn.start`。Desktop 先用 `agentId` 解析并绑定上传所属实例，再将 provider 路由字段从下游参数中移除；这样上传完成后只能由同一实例的 `turn.start` 使用。附件内容直接写入项目目录的 `.work-anywhere-remote-attachments/<conversationId>/<clientMessageId>/`，文件名为清理后的原名加唯一 attachment ID 后缀。

`conversation.create` 仅在 `mobile-control-v1` 下可用，接受 `agentId` 和当前 Codex `projectId`，映射为 App Server `thread/start`，返回稳定的 `{id, projectId?}`。它是受控写操作，要求在线 App Server；新 pairing 使用 `conversation.create` scope，已有仅授予 `turn.start` 的 pairing 也保持向后兼容。Mobile 收到结果前断线时不得自动重试。

`mobile-control-v1` 接受 `agentId` 作为 `instanceId` 的产品化别名，并把 `query` 转为 App Server `searchTerm`。`agent.list` 返回 Codex Agent summary；Mobile-facing `project.list` 不返回 provider 根路径，`conversation.list` 不返回 preview，只返回稳定导航字段，并在归一化时按 thread ID 去重。Mobile contract 下的 `project.list` 会读取 provider 项目和 thread 列表，按每个项目最近一条对话的 `updatedAt` 倒序聚合，只返回至少有一条对话的项目；并列时间按项目 ID 倒序，分页 cursor 锚定这两个字段。聚合时优先使用 thread 的 `projectId`；字段缺失、为空或指向未知项目时，以 thread `cwd`（必要时使用 environment cwd/workspace roots）匹配最长的 provider 项目根目录。返回给 Mobile 的 `ProjectSummary.updatedAt` 是该项目最近对话的时间，而不是 provider 项目元数据时间。项目过滤的正常路径使用 App Server `thread/list(projectId)`；若结果为空，Desktop 会根据同一 Project ID 解析根目录并以 `thread/list(cwd)` 回退，以兼容项目迁移前仍只保存旧项目关联或 session cwd 的历史线程。`conversation.read` 使用 `thread/read(includeTurns=false)` 读取 metadata，再用 `thread/items/list(sortDirection=desc)` 返回最近 20 项（最大 50）的稳定 `ConversationSnapshot`，同时用 `thread/queue/list` 返回当前 queue；归一化器解包官方 `ThreadItemEntry` 的 `{item,turnId}` 结构，并兼容旧的扁平 fixture。`conversation.timeline.list` 原样续传 App Server opaque cursor 读取更早页。Plan item 的原始 `text` 被归一化为单个 `kind=plan` block；真正超过 5 个源文件行时，普通 Timeline response 只返回原始 Markdown 前五行，并返回绑定 agent/thread/turn/item/block 的随机 `contentRef` 与 UTF-8 字节数。`conversation.content.read` 以稳定定位重新遍历该 turn 的 provider item 页，并按最多 128 KiB 返回正文；引用过期后，Mobile 可用相同定位调用 `conversation.content.resolve` 重新签发引用，item 已删除时返回 `content_not_found`。引用只保存定位，不缓存正文。

`hookPrompt` 不进入 Mobile DTO；function/tool、协作、搜索、图片、sleep、review 和 compaction item 只生成单个结构化 summary block：`summaryKind` 保存 provider item 类型，`summaryValue` 保存最长 512 UTF-8 字节的脱敏单行值，`text` 是语言无关 fallback。完整展示文案由 Mobile 本地化，daemon 不拼接中英文句子。summary 不含 arguments/output/result/error/path/prompt，也不生成 `contentRef`；`commandExecution` 只返回 command，不返回 aggregated output。过滤后可见 item 不足请求数量时，daemon 沿 provider cursor 补读后续页面，最多追踪 8 页。

Mobile control 响应序列化后上限为 512 KiB。时间线超预算时以同一 cursor 按 `20→10→5→2→1` 缩小可见 page；limit 为 1 仍超预算则进一步引用大 block。Daemon 通过 Relay unsolicited event 主动发送 metadata-only conversation event，不携带 provider 正文；Mobile EventSink 收到事件后合并刷新对应 snapshot。Mobile endpoint 只把 `type=event` 送入业务事件流，迟到/未知 response 直接丢弃；无 sink 时最多缓存 1000 条事件，并用 `type=transport` 报告 `online/disconnected/revoked/closed`。EventSink 不可用或断开时才启用 2 秒轮询恢复，恢复 online 后停止轮询并执行一次 cursor catch-up。待追赶事件超过有界窗口时返回 `snapshot=true`，由 Mobile 重新读取分页 snapshot。超限保护返回 `response_too_large`，不会把超大业务响应交给 Relay。Mobile control 结构化日志只记录 method、耗时、响应字节数和稳定错误码。

CLI 对应提供 daemon、instance、conversation、turn、action、catalog、traffic、通用 `call`、`ui`、`self-test`、install/uninstall 和 version 命令。CLI 通过本机受保护管理 Pipe 调用，不自行启动第二个 App Server。

Provider capabilities 当前包含 history、live stream、text/image input、approval、question、interrupt、steer、model/reasoning selection、permission mode 和 desktop mirror。Codex 是唯一 driver；部分能力标志表示协议边界，不代表所有 provider 已实现。

## State and persistence

控制连接和 subscriber 队列在内存中。客户端通过 conversation sequence/cursor 恢复事件；每个 thread 的 collaboration mode/model/effort、关联 Turn ID 与成功状态写入加密 settings，daemon 重启后仍可判定是否能安全响应计划。附件分块直接追加到项目目录下的 `.part` 临时文件，commit 校验大小后原子重命名，再仅把 attachment 元数据和文件路径写入 SQLite。上传失败由 `attachment.abort` 清理临时文件；已 commit 的文件不会因后续 Turn 失败自动删除。`turn.start` 根据 attachment ID 读取文件，仅将 `image/*` 转为官方 App Server 的 data URI，其他 MIME 类型返回 `unsupported_attachment_type`。控制暂停只阻止本机控制操作，不改变 Desktop 原始连接。

## Security and failure handling

- 控制服务仅绑定随机 `127.0.0.1` 端口；管理 Pipe 受当前 SID ACL 保护。
- Web 管理入口使用一次性 fragment token 换取 SameSite/HttpOnly session，并校验 Origin 和 CSRF。
- `expectedRevision` 防止基于陈旧状态开始 Turn。
- 控制面暂停、实例未完成初始化、目标会话正在运行或等待 action 时拒绝不安全操作；其他会话的活动不造成误拒绝。

## Invariants and non-goals

- 本机控制协议使用版本化 envelope，业务错误使用稳定 code/message，而不是泄露内部 panic。
- Relay 出站连接器通过 scope allowlist 复用本控制语义，不绕过 daemon 直接操作 App Server stdio；当前实现见 [`remote-relay-connector`](../remote-relay-connector/design.md)。
- 管理页实现 daemon/Relay/配对管理，不实现聊天、Timeline 或 Prompt UI。
- 本机 control 不直接实现网络身份；网络配对和 Mobile 身份由 outbound Relay connector 负责。push provider 与 action 的完整稳定 DTO 尚未完成。

## Code ownership

- `internal/daemon/daemon.go`（控制路由）
- `internal/daemon/mobile_contract.go`（Mobile DTO、时间线分页和大内容引用）
- `internal/daemon/admin.go`（HTTP/WebSocket 与授权）
- `internal/cli/cli.go`
- `internal/provider/`
- `internal/protocol/types.go`

## Verification

- CLI 查看实例/会话、启动和中断 Turn、列出/解决 action、读取 catalog。
- 附件 begin/chunk/commit 后可用于图片输入；abort、size mismatch、路径穿越、项目目录回退和重启清理测试覆盖文件存储边界。
- cursor 重连补发和 snapshot fallback 测试。
- metadata/timeline DTO、结构化 summary、本地化边界、opaque cursor、自适应页大小、UTF-8 内容分块和引用越权/过期/重新签发测试。
- queue DTO 路径脱敏、文字更新保留非文字输入、四种 provider 方法映射、阻塞 action 门禁和 `turn.start` scope 测试。
- plan/default 配置投影、重启恢复、成功 Plan 门禁、计划身份/revision/action 冲突、模式切换和 `turn.start` scope 复用测试。
- Origin、CSRF、一次性 token、控制暂停和慢订阅者测试。

## Documentation update triggers

增加/删除/重命名控制方法、字段、事件、错误码、Provider capability、附件协议、认证方式或 CLI 命令时，必须更新本文档以及消费该接口的模块文档。
