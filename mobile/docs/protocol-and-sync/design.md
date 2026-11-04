# Mobile protocol and synchronization

> 状态：Mobile Go Core 已复用 Relay endpoint 完成 pair/auth/Noise/control、多 pairing catalog、选择/启停/排序/Relay 更新/本机删除和 Android 安全恢复路径；Desktop 已发布 normalized summary、分页 timeline、大内容引用、会话 settings/catalog 和 `request_user_input` 问题 DTO，Mobile 会话控制器已支持分页、局部重试和 gap snapshot 恢复。完整 approval DTO、多 pairing 真机 E2E 和 iOS host 尚未完成。

## 目标与范围

定义 Mobile 如何复用现有 pairing、WSS、Noise 和 Desktop control contract，并把不可靠在线路由转换成不会误报成功、不会盲目重放写操作的 UI 状态。

## 外部与内部接口

`conversation.read.settings` 可为 null，表示尚无可靠设置；模型为空的旧版快照同样按未知处理，并清除旧设置投影。有效快照仍受 sequence/syncEpoch 约束，迟到快照不能清除较新设置。已有会话普通发送省略 settings、继承官方配置；用户主动修改通过 `conversation.settings.update` 提交。新建会话首条发送将草稿设置保存在内存提交记录中，获取最新快照只更新 revision/运行状态，不覆盖该选择。失败重试复用会话 ID 和设置；结果不明时仅对账，不自动重放。

外层唯一 schema 是 [`relay.proto`](../../../relay/proto/waw/relay/v1/relay.proto)，当前使用：

- pair：`server_challenge`、`pair_join`、`pair_result`；
- authenticated connect：`server_challenge`、`client_auth`、`auth_ok`；
- session：`session_open/handshake/data/close`、`route_ack`、`delivery_ack`；
- lifecycle：`pair_revoked`、`ping/pong`、`protocol_error`；
- push：`push_register/push_unregister`。

密文 `desktop-control-json-v1` 只调用现有 allowlist，并按 scope 驱动 UI：

| scope | Mobile 能力 | control methods |
| --- | --- | --- |
| `status.read` | Daemon/Agent 状态 | `system.status`, `instance.list/get`, `agent.list` |
| `conversation.read` | 项目/对话列表、搜索、snapshot、增量与大内容读取 | `project.list`, `conversation.list/read/subscribe`, `conversation.timeline.list`, `conversation.content.read/resolve` |
| `conversation.create` | 首次发送时以当前项目 ID 创建 Codex thread；Desktop 解析项目主目录并适配 provider 的 cwd 归属协议 | `conversation.create` |
| `turn.start` | 继续已有 thread、管理运行中消息队列、修改会话设置、重命名会话或响应已完成计划 | `turn.start`, `conversation.queue.add/update/delete/start`, `conversation.settings.update`, `conversation.rename`, `plan.respond` |
| `turn.interrupt` | 中断活跃 Turn | `turn.interrupt` |
| `action.read` | 查看待处理 action | `action.list` |
| `action.resolve` | 回答 approval/question | `action.resolve` |
| `attachment.write` | 图片分块上传 | `attachment.begin/chunk/commit/abort` |
| `catalog.read` | 模型、思考与权限选项 | `catalog.models/permissionProfiles` |

Mobile 不发送 `system.shutdown`、traffic、UI、install/uninstall、force-stop 或 `instance.restart`。即使未来 wire 收到相同字符串，客户端和 Desktop 都必须 fail closed。

### Codex-first navigation contract

Desktop 的 `instance` 在 `mobile-control-v1` 中映射为 `Agent`，不把 Windows 进程或 App Server 暴露成产品概念。当前 normalized control 层提供：

```text
agent.list()
project.list(agentId, limit, cursor?)
conversation.list(agentId, projectId?, query?, limit, cursor?)
conversation.read(agentId, threadId, limit=20)
conversation.timeline.list(agentId, threadId, cursor, limit=20)
conversation.content.read(agentId, threadId, contentRef, offset)
conversation.content.resolve(agentId, threadId, turnId, itemId, blockIndex)
conversation.subscribe(agentId, threadId, cursor)
plan.respond(agentId, threadId, planItemId, expectedRevision, decision, text)
```

