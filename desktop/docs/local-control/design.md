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

daemon 将控制方法路由到实例 supervisor。Mobile 请求的 `agentId` 若属于已注册的 `provider.Backend`（当前为 [Claude Code provider](../claude-code-provider/design.md)），agent 方法（项目、会话、Turn、action、计划、设置、catalog、`attachment.begin`）改由 `backendControl` 处理；附件分块/提交、系统和 Remote 方法以及未知 agent 仍走共享或 Codex 路径。事件订阅使用有界队列，慢订阅者会被断开，客户端应使用 conversation cursor 补齐状态。

## Interfaces

新增本机 `remote.register`，参数可选 `displayName`、`token`，执行签名注册并返回 remote status。`remote.configure` 接受 `resetIdentity`；已登记身份更换 origin 未确认返回 `relay_change_requires_reset`，明确重置后清除身份和配对。这些操作只属于本机控制，不进入 Remote 业务 allowlist，协议见 [连接器](../remote-relay-connector/design.md)。

当前固定控制方法：

- 系统：`system.status`, `system.ui_url`, `system.control.pause`, `system.control.resume`, `system.shutdown`, `system.autostart.set`（参数 `enabled`；仅本机管理 contract，Mobile contract 调用返回错误，Relay scope 白名单不包含它）

`system.status` 返回 daemon 运行电脑的 `computerName`（来自主机名）以及版本、进程、运行时长、实例和控制状态；Remote 转发给 Mobile 时仍移除 `remote` 字段。 本机管理 HTTP `GET /api/status` 额外附加 `agents`（backend 状态与 Claude Code 诊断、未启用原因）和 `startup`（自启动、`CODEX_CLI_PATH` 集成状态）；二者含本机路径，不进入 `system.status`，见 [管理页与生命周期](../administration-and-lifecycle/design.md)。
- 实例：`instance.list`, `instance.get`, `instance.restart`
- Mobile navigation：`agent.list`, `project.list`（要求 `mobile-control-v1`）
- 会话：`conversation.list`, `conversation.read`, `conversation.timeline.list`, `conversation.content.read`, `conversation.content.resolve`, `conversation.subscribe`, `conversation.create`, `conversation.rename`, `conversation.settings.update`, `conversation.queue.add/update/delete/start`
- Turn：`turn.start`, `turn.interrupt`, `plan.respond`（后者要求 `mobile-control-v1`）
- action：`action.list`, `action.resolve`
- catalog：`catalog.models`, `catalog.permissionProfiles`
- 附件：`attachment.begin`, `attachment.chunk`, `attachment.commit`, `attachment.abort`
- 流量：`traffic.enable`, `traffic.disable`
- Remote：`remote.status`, `remote.configure`, `remote.register`, `remote.enroll`, `remote.reconnect`, `remote.pair.start/list/resolve/cancel/revoke`

`turn.start` 当前接受 `instanceId`、`threadId`、文字、attachment IDs、model、reasoning effort、approval policy、sandbox policy、client message ID 和 expected revision。Mobile 内置权限设置只发送 `sandboxPolicy`、`approvalPolicy` 和 `approvalsReviewer`，不与 `permissions` 混用；profile ID 用于目录可用性校验和界面映射。附件中的图片被转换为官方 App Server 支持的输入项；调用必须指向已有 thread。忙碌门禁按目标 thread 判断：同一 thread 已有活跃 Turn、等待该 thread 的用户 action，或用户 action 无法归属 thread 时拒绝；同一 Agent 的其他 thread 正在运行或等待自身 action 时不阻塞本 thread。`attestation/generate` 等明确分类的客户端基础设施请求不参与会话门禁。实例仍汇总保存全部活跃 Turn，用于状态、升级和强制退出。

Desktop 从成功的 `thread/start`、`thread/resume` 响应，以及 `turn/start`、`thread/settings/update` 和 `thread/settings/updated` 投影每个 thread 的设置。新建/恢复响应在发回 frontend 前提交内存投影，并异步加密持久化；响应不能覆盖请求发出后收到的更新或新进程代次。映射 `model`、`reasoningEffort`、`sandbox`/`sandboxPolicy`、审批策略、reviewer、`activePermissionProfile` 和 collaboration mode，保留完整沙箱及未知策略字段。新建缺省 mode 为 `default`；旧恢复响应缺失 mode 时只保留已知值。null reasoning effort 保持继承语义，不伪造具体档位。