`agent.list` 的 Codex adapter 可由现有 `instance.list` 投影；Mobile contract 下的 `project.list` 由 Desktop 根据 provider 项目和完整 thread metadata 聚合，只返回有对话的项目，并以项目最近一条对话的 `updatedAt desc, projectId desc` 排序。Desktop 优先使用 thread `projectId` 关联项目；该字段缺失、为空或无效时，根据 thread cwd/environment workspace roots 匹配 provider 项目的主目录。Codex 当前的 Project DTO 没有单独的主目录标记，Desktop 将 `roots` 中第一项非空路径视为主目录，后续 roots 仅作为附加工作目录，不参与项目归属匹配。`ProjectSummary.updatedAt` 表示该最近对话时间；cursor 以更新时间和项目 ID 锚定，不能因并列时间重复或漏项。不能由 Mobile 对最近 20 条记录按路径字符串猜测项目。`conversation.list` 的 `projectId`、`query`、`limit` 和 cursor 由 Desktop 执行，排序固定为 `updatedAt desc, id desc`，同一 cursor 下不能因并列时间重复或漏项。Desktop 归一化 `thread/list` 时以及 Mobile 展示分页结果时都以会话 ID 作为唯一键；同一会话在 provider 刷新或分页边界重复返回时只保留一条。同名但 ID 不同的会话仍分别展示。

Agent 声明 `navigationKind = projects | bots | none`。Codex 为 `projects`；Hermes 未来为 `bots`，并在实现时增加同属 `conversation.read` scope 的 `bot.list`。Claude Code 由 Desktop 后端提供（`kind=claude-code`，`projects` 导航，见 [Desktop 设计](../../../desktop/docs/claude-code-provider/design.md)）：会话浏览、新建、发送文字/图片、实时文字与思考、审批、提问、计划确认、中断、模型/effort/权限选择使用与 Codex 相同的方法和 DTO；`capabilities.queue`/`steer` 为 false，快照 `capabilities.queue=false`；action 可带 `detail`；`ConversationSummary`/快照 metadata 的 `externallyActive=true` 表示会话正在电脑端使用，`turn.start` 返回 `conversation_busy_external`。Mobile 解析 agent `capabilities`（缺失按全部支持），并展示 `externallyActive` 与 action `detail`，交互见 [产品交互](../product-interaction/design.md)。未适配的 OpenCode、Pi 等可以由 `agent.list` 返回 discovery metadata，但 `supported=false`，Mobile 不调用项目/对话写接口。

## 配对流程

```text
scan/paste waw://pair
        │ parse + local expiry/host checks
        ▼
show unverified fingerprint ── user confirms ──► generate/load Mobile identity
        │
        ▼
WSS /v1/pair challenge ── HMAC proof + public keys ──► wait for Desktop decision
        │
        ├─ rejected/expired/offline ──► discard candidate, keep identity
        └─ approved ── verify daemon key + grant + scope ──► persist PairingRecord
```

QR URI 中的 secret 只存在解析流程内存；截图、analytics、crash report、clipboard history 和日志都不能接收完整 URI。粘贴 URI 后应立即清空文本 controller；是否清空系统剪贴板由用户显式选择，App 不静默覆盖用户剪贴板。

pair/auth/handshake 失败属于 pairing flow 错误，Mobile 必须停留在 pairing 页面并允许重新扫描，不能把它提升为工作台加载失败。grant 已验证并持久化后，后续 `agent.list`、项目或对话初始同步失败只将该 Daemon 标记为待重试/同步失败；已建立的 PairingRecord 保留，根页面不得回退到 unpaired。

Mobile display name 是不可信 metadata。Desktop 明确批准前，Mobile 页面文案固定为“等待电脑确认”，不能把扫码本身显示成成功。配对结果必须逐项验证 daemon ID/keys、Mobile ID、pairing ID、scope、grant signature 和 expiry；任何不匹配都删除候选状态。

## 连接状态机

```text
unpaired
  └─ pair approved → disconnected
disconnected
  └─ foreground/manual retry → connecting → authenticating → handshaking → syncing → online
connecting/authenticating/handshaking/syncing
  ├─ transient error → backoff → connecting
  ├─ revoked/key mismatch → revoked
  └─ app background → disconnected
online
  ├─ socket/gap/decrypt error → disconnected
  ├─ app background → disconnected
  └─ pair_revoked → revoked
```

当前 Go endpoint 在已有 pairing credential 且请求发现 socket 已断开时，会在该请求边界同步重建 authenticated WSS/Noise session；同一 runtime 的并发请求由单一重连门闩串行化，失败后共享 500 ms 起、最大 30 s 的指数冷却。Relay `/v1/connect` 的 429 映射为 `rate_limited`，不会被伪装成新的拨号错误；冷却只抑制重复连接，不重放已路由的写请求。endpoint 为 `SessionOpen` 关联 route ACK，明确拒绝时立即返回对应错误，整个建链最多等待 30 秒；已建立 session 的请求若收到 `peer_offline` route ACK，endpoint 立即销毁当前 WSS/Noise transport、通知 Flutter `disconnected`，下一次恢复读再创建全新 session，不能继续显示或复用假在线连接。endpoint 只接受 pairing、session、sender 和 recipient 全部匹配当前建链尝试的 `SessionHandshake`，迟到的旧握手直接丢弃。建链成功后，socket 生命周期转交给 runtime 自有 context，不继承触发重连的单次业务请求 deadline。Mobile 收到不属于当前 session 的迟到 data frame 时同样直接丢弃，Desktop daemon 在新握手完成时清理同一 pairing 的旧 session，避免旧密文触发新连接的解密失败。已验证 credential 在建链前进入 runtime，启动恢复失败后仍可由后续请求重连；配对批准后的 credential 即使首次建链失败也必须持久化。旧连接的迟到 callback 不得修改新 session。Flutter 前台仍在 disconnected 状态每 2 秒进行 best-effort 同步，鉴权失败、grant/key 不匹配和 revoke 不进入无限重试。

## 初始同步与增量

进入 `syncing` 后顺序执行：

1. 配对二维码携带 daemon 运行电脑的 `computerName`（由 Desktop 使用主机名生成）；首次配对成功后 Mobile 从配对结果读取并保存为 Daemon display name。后续恢复、刷新和未选中 Daemon 的状态探测不再为显示名调用 `system.status`，也不覆盖本地已修改的名称。随后调用 `agent.list`，恢复该 Daemon 上次选中的 Agent，记录不存在时使用返回列表中的第一个 Agent，不根据 ready/error 状态自动改选其他 Agent。
2. 独立保留完整 Agent 列表与当前选择，再对选中 Agent 并发调用 `project.list(limit=10)`、`conversation.list(limit=20)` 与 `action.list`。`navigationKind=bots` 时把 project call 替换为 `bot.list`；Codex-first V1 不进入该分支。当前 Agent 的项目或对话同步失败时，只清空该 Agent 的内容并显示局部重试状态，Agent selector 必须继续保留；切换 Agent 时立即更新选择并独立同步新 Agent，前一个 Agent 的迟到结果或失败不得覆盖当前 Agent。
3. 点击项目后调用 `conversation.list(projectId, limit=15)`；“显示更多”只使用 response next cursor，不用 offset 猜页。
4. 搜索时分别调用 `project.list(query, limit, cursor)` 与 `conversation.list(query, limit, cursor)`，搜索服务端全集而不是当前 10/20 条内存列表。
5. 打开会话时调用 `conversation.read(limit=20)` 获取 metadata、revision/sequence 和最近 timeline 页；页面按 ID 去重并倒转为正序展示。滚动到顶部用 opaque `nextCursor` 调用 `conversation.timeline.list`，不使用 offset。
6. EventSink 对连续 `conversation.delta` 直接更新当前 item；其他事件约 150 ms 合并后刷新。只有 `syncEpoch` 相同且 `fromSequence == 当前 sequence + 1` 的 delta 可应用；gap、epoch 变化、终态、过期 cursor、内存超限或 `snapshot=true` 触发 `conversation.read`。
7. `revision` 随 snapshot/event 更新；`turn.start` 必须提交最后已确认的 `expectedRevision`。
8. `conversation.read` 可返回可选 `planConfirmation={planItemId,turnId,revision}`。仅把该权威字段作为计划确认入口，不能从 Markdown 或历史 Plan item 推断；提交 `plan.respond` 后继续通过 snapshot/event 确认最终状态，不因超时自动重复写入。

App 前台对未选中 Daemon 以有界短连接依次执行 status probe，更新下拉列表的 `connecting/online/offline + checkedAt` 后关闭；只有选中 Daemon 保持前台 session。切换 Daemon/Agent/project/thread 或 search query 时取消旧 read request，并用 selection generation 丢弃迟到 response。实时 event 可以更新项目/对话摘要，但只有属于当前 Agent/thread 且 sequence 连续的 event 才能写入详情 reducer。