Mobile 的 `conversation.read.settings` 在设置未知时为 `null`，已知时返回 `mode`、`modelId`、`reasoningEffort` 与 `permissionModeId`。只有真实策略不能映射为内置权限时才返回 `custom`，不能作为新会话预设；未知权限不会通过校验。空 reasoning effort 仅在同一模型已有继承状态时接受，并映射为官方 null。投影不保存 developer instructions 或计划正文；Turn 通知把配置绑定到权威 Turn 并记录是否成功完成。`ConversationSnapshot.planConfirmation` 仅在最新 Turn 成功完成、以 plan mode 启动、包含完成的 Plan item、当前 thread 空闲且无阻塞 action，并且 model/effort 足以安全启动下一 Turn 时返回 `{planItemId, turnId, revision}`。

`plan.respond` 接受 `agentId`、`threadId`、`planItemId`、`expectedRevision`、`decision=implement|revise` 和非空 `text`。它重新读取权威 snapshot 并校验计划身份、revision、空闲/action 状态和配置：`implement` 继承 model/effort/permission 并以 `collaborationMode.mode=default` 启动 Turn，`revise` 继承同一组设置并以 `mode=plan` 启动 Turn，两者都显式清空 `developer_instructions`。响应为 `{started:true, turnId?}`；`invalid_params`、`conversation_busy`、`revision_conflict`、`stale_plan`、`plan_mode_unavailable` 和 `provider_error` 保持稳定。该方法复用 `turn.start` pairing scope。

`catalog.models` 在 Mobile contract 下把 App Server catalog 归一化为模型 ID、名称、说明、默认标记、可选择标记、默认思考等级和受支持等级；隐藏的当前模型可以作为不可重新选择的当前值呈现。`catalog.permissionProfiles` 同时读取 permission profile 和配置约束，按可选 `modelId` 应用 auto-review 强制规则，固定输出 `default`、`autoReview`、`readOnly`、`fullAccess` 四项及可用性/原因，Mobile 不解析 provider 原始权限 JSON。四项分别映射到 `:workspace/on-request/user`、`:workspace/on-request/auto_review`、`:read-only/on-request/user` 和 `:danger-full-access/never/user`。

`conversation.settings.update(agentId, threadId, expectedRevision, settings)` 在目标 thread 空闲或运行中且无阻塞 action 时调用官方 `thread/settings/update`；运行中调用只更新后续 Turn，当前 Turn 已捕获的步骤保持不变。选择必须来自当前 catalog，错误稳定为 `invalid_selection`、`selection_unavailable`、`conversation_busy` 或 `revision_conflict`。只有 App Server 明确成功后才提交加密投影，失败或结果不明不进行自动重试。该方法复用 `turn.start` pairing scope。Mobile `turn.start.settings` 使用同一验证和映射，供尚未创建 thread 的草稿在首次发送时一次提交完整设置。
`conversation.rename(agentId, threadId, name)` 只接受 mobile-control-v1。Desktop 统一校验名称：去除首尾空白后 1–200 个 Unicode 字符，不含换行或其他控制字符，否则返回 `invalid_params`。Codex 调用官方 `thread/name/set`；Claude Code 由后端追加 `custom-title` 记录（见 [Claude Code provider](../claude-code-provider/design.md)）。成功返回 `{threadId, name}`，标题变化随后经正常的会话失效事件到达。该方法属于托盘暂停时拒绝的写方法，复用 `turn.start` pairing scope，失败或结果不明不自动重试。

`conversation.read.queue` 通过官方 `thread/queue/list` 返回 App Server 权威排队消息，只投影 `{id,clientMessageId,text,attachmentCount}`，不暴露图片路径、mention 路径或其他 provider 输入。`conversation.queue.add` 复用图片附件解析并映射 `thread/queue/add`；`update` 在替换文字时保留现有非文字输入；`delete` 映射 `thread/queue/delete`；`start` 映射 `thread/queue/start`，用于把指定消息立即作为引导提交。四个方法都要求 `mobile-control-v1` 和 `turn.start` pairing scope；新增/立即引导在 App Server 可写且无阻塞 action 时允许当前 thread 正在运行。写入结果未知时客户端只刷新 queue 对账，不自动重试。