管理页的删除电脑是本机 catalog 操作：Go Core 移除指定 pairing runtime 并关闭连接，Android host 在返回成功前持久化不含该 credential 的加密 catalog；持久化失败时恢复删除前 catalog 并返回错误。若删除当前电脑，Core 选择排序最前的剩余已启用电脑并递增 generation，Flutter 清空旧 Agent/Project/Conversation projection 后重新同步。删除最后一台时清除本机选择偏好并进入配对页。此操作不发送远端 revoke；Desktop 授权需要在 Desktop 管理页单独撤销。

### 规范化 DTO 当前边界与发布门槛

`mobile-control-v1` envelope 会回显 contract，未知 contract 返回 `unsupported_contract`。Mobile-facing target 参数统一使用 `agentId`，Desktop 只在 provider 边界内转换为 `instanceId`；附件上传会先绑定该实例，再把 `instanceId` 从 provider 参数中移除。`agent.list`、`project.list`、`conversation.list`、conversation timeline 与 model/permission catalog 已输出稳定 DTO；project list 不暴露 root，conversation list 不暴露 preview，Mobile 不解析 provider 原始权限结构。Mobile `action.list` 不返回原始 params：通用 approval 只返回归属和 method，`item/tool/requestUserInput` 额外归一化 questions/options。

`conversation.read` 组合 metadata-only `thread/read`、`thread/items/list(sortDirection=desc)` 和 `thread/queue/list`，默认 timeline 20、最大 50。snapshot 包含 metadata、revision、sequence、可选 `syncEpoch`、timeline、权威 queue 和 `settings=null|{mode,modelId,reasoningEffort,permissionModeId}`。queue item 固定为 `id`、`clientMessageId`、`text` 与 `attachmentCount`，不得把 Desktop 图片/mention 路径带到 Mobile。`TimelineItem` 固定为 id、可选 turnId、kind、status 与 blocks，不透传 provider JSON。Plan item 使用单个 `kind=plan` block，daemon 保留原始 Markdown；真正超过 5 个源文件行时普通 Timeline response 只返回前 5 行，并为完整正文返回 `contentRef` 与 `totalBytes`，不生成 title/summary。Plan 和其他大 block 通过 `conversation.content.read` 按 128 KiB 分块读取；引用过期后，Flutter 使用私有的 turnId/itemId/blockIndex 调用 `conversation.content.resolve`，daemon 在该 turn 内分页查找 item 并重新签发引用，找不到时返回 `content_not_found`，不要求刷新或丢弃已加载旧页。

`catalog.models` 返回模型 ID/名称/说明、default/selectable、默认思考等级与该模型支持的等级；`catalog.permissionProfiles(modelId)` 固定返回 `default`、`autoReview`、`readOnly`、`fullAccess` 及 Desktop 根据 profile catalog、企业配置和该模型 auto-review 要求计算的可用性。更换模型时 Mobile 重新读取权限 catalog，保留当前自定义权限或仍可用的内置权限，并仅保留新模型仍支持的思考等级，否则分别回落到可用权限和模型默认等级。`conversation.settings.update` 携带最后确认的 revision 和完整 settings；Desktop 仅发送 `sandboxPolicy` 和审批字段，不同时发送互斥的 `permissions`。投影优先采用 `activePermissionProfile`，缺失时依据 sandbox、审批策略与 reviewer 推导，无法准确映射则显示 `custom`（自定义权限）。自定义权限仅代表当前配置，不进入新会话预设；模型变化保留它并由 App Server 校验。完整策略在 Desktop 加密保存，模型/思考/模式变化不得重置网络或可写目录。空闲或运行中的 thread 均可在无阻塞 action 时更新后续 Turn 设置，当前正在执行的 Turn 不改变；明确失败后刷新 snapshot，结果不明不自动重试。尚未创建的草稿只在进程内保存选择，首次 `turn.start` 才提交完整 settings。计划的 revise/implement 继承 model/effort，省略权限覆盖以继承实时线程权限，implement 将 mode 切回 `default`。

目标 thread 运行时，Composer 继续接受文字和图片，发送调用 `conversation.queue.add` 并携带稳定 `clientMessageId`；成功后从下一次 snapshot 显示权威 queue。编辑操作先调用 `conversation.queue.delete`，明确成功后才把文字和当前进程仍持有的图片草稿放回 Composer；应用重启后无法恢复原图片内容的排队消息不允许编辑，但仍可立即引导或取消。立即引导调用 `conversation.queue.start`，取消调用 `conversation.queue.delete`；`conversation.queue.update` 保留为稳定协议能力，用于原位替换文字且保留非文字输入。所有队列写入结果未知时只刷新 snapshot 并按 ID/clientMessageId 对账，不自动重试。