`attachment.begin` 在 Mobile contract 下接受 `agentId`、`projectId`、`conversationId`、`clientMessageId`、`mimeType`、文件名和大小。一次发送生成的 `clientMessageId` 同时用于 begin 和 `turn.start`。Desktop 先用 `agentId` 解析并绑定上传所属实例，再将 provider 路由字段从下游参数中移除；这样上传完成后只能由同一实例的 `turn.start` 使用。附件内容直接写入项目目录的 `.work-anywhere-remote-attachments/<conversationId>/<clientMessageId>/`，文件名为清理后的原名加唯一 attachment ID 后缀。

`conversation.create` 仅在 `mobile-control-v1` 下可用，接受 `agentId` 和当前 Codex `projectId`。Desktop 必须先从 provider 项目目录解析并验证主目录，再向 App Server `thread/start` 同时传递 `projectId` 与可信 `cwd`：旧 runtime 使用项目 ID，新 runtime 根据 cwd 建立项目归属；Mobile 不能直接指定 provider cwd。响应返回稳定的 `{id, projectId?}`。项目不存在、没有主目录或目录不可访问时返回 `project_not_resolved`，不得回退到 daemon 进程目录。它是受控写操作，要求在线 App Server；新 pairing 使用 `conversation.create` scope，已有仅授予 `turn.start` 的 pairing 也保持向后兼容。Mobile 收到结果前断线时不得自动重试。

Mobile-facing `conversation.list` 的每条 `ConversationSummary` 同时包含 Desktop 从完整项目目录解析的 `projectId + projectName`。即使对应项目不在 Mobile 当前加载的首个项目分页中，进入会话详情也不需要再用有限项目列表反查名称；历史 thread 的项目 ID 缺失或失效时，Desktop 使用 cwd/environment workspace roots 恢复归属。

`conversation.read` 的 provider metadata 若缺少 `projectId` 且仍有 cwd/environments，也读取有界分页的 provider 项目目录，复用会话列表的主目录匹配规则恢复归属；查询失败原样返回错误，不使用进程 cwd 猜测项目。官方 tagged status 的 `idle`、`notLoaded`、`systemError` 保持原状态且不标记为运行中。

`mobile-control-v1` 接受 `agentId` 作为 `instanceId` 的产品化别名，并把 `query` 转为 App Server `searchTerm`。`agent.list` 返回 Codex Agent summary；Mobile-facing `project.list` 不返回 provider 根路径，`conversation.list` 不返回 preview，只返回稳定导航字段，并在归一化时按 thread ID 去重。Mobile contract 下的 `project.list` 会读取 provider 项目和 thread 列表，按每个项目最近一条对话的 `updatedAt` 倒序聚合，只返回至少有一条对话的项目；并列时间按项目 ID 倒序，分页 cursor 锚定这两个字段。聚合时优先使用 thread 的 `projectId`；字段缺失、为空或指向未知项目时，以 thread `cwd`（必要时使用 environment cwd/workspace roots）匹配 provider 项目的主目录。Codex 当前的 Project DTO 没有单独的主目录标记，Desktop 将 `roots` 中第一项非空路径视为主目录，后续 roots 仅作为附加工作目录，不参与项目归属匹配。返回给 Mobile 的 `ProjectSummary.updatedAt` 是该项目最近对话的时间，而不是 provider 项目元数据时间。带 `projectId` 的 `conversation.list` 与 `project.list` 使用同一归属规则：Desktop 不把 `projectId` 传给 App Server（其过滤会漏掉只有 session cwd、从未分配项目 ID 的线程），而是按请求的其余过滤条件（排序、`searchTerm` 等）分页读取完整 `thread/list`，按 thread ID 去重并筛出归属该项目的线程，再以 Desktop 自有 cursor（上一页最后一条的位置和 thread ID）分页，单页最多 100 条。`conversation.read` 使用 `thread/read(includeTurns=false)` 读取 metadata，再用 `thread/items/list(sortDirection=desc)` 返回最近 20 项（最大 50）的稳定 `ConversationSnapshot`，同时用 `thread/queue/list` 返回当前 queue；归一化器解包官方 `ThreadItemEntry` 的 `{item,turnId}` 结构，并兼容旧的扁平 fixture。`conversation.timeline.list` 原样续传 App Server opaque cursor 读取更早页。Plan item 的原始 `text` 被归一化为单个 `kind=plan` block；真正超过 5 个源文件行时，普通 Timeline response 只返回原始 Markdown 前五行，并返回绑定 agent/thread/turn/item/block 的随机 `contentRef` 与 UTF-8 字节数。`conversation.content.read` 以稳定定位重新遍历该 turn 的 provider item 页，并按最多 128 KiB 返回正文；引用过期后，Mobile 可用相同定位调用 `conversation.content.resolve` 重新签发引用，item 已删除时返回 `content_not_found`。引用只保存定位，不缓存正文。