`hookPrompt` 不进入 DTO；function/tool、协作、搜索、图片、sleep、review 和 compaction item 只返回单个 `kind=summary` block。该 block 的 `summaryKind` 是原始类型，`summaryValue` 是脱敏、单行且不超过 512 UTF-8 字节的值，`text` 是语言无关 fallback；完整句子和 status 由 Flutter ARB 本地化。summary 不包含 arguments/output/result/error/path/prompt，不生成 `contentRef`；commandExecution 只返回 command block。过滤后可见 item 不足请求数量时 daemon 沿 provider cursor 补读后续页面，最多 8 页。其他单 block 超过 64 KiB 时内联 UTF-8 前 16 KiB，并用 15 分钟滑动过期的 `contentRef` 分块读取。项目下对话列表与 `project.list` 使用同一归属规则（有效 `projectId` 优先，否则按 cwd 匹配项目主目录），由 Desktop 在完整 thread 列表上筛选并分页，因此未分配项目 ID 的新线程也会出现在所属项目中。Desktop control response 不超过 512 KiB；页面超预算时以相同 cursor 将 limit 逐次减半。

`conversation.subscribe` 保留为恢复用的 cursor/snapshot 接口；snapshot、subscription 和 event 都可带 `syncEpoch`。daemon 每次启动生成新 epoch。Mobile 只在 epoch 相同且 `fromSequence == 当前 sequence + 1` 时应用 `conversation.delta`；epoch 变化会清空旧权威 sequence、cursor 和流式 projection，但保留本地待发送气泡。item/Turn 终态始终读取一次权威 snapshot，并按 item ID 替换临时流式 item。新 Mobile 连接旧 Desktop 时继续把 metadata-only event 当失效提示；旧 Mobile 收到 `conversation.delta` 时刷新 snapshot。

Desktop 对文字、计划、推理、命令和文件输出按语义合并 delta。普通文字在段落、句末且至少 256 字节、250 ms 或 2 KiB flush；plan/reasoning 在段落、250 ms 或 4 KiB flush；命令/文件输出在行边界且至少 2 KiB、200 ms 或 8 KiB flush。key、Turn、item、kind、index/stream 变化及关键事件前立即 flush，单批硬上限 32 KiB。异常超大单个 provider delta 或未知事件降级为 `conversation.invalidated`。

Android 正常路径由 Relay unsolicited event 推送；endpoint 只转发 `type=event`，无 sink 时保留 256 条或 2 MiB，溢出丢弃旧 delta并标记重新读取 snapshot。连接生命周期另发 `type=transport`。Flutter 对 delta 以 75 ms 节奏分块渲染，单 item 最多 512 KiB、当前会话最多 4 MiB；超过后停止拼接并读取 snapshot/contentRef。断线后每 2 秒 catch-up，online 后停止轮询并再同步一次。

- `DaemonSummary`：由本地 pairing + live probe 组合的 id、配对时读取的 daemon 运行电脑 `computerName` 或后续本地管理名称、connection state、checked time；名称不可用时使用本地化通用文案，不把固定产品文案冒充主机名；
- `AgentSummary`：agent id、provider (`codex` 等)、display name、state、supported、navigation kind、capabilities、active turn/pending action count、last error；
- `ProjectSummary`：稳定 project id、name、conversation/running counts、updated time；
- `BotSummary`：为 Hermes 预留的稳定 bot id/name/state/capabilities，Codex 不返回；
- `ConversationSummary`：thread id、title、project id、project name、status、`running`、`needsAttention`、created/updated time；project name 由 Desktop 使用完整项目目录解析，不依赖 Mobile 当前加载的项目页；`needsAttention` 只由与该 thread 关联的未完成 action 投影；
- `PendingAction`：id、kind (`approval`/`userInput`)、method、conversation/turn id、created time；`userInput` 额外包含 questions，每题固定为 header、id、question、`isOther`、`isSecret` 与 label/description options；
- `Catalog`：稳定 ID/label/capability/availability，不把 provider enum 当 UI 文案；
- `Page<T>`：items、next cursor、has more；project 默认请求 10、项目对话 15、最近对话 20，数量是 client request 而非 server hard limit；
- 稳定 error code 包含 `payload_too_large`、`response_too_large`、`content_ref_expired`、`content_not_found`、`peer_offline`、`ambiguous_write`、`queue_full`、`request_timeout`，并保留 busy、revision conflict、already resolved 与 forbidden。

Mobile 产品页面只消费稳定 timeline、catalog/settings 和 action DTO；通用 approval 尚未提供完整上下文，只保留允许/拒绝兼容交互。

## 写操作语义

### Turn start

1. 本地检查 `online`、`turn.start` scope 和无 blocking action；idle 时开始新 Turn，running 时新增权威 queue item。
2. 一次发送先生成唯一 `clientMessageId`，捕获文字与图片快照并立即建立仅驻留进程内存的本地消息气泡；Composer 随即清空并允许准备下一条，当前提交结束前禁止再次发送。Mobile 每条消息最多接受 10 张图片，按用户选择顺序逐张执行 begin/chunk/commit，所有图片共用该 ID。begin 同时传递当前会话 metadata 中的 `projectId`、conversation ID、MIME、原始文件名和大小。气泡显示逐文件进度，分块大小为 256 KiB。
3. 上传失败调用 `attachment.abort` 清理 Desktop 的 `.part` 文件，不提交 Turn；失败气泡保留重试和编辑入口。已经 commit 的附件 ID 可在同一会话重试时复用，未完成图片重新上传，重试生成新的 `clientMessageId`。
4. response 成功后气泡显示等待电脑确认；权威 timeline 的 user message 通过 `clientMessageId` 替换本地气泡。兼容未返回该字段的旧 Desktop 时，只允许用成功响应中的 Turn ID 和该 Turn 下唯一 user message 对账，不能按正文或 running 状态猜测。
5. 全部图片 commit 后，`turn.start.attachmentIds` 按选择顺序一次提交 0–10 个 ID。任一图片上传失败时不调用 `turn.start`，abort 当前未完成上传并保留失败气泡；若 `turn.start` response 前断线，禁止自动重发。重连同步后按 `clientMessageId`/snapshot 判定；无法判定则显示“提交结果未知”，只允许同步确认。会话创建结果未知时禁止从该草稿继续创建或发送，避免重复 thread。

V1 只支持图片附件，一条消息最多 10 张；单文件和图片总大小均不得超过 Desktop 48 MiB 上限，route frame 按 1 MiB 上限分块。Desktop 另检查 base64、文字、JSON 与换行组成的完整 App Server 请求不超过 64 MiB，超限返回 `too_large` 并保留草稿。Desktop 将附件写入项目目录临时 spool，索引只在内存中；明确受理后删除，失败或结果未知最多保留 24 小时。图片转 base64 全局单并发。`turn.start` 只提交 attachment ID，不向 Mobile 或 Relay 暴露本地绝对路径。

### Action resolve

打开 action sheet 时再次确认 action 仍存在。提交后按钮进入不可重复点击状态；成功 response 或 snapshot 中 action 消失后显示完成。`already_resolved` 不是错误重试，而是“已在电脑或另一台手机处理”，关闭 sheet 并刷新。

`request_user_input` 当前按每题单个选项或一条自定义文本校验必填，并提交 `answers[questionId].answers=[value]`；approval 保持允许/拒绝兼容，不自造永久授权。多选和完整 provider approval 选项必须在 DTO 明确支持后再开放。

### Interrupt

点击后弹出轻确认，提交一次 `turn.interrupt`。response 不代表 Turn 已停止；直到 interrupted/completed/failed event 或 snapshot 才移除“正在停止”。断线时不自动重发 interrupt，重连后若仍 active 再允许用户操作。

### 权威快照与版本冲突

控制器使用 `hasSnapshot` 区分未加载状态与真实 revision 0。创建返回 ID 后先监听事件并等待 `conversation.read` 成功，再上传图片和发送；普通发送与会话设置修改都先同步快照。失败时保留草稿、图片和已创建 ID，重试不会重复创建。读请求绑定 Daemon/选择代次；同一控制器只保留一个进行中的 `conversation.read`，页面加载、发送前同步与事件刷新共同等待该权威结果，不使用缓存代替读取。事件在读取期间继续按 burst 合并，必要的后续刷新在当前请求完成后执行。成功读取不能因为另一调用者请求刷新而被判为失败；完成后清除共享请求，下一次发送重新读取。sequence 倒退的响应不更新正文或设置，退出页面或选择改变后不得写入旧结果。

`revision_conflict` 刷新状态并提示用户重试，不自动重发；计划确认继续使用原 plan ID/revision，冲突不能被误判为已提交。`snapshot_unstable` 是可重试读取错误，写入被阻止。`cursor_expired` 清除旧历史分页依据，成功刷新后重建最新 Timeline。业务 revision 与事件 sequence 的权威定义见 [Desktop 会话设计](../../../desktop/docs/json-rpc-and-actions/design.md)。