Mobile contract 下的 `conversation.list` 额外返回 `needsAttention`，其值来自与该 thread 关联的未完成用户 action；无法归属 thread 的 action 不猜测列表目标。Mobile `action.list` 按创建时间稳定返回且不透传原始 params：普通命令/权限请求归一化为 `kind=approval` 并保留 id、method、conversation/turn id 和 created time；官方 `item/tool/requestUserInput` 归一化为 `kind=userInput`，额外返回每题的 header、id、question、`isOther`、`isSecret` 以及 label/description options。`attestation/generate` 不进入 Mobile DTO、待办计数或 action 事件，由官方 Desktop 透明响应。`action.resolve` 对用户 action 执行 first-valid-wins；问题答案使用官方 `{answers:{questionId:{answers:[value]}}}` 结构，普通审批继续兼容 `{decision:"accept"|"decline"}`。

`hookPrompt` 不进入 Mobile DTO；user message 时间线项把官方 `clientId` 归一化为可选 `clientMessageId`，供 Mobile 将立即显示的本地发送气泡与权威消息精确合并，旧 runtime 未提供该字段时保持省略。function/tool、协作、搜索、图片、sleep、review 和 compaction item 只生成单个结构化 summary block：`summaryKind` 保存 provider item 类型，`summaryValue` 保存最长 512 UTF-8 字节的脱敏单行值，`text` 是语言无关 fallback。完整展示文案由 Mobile 本地化，daemon 不拼接中英文句子。summary 不含 arguments/output/result/error/path/prompt，也不生成 `contentRef`；`commandExecution` 只返回 command，不返回 aggregated output。过滤后可见 item 不足请求数量时，daemon 沿 provider cursor 补读后续页面，最多追踪 8 页。

Mobile control 响应序列化后上限为 512 KiB。时间线超预算时以同一 cursor 按 `20→10→5→2→1` 缩小可见 page；limit 为 1 仍超预算则进一步引用大 block。Daemon 事件带进程级 `syncEpoch`，每会话保留最近 256 条、全局 payload 预算 32 MiB；窗口或预算淘汰后旧 cursor 返回 `snapshot=true`。

实时正文通过 `conversation.delta` 发送。Desktop 按 instance/conversation/turn/item/kind/index/stream 保持顺序并在段落、句末、行边界、200–250 ms 或大小阈值到达时合并 flush；审批、提问、队列、item 完成和 Turn 终态发送前先 flush。单批最大 32 KiB；异常的超大单个 provider delta 转为 `conversation.invalidated`，由 Mobile 读取权威快照。未知 provider 事件也只触发失效。

每个 Noise session 有独立 writer，高、低优先级队列各限制 1 MiB。response、审批和终态优先于 delta；加密和 Relay 写入只在 writer 中串行执行。低优先级溢出时每会话只排入一次 `conversation.invalidated`，高优先级溢出时关闭 session，客户端通过重连和 snapshot 恢复。Mobile endpoint 无 EventSink 时最多缓存 256 条或 2 MiB，溢出先发失效事件再转交保留项。EventSink 断开时启用 2 秒轮询，恢复 online 后停止轮询并 catch-up。超限保护返回 `response_too_large`。Mobile control 结构化日志只记录 method、耗时、响应字节数和稳定错误码。

CLI 对应提供 daemon、instance、conversation、turn、action、catalog、traffic、通用 `call`、`ui`、`self-test`、install/uninstall 和 version 命令。CLI 通过本机受保护管理 Pipe 调用，不自行启动第二个 App Server。

非 Codex agent 在 `agent.list` 中携带 `capabilities`（`history`、`liveStream`、`textInput`、`imageInput`、`approval`、`question`、`interrupt`、`steer`、`queue`、`plan`、`createConversation`、`modelSelection`、`reasoningSelection`、`permissionMode`、`desktopMirror`、`rename`）；Codex agent 当前省略该字段，按全部支持处理。后端不支持的方法返回 `unsupported_capability`；后端特有错误码包括 `conversation_busy_external`（会话正被电脑端其他进程使用）、`runtime_unavailable`（找不到可用 CLI）和 `stale_plan`。`ConversationSummary` 和快照 metadata 可带 `externallyActive=true`，Mobile 只读展示。Mobile `action` 可带可选 `detail`（`toolName`、`description`、`command`、`filePath`、`reason`，均为有界单行值），Codex action 当前不提供。后端事件映射为：`provider.conversation.updated`/`provider.conversations.changed`（失效）、`provider.item/agentMessage/delta` 与 `provider.item/reasoning/textDelta`（payload `{params:{threadId,turnId,itemId,delta}}`，由 Remote 批处理为 `conversation.delta`）、`action.requested`/`action.resolved`、`provider.turn/started|completed|failed|interrupted`。daemon 为后端会话维护 sequence（所有事件递增）和 revision（仅 Turn 与 action 变化递增，`turn.start`、`plan.respond`、`conversation.settings.update` 的 `expectedRevision` 与之比较）；订阅不回放事件，cursor 不等于当前 sequence 时返回 `snapshot=true`。

### 会话读取兼容与写入版本

Mobile 快照携带可选 `capabilities.queue`；缺失按支持处理，`false` 表示当前会话的 provider 明确不支持队列，客户端不显示队列编辑且运行中保留草稿。普通队列读取错误不会被转换为空队列。

Timeline 优先调用 `thread/items/list`。仅官方 JSON-RPC `-32601` 方法不支持错误触发 `thread/read(includeTurns=true)` 回退，按 Turn/item 原始顺序展开，再按最近项优先返回。成功的兼容后端只缓存到当前实例进程代次和 thread；空会话的暂时不支持不缓存为永久后端。原生页继续使用 provider cursor；兼容 cursor 使用 `wa:history:` 前缀、base64url JSON，携带版本 1、backend、Agent/thread、Turn/item 锚点，不携带正文，最长 4096 字符。归属错误返回 `invalid_params`，锚点删除或原生后端变更返回 `cursor_expired`，Mobile 重新加载最新页。大内容定位和引用重签复用同一后端；兼容读取需要完整 provider history，但 Mobile 响应、预览和正文分块限制保持不变。

metadata 与请求会话 ID 匹配、没有待启动或活跃 Turn 时，允许以可验证的空会话证据将明确无 rollout 错误或 `list_turns is not supported yet` 转为空 timeline。路径不存在须有当前进程创建证据；已有文件须经符号链接解析后位于该实例的 sessions 目录，大小不超过 1 MiB，且每条记录均为相同会话 ID 的 `session_meta`。该证据可跨 daemon 重启使用；读取失败、损坏、越界或任何真实内容均保留错误，历史读取完成后再次检查。队列仅在相同边界下兼容无 rollout 错误。

已确认空会话在官方进程中尚未加载时，手机读取只记录当前代次的恢复提示。用户主动发送经过 revision/busy 检查并预留 Turn 后，重新验证官方 metadata 和磁盘证据，再调用 `thread/resume(excludeTurns=true)`；恢复失败不提交用户消息，恢复成功才继续设置校验和首次发送。不自动重放消息、不改写 rollout，不伪造 Desktop 原始 RPC 响应。

快照在读取前后比较业务 revision，最多共读取三次；仍有业务变化时返回 `snapshot_unstable`。发送、设置和队列控制写入按 thread 串行；启动 Turn 时在实例锁内检查版本、busy/action 并建立 pending 标记，provider 错误、取消或超时清理标记，成功响应或通知转为 active。官方 Desktop 请求继续由 App Server 仲裁，不承诺跨所有 frontend 的官方 CAS。

业务 revision 与订阅 sequence 的区别由 [JSON-RPC 会话设计](../json-rpc-and-actions/design.md) 定义，持久化恢复由 [存储设计](../persistence-and-security/design.md) 定义。日志只新增 request/Agent/thread 标识和状态，不记录 prompt。

## Compatibility diagnostics

`self-test` 是基础 runtime/instance 发现检查；`self-test --protocol` 在本机执行官方一次性策略检查和临时 CODEX_HOME 握手，不使用用户 daemon、不发起模型请求。`instance.get/list` 新增可选 `lastExitCode`，保留 `lastError` 的有界进程诊断。WAI1、mobile-control-v1 DTO 和 Relay protobuf 不变。