快照可新增 `capabilities.queue=false`；缺失仍按支持处理。不支持队列的运行中会话保留输入并提示等待任务结束，不将排队请求转为普通发送。读取兼容、空会话与错误降级的唯一规范见 [Desktop 控制设计](../../../desktop/docs/local-control/design.md)。

## 请求安全与重试表

| 类型 | 自动重试 | 断线后的处理 |
| --- | --- | --- |
| status/list/read/catalog/subscribe | 是，限前台且有退避 | 丢弃旧 response，重新同步 |
| pair join/auth/Noise handshake | 仅协议允许的全新 attempt/session | 旧 session 全部销毁 |
| conversation.create | 否 | 结果未知时重新同步项目列表，不自动创建第二个 thread |
| turn.start/conversation.queue.*/conversation.settings.update/conversation.rename/action.resolve/turn.interrupt | 否 | 若返回 `ambiguous_write` 或响应丢失，刷新 snapshot 对账；确认已执行后不再提示失败，也不自动重发 |
| attachment upload | 不续传旧 upload | 调用 abort 清理 `.part`，丢弃 attachment ID，用户触发整份重试 |
| push register | 可按更高 generation 重试 | 新 socket auth 后再注册 |

`route_ack.accepted` 只表示进入目标内存队列，`delivery_ack` 只表示 endpoint 收到 frame；两者都不能完成业务 promise。Mobile endpoint 按外层 message ID 关联 route ACK，拒绝 ACK 立即结束对应请求；断线、迟到或未知 ACK 不得污染新连接。只有匹配 request ID 的 E2EE response 或后续权威 snapshot/event 能决定业务状态。

## Push 与冷启动同步

Mobile 获取新 APNs/FCM token 后递增本地 generation，并由 host 直接交给 Go core，在 authenticated socket 上注册；logout 不存在，解除某 Relay origin 的最后一条 pairing或关闭通知时 unregister。provider token 不进入 Dart heap、日志或 analytics。

`action_required` 打开工作台并尝试定位关联对话，`turn_completed` 打开最近对话，`generic` 打开上次 Daemon/Agent 工作台；三者都先显示“正在从电脑同步”。因为通知不含 daemon/conversation ID，App 对所有已配对且可连接设备执行轻量 status/action sync，只有唯一匹配目标时才自动进入详情，否则停留工作台并展示待处理入口。推送丢失、延迟或被合并不影响下一次前台全量同步。

## 状态与持久化

- 持久化：identity、pairing/grant/scope、Relay origin、push generation、用户别名、`lastSelectedDaemonId` 和每个 Daemon 的 `lastSelectedAgentId`。
- 仅内存：pairing secret、socket、Noise/session key、request map、cursor/revision projection、会话正文、action 正文、附件字节。
- Desktop/Relay revoke 后立即删除 PairingRecord 和对应内存 projection；本地“删除设备”先尝试发送 revoke，离线时至少立即销毁本地 key/record，并明确提示服务器侧 edge 可能等待 Desktop 管理员撤销。

## 安全与失败处理

- malformed protobuf、错误 sender/recipient binding、过期 frame、sequence gap、decrypt/tag failure 关闭 session，不尝试容错解密。
- release build 证书校验不可关闭；不支持用户输入自签 CA。企业私有 CA 属于未来单独 threat model。
- server time 只用于校正 expiry 判断，不修改系统时钟；偏差过大显示“设备时间异常”并阻止配对/写操作。
- stable error code 决定交互；服务端 message 只进入可展开诊断，不直接作为主按钮文案或授权依据。

## 不变量与非目标

- 无已验证 grant 就没有持久 pairing；无完成 Noise handshake 就不发送 control JSON。
- 任一 session 每方向 sequence 单调；gap/tamper/revoke 必须重建而非继续。
- 不提供离线 outbox、自动重放写命令、exactly-once 承诺或通知内审批。`conversation.create` 只接受当前项目 ID，不接受 Mobile 提供 cwd；Desktop 必须使用 provider 项目目录解析出的可信主目录创建 thread，结果未知时不自动重放。
- V1 发布 Codex `projects` adapter 和 Claude Code `projects` adapter；Hermes `bots` 和其他 Provider 仅保留 capability/DTO 边界，不作为已实现能力。
- Mobile 不拥有 Relay schema 或 Desktop 业务 DTO 的平行副本。