基础设施请求的分类与接收者约束由 [JSON-RPC 设计](../json-rpc-and-actions/design.md#pending-actions) 定义；Mobile 的 action、needsAttention 与写入门禁只考虑用户 action。`partial_answer` 与未知消息阶段按正常文字归一化，Turn 完成仅由 Turn 生命周期判断；imageGeneration 无 result 字段仍显示现有摘要，不新增图片读取能力。

## State and persistence

控制连接、subscriber 队列、会话 projection、pending action 和附件索引都在内存中。客户端通过 conversation sequence/cursor 恢复；每个 thread 的 collaboration mode/model/effort 和 permission 设置写入 DPAPI 保护的 JSON 配置，最多保留最近 2,000 条，不保存正文或计划内容。全部会话设置写入按实例串行，并在落盘前丢弃已被后续 Turn 状态取代的旧快照，避免同步设置更新或 started/completed 通知被旧快照倒序覆盖。

附件分块直接追加到项目目录下的 `.part` 临时文件，commit 校验大小后原子重命名；索引不落盘。明确接受 `turn.start` 或 `conversation.queue.add` 后删除附件，失败或结果未知最多保留 24 小时。`turn.start` 根据 attachment ID 读取文件，仅将 `image/*` 转为 App Server data URI；全局只允许一个图片编码任务，其他 MIME 返回 `unsupported_attachment_type`。控制暂停只阻止本机控制操作，不改变 Desktop 原始连接。

## Security and failure handling

- 控制服务仅绑定随机 `127.0.0.1` 端口；管理 Pipe 受当前 SID ACL 保护。
- Web 管理入口使用一次性 fragment token 换取 SameSite/HttpOnly session，并校验 Origin 和 CSRF。
- `expectedRevision` 防止基于陈旧状态开始 Turn。
- 控制面暂停、实例未完成初始化、目标会话正在运行或等待用户 action 时拒绝不安全操作；其他会话的活动和客户端基础设施请求不造成误拒绝。

## Invariants and non-goals

- 本机控制协议使用版本化 envelope，业务错误使用稳定 code/message，而不是泄露内部 panic。
- Relay 出站连接器通过 scope allowlist 复用本控制语义，不绕过 daemon 直接操作 App Server stdio；当前实现见 [`remote-relay-connector`](../remote-relay-connector/design.md)。
- 管理页实现 daemon/Relay/配对管理，不实现聊天、Timeline 或 Prompt UI。
- 本机 control 不直接实现网络身份；网络配对和 Mobile 身份由 outbound Relay connector 负责。push provider 与 action 的完整稳定 DTO 尚未完成。

## Code ownership

- `internal/daemon/daemon.go`（控制路由）
- `internal/daemon/mobile_contract.go`（Mobile DTO、时间线分页和大内容引用）
- `internal/daemon/backends.go`（非 Codex 后端注册、路由、版本、事件与图片附件读取）
- `internal/provider/provider.go`（共享 Mobile DTO 与 `Backend` 接口）
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
- Mobile 新建会话只传项目 ID；Desktop 注入 provider 项目主目录，并验证真实 App Server 返回的 thread 保持同一项目归属。
- Origin、CSRF、一次性 token、控制暂停和慢订阅者测试。

## Documentation update triggers

增加/删除/重命名控制方法、字段、事件、错误码、Provider capability、附件协议、认证方式或 CLI 命令时，必须更新本文档以及消费该接口的模块文档。

## 权限策略与请求边界

每个线程的加密配置包含可选 `sandboxPolicyJSON`，保存完整策略和未知字段。模型、思考等级或协作模式更新保留有效策略；计划确认只提交 mode/model/effort 并继承线程的实时权限。自定义权限的模型/模式更新不重建权限，交由 App Server 校验配置要求。显式切换至 workspace 模式时读取 thread cwd 和该项目的有效 `config/read`，保留 networkAccess、writableRoots、excludeSlashTmp、excludeTmpdirEnvVar；无法读取配置返回 `selection_unavailable`。同一 workspace 策略仅切换 reviewer 时原样保留。旧记录可读；同一权限模式下仅修改模型/思考/计划模式时省略权限覆盖，继承实时策略，等待权威通知补齐。缺少完整策略而显式切换 workspace 模式时读取有效项目配置，不根据类型猜测。

设置 RPC 失败不提交本地成功状态；期间收到的权威设置通知优先，即使其内容与更新前相同。提交与通知版本比较在同一锁下完成，落盘继续使用现有串行加密存储。

控制请求在发出 Turn 开始事件和写入 App Server 前检查完整序列化 JSONL（含换行）是否超过 64 MiB，超限返回 `too_large`；Mobile 保留草稿。权限回归覆盖两套仓库协议 fixture 的权限字段、互斥字段、仅 sandbox 回执、配置保留、custom、计划继承和失败/权威通知竞争。