## 代码归属

当前路径：`mobile/core/mobilecore/`、`relay/pkg/endpoint/{client,e2ee,protocol}/`、`mobile/app/pigeons/`、`mobile/app/lib/core/`、`mobile/app/android/`。权威 schema 与 control DTO 仍归属 Relay/Desktop。

## 验证方式

- 对 `gomobile` AAR/XCFramework 中的真实 core 跑 auth、pair proof/grant、Noise prologue/handshake/transport 的相同 golden vectors与 Relay/Desktop E2E，不能只测 host Go package。
- 模拟 duplicate/reorder/gap/expiry/tamper、late response、connection epoch race、route ACK 后掉线和 revoke。
- 验证上次 Daemon/Agent 恢复、删除后的 fallback、未选中 Daemon status probe、Agent 切换 generation、project 10 条分页、project conversation 15 条分页、recent 20 条和 project/conversation 全集搜索。
- 弱网 E2E 验证所有写操作不自动重复、断线轮询不会重复拨号，并验证 Relay 429 映射为 `rate_limited` 后按冷却窗口恢复；覆盖 `SessionOpen` route 拒绝、握手超时、旧 session handshake/data 迟到和启动 restore 首次失败后的重试；action 多端 race 正确归类 `already_resolved`。
- 48 MiB 边界、1 MiB route 分块、取消上传、图片 MIME 和跨 instance attachment 拒绝测试。
- 运行中输入、queue snapshot 恢复、add/edit/start/delete、当前草稿冲突、图片草稿恢复限制和结果不明对账测试。
- push cold/background/terminated/token rotation/permission denied 场景，确认点击必先同步且 payload/log 无业务正文。

## 文档更新触发

修改 frame/content type、canonical binding、pairing、scope、control method/DTO、cursor/revision、retry/idempotency、attachment、push metadata 或 revoke 行为时，必须更新本文档、Mobile 架构/交互、Relay wire/E2EE/push 和 Desktop control/connector 设计。

## 活动电脑与本机调用绑定

Go Core catalog 的 `activeDaemonId` 和 generation 为选择权威；Flutter 启动不使用 `lastDaemonId` 覆盖 catalog。旧 catalog 的活动项无效时 Core 按排序选择首个已启用电脑并由 host 持久化；全部禁用保持无活动项。重新启用且无有效活动项时选择该电脑并递增 generation，有有效项时不抢占。

Flutter 的工作台、项目分页和会话调用通过本机 `_wawTarget={daemonId,generation}` 绑定目标。Core 在捕获活动 client 的同一锁下校验，过期返回 `stale_selection`，发送前剥离此字段，不改变 Relay/Desktop 控制协议。迟到结果按选择代次丢弃；切换/启停/删除/保存失败后重新读取 Core catalog，不以旧 UI 快照猜测回滚目标。并发 catalog 操作完成后再次核对 Core 状态。

Core 串行协调 catalog 变更和探测；当前活动设备复用自己的 client 探测，禁止第二次登录挤掉 Relay 连接。Android host 将导出旧 catalog、执行变更、加密保存及失败恢复作为同一串行事务。

排序的 Pigeon/Flutter 接口仍接收 `List<String>`，Kotlin 编码为 JSON 数组字符串调用 Go `ReorderPairings(idsJSON string) string`。参数必须为全部 catalog ID 的无重复排列，非法输入不修改顺序。生成脚本使用 javap 验证真实 AAR 的 `reorderPairings(String)`；AAR 为忽略的构建产物，Pigeon 生成文件无协议变更。

## Codex infrastructure requests

会话详情的项目归属由 Desktop 在官方 thread/read 缺少 projectId 时根据 provider cwd/environments 与项目主目录恢复；Mobile 无需修改 DTO 或自行猜测目录。官方 idle 状态保持空闲，消息 item 的完成状态不能代替 Turn 生命周期。

客户端基础设施请求与用户审批的分类由 [Desktop JSON-RPC 设计](../../../desktop/docs/json-rpc-and-actions/design.md#pending-actions) 所有。时间读取由 daemon 本地响应，认证刷新、attestation 和动态工具调用由 Desktop 处理；这些请求不进入 Mobile action/needsAttention，也不能由手机 action.resolve 回答。此行为不改变 mobile-control-v1 DTO、Pigeon 或 Relay protobuf。消息阶段 partial_answer 不表示 Turn 结束；图像生成 item 缺少 result 时继续显示摘要。
