# Mobile protocol and synchronization

> 状态：Mobile Go Core 已复用 Relay endpoint 完成 pair/auth/Noise/control 和 Android 安全恢复路径；Desktop 已发布 normalized summary、分页 timeline 与大内容引用，Mobile 会话控制器已支持分页、局部重试和 gap snapshot 恢复。完整 action/catalog DTO、多 pairing reducer和全量真机 E2E 尚未完成。

## 目标与范围

定义 Mobile 如何复用现有 pairing、WSS、Noise 和 Desktop control contract，并把不可靠在线路由转换成不会误报成功、不会盲目重放写操作的 UI 状态。

## 外部与内部接口

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
| `conversation.create` | 首次发送时在当前项目创建 Codex thread | `conversation.create` |
| `turn.start` | 继续已有 thread或响应已完成计划 | `turn.start`, `plan.respond` |
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

`agent.list` 的 Codex adapter 可由现有 `instance.list` 投影；Mobile contract 下的 `project.list` 由 Desktop 根据 provider 项目和完整 thread metadata 聚合，只返回有对话的项目，并以项目最近一条对话的 `updatedAt desc, projectId desc` 排序。`ProjectSummary.updatedAt` 表示该最近对话时间；cursor 以更新时间和项目 ID 锚定，不能因并列时间重复或漏项。不能由 Mobile 对最近 20 条记录按路径字符串猜测项目。`conversation.list` 的 `projectId`、`query`、`limit` 和 cursor 由 Desktop 执行，排序固定为 `updatedAt desc, id desc`，同一 cursor 下不能因并列时间重复或漏项。

Agent 声明 `navigationKind = projects | bots | none`。Codex 为 `projects`；Hermes 未来为 `bots`，并在实现时增加同属 `conversation.read` scope 的 `bot.list`。未适配的 Claude Code、OpenCode、Pi 等可以由 `agent.list` 返回 discovery metadata，但 `supported=false`，Mobile 不调用项目/对话写接口。

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

1. 对上次选中的 Daemon建立安全会话并调用 `system.status` / `agent.list`；恢复该 Daemon 上次选中的 Agent，记录不存在时使用返回列表中的第一个 Agent，不根据 ready/error 状态自动改选其他 Agent。
2. 独立保留完整 Agent 列表与当前选择，再对选中 Agent 并发调用 `project.list(limit=10)`、`conversation.list(limit=20)` 与 `action.list`。`navigationKind=bots` 时把 project call 替换为 `bot.list`；Codex-first V1 不进入该分支。当前 Agent 的项目或对话同步失败时，只清空该 Agent 的内容并显示局部重试状态，Agent selector 必须继续保留；切换 Agent 时立即更新选择并独立同步新 Agent，前一个 Agent 的迟到结果或失败不得覆盖当前 Agent。
3. 点击项目后调用 `conversation.list(projectId, limit=15)`；“显示更多”只使用 response next cursor，不用 offset 猜页。
4. 搜索时分别调用 `project.list(query, limit, cursor)` 与 `conversation.list(query, limit, cursor)`，搜索服务端全集而不是当前 10/20 条内存列表。
5. 打开会话时调用 `conversation.read(limit=20)` 获取 metadata、revision/sequence 和最近 timeline 页；页面按 ID 去重并倒转为正序展示。滚动到顶部用 opaque `nextCursor` 调用 `conversation.timeline.list`，不使用 offset。
6. EventSink 中的 event 只作为失效提示：约 150 ms 内合并 burst，同一时刻只允许一个 conversation refresh，期间到达新事件则再刷新一次；sequence 低于当前 snapshot 的迟到 response 不得覆盖 UI。gap、过期 cursor 或 `snapshot=true` 触发重新 `conversation.read/subscribe`。
7. `revision` 随 snapshot/event 更新；`turn.start` 必须提交最后已确认的 `expectedRevision`。
8. `conversation.read` 可返回可选 `planConfirmation={planItemId,turnId,revision}`。仅把该权威字段作为计划确认入口，不能从 Markdown 或历史 Plan item 推断；提交 `plan.respond` 后继续通过 snapshot/event 确认最终状态，不因超时自动重复写入。

App 前台对未选中 Daemon 以有界短连接依次执行 status probe，更新下拉列表的 `connecting/online/offline + checkedAt` 后关闭；只有选中 Daemon 保持前台 session。切换 Daemon/Agent/project/thread 或 search query 时取消旧 read request，并用 selection generation 丢弃迟到 response。实时 event 可以更新项目/对话摘要，但只有属于当前 Agent/thread 且 sequence 连续的 event 才能写入详情 reducer。

### 规范化 DTO 当前边界与发布门槛

`mobile-control-v1` envelope 会回显 contract，未知 contract 返回 `unsupported_contract`。Mobile-facing target 参数统一使用 `agentId`，Desktop 只在 provider 边界内转换为 `instanceId`；附件上传会先绑定该实例，再把 `instanceId` 从 provider 参数中移除。`agent.list`、`project.list`、`conversation.list` 和 conversation timeline 已输出稳定 DTO；project list 不暴露 root，conversation list 不暴露 preview；catalog 与 action 仍有 provider-shaped payload。

`conversation.read` 组合 metadata-only `thread/read` 与 `thread/items/list(sortDirection=desc)`，默认 20、最大 50。snapshot 包含 metadata、revision、sequence、timeline 和 opaque `nextCursor`。`TimelineItem` 固定为 id、可选 turnId、kind、status 与 blocks，不透传 provider JSON。Plan item 使用单个 `kind=plan` block，daemon 保留原始 Markdown；真正超过 5 个源文件行时普通 Timeline response 只返回前 5 行，并为完整正文返回 `contentRef` 与 `totalBytes`，不生成 title/summary。Plan 和其他大 block 通过 `conversation.content.read` 按 128 KiB 分块读取；引用过期后，Flutter 使用私有的 turnId/itemId/blockIndex 调用 `conversation.content.resolve`，daemon 在该 turn 内分页查找 item 并重新签发引用，找不到时返回 `content_not_found`，不要求刷新或丢弃已加载旧页。

`hookPrompt` 不进入 DTO；function/tool、协作、搜索、图片、sleep、review 和 compaction item 只返回单个 `kind=summary` block。该 block 的 `summaryKind` 是原始类型，`summaryValue` 是脱敏、单行且不超过 512 UTF-8 字节的值，`text` 是语言无关 fallback；完整句子和 status 由 Flutter ARB 本地化。summary 不包含 arguments/output/result/error/path/prompt，不生成 `contentRef`；commandExecution 只返回 command block。过滤后可见 item 不足请求数量时 daemon 沿 provider cursor 补读后续页面，最多 8 页。其他单 block 超过 64 KiB 时内联 UTF-8 前 16 KiB，并用 15 分钟滑动过期的 `contentRef` 分块读取。项目下对话列表优先按 `projectId` 查询；Desktop 若发现历史线程项目关联未迁移，会按项目根目录 `cwd` 回退查询。Desktop control response 不超过 512 KiB；页面超预算时以相同 cursor 将 limit 逐次减半。

`conversation.subscribe` 保留为恢复用的 cursor/snapshot 接口；Android 正常路径由 Relay unsolicited event 主动推送 metadata-only event，Go Core 通过 EventSink/EventChannel 转交 Flutter。Desktop 先在 100 ms 内按 instance/thread 合并普通流式失效提示，并分别保留 action 与 terminal 类别；endpoint 只转发 `type=event`，无 sink 时按到达顺序保留最近 1000 条，迟到/未知 response 不进入事件流。连接生命周期另发 `type=transport` 的 `online/disconnected/revoked/closed`。Flutter 在 disconnected、stream error/done 时立即把会话页连接点改为离线、禁用发送并启动 catch-up，每 2 秒轮询；online 或成功恢复读时恢复安全连接状态，停止轮询并再 catch-up 一次，revoked/closed 停止恢复。Flutter 对收到的 event burst 再做约 150 ms 合并刷新，旧 snapshot 不覆盖新 sequence，刷新最新页时保留用户已加载的旧页。当前尚未完成 iOS Go Core/事件宿主接入的平台直接使用恢复路径。事件和轮询都不携带 provider 正文；当客户端游标过旧或待追赶事件超过 Desktop 的有界窗口时，服务端返回 `snapshot=true`。

- `DaemonSummary`：由本地 pairing + live probe 组合的 id、display name、connection state、checked time；
- `AgentSummary`：agent id、provider (`codex` 等)、display name、state、supported、navigation kind、capabilities、active turn/pending action count、last error；
- `ProjectSummary`：稳定 project id、name、conversation/running counts、updated time；
- `BotSummary`：为 Hermes 预留的稳定 bot id/name/state/capabilities，Codex 不返回；
- `ConversationSummary`：thread id、title、project id/label、status、`isRunning`、updated time、revision；
- `PendingAction`：id、kind (`approval`/`question`)、title、explanation、choices/input schema、created time；
- `Catalog`：稳定 ID/label/capability/availability，不把 provider enum 当 UI 文案；
- `Page<T>`：items、next cursor、has more；project 默认请求 10、项目对话 15、最近对话 20，数量是 client request 而非 server hard limit；
- 稳定 error code 包含 `payload_too_large`、`response_too_large`、`content_ref_expired`、`content_not_found`、`peer_offline`、`ambiguous_write`、`queue_full`、`request_timeout`，并保留 busy、revision conflict、already resolved 与 forbidden。

Mobile 产品页面只消费稳定 timeline DTO；尚未规范化的 action/catalog 仍按预发布边界处理。

## 写操作语义

### Turn start

1. 本地检查 `online`、`turn.start` scope、conversation idle、无 blocking action。
2. 一次发送先生成唯一 `clientMessageId`；Mobile 每条消息最多接受 10 张图片，按用户选择顺序逐张执行 begin/chunk/commit，所有图片共用该 ID。begin 同时传递当前会话 metadata 中的 `projectId`、conversation ID、MIME、原始文件名和大小。显示逐文件进度，分块大小为 256 KiB。
3. 上传失败调用 `attachment.abort` 清理 Desktop 的 `.part` 文件，不提交 Turn；用户重试时重新生成消息 ID并上传整份文件。
4. response 成功只显示“已提交”；收到 matching turn event/snapshot 后才进入“运行中”。
5. 全部图片 commit 后，`turn.start.attachmentIds` 按选择顺序一次提交 0–10 个 ID。任一图片上传失败时不调用 `turn.start`，abort 当前未完成上传并保留 Mobile 内存草稿；若 `turn.start` response 前断线，禁止自动重发。重连同步后按 `clientMessageId`/snapshot 判定；无法判定则显示“提交结果未知，请先在电脑端确认”。

V1 只支持图片附件，一条消息最多 10 张；单文件和图片总大小均不得超过 Desktop 48 MiB 上限，并预留 base64/1 MiB route frame 的分块余量。Desktop 会把附件保存到项目目录而不是数据库；`turn.start` 只提交 attachment ID，不向 Mobile 或 Relay 暴露本地绝对路径。压缩/缩放必须在用户确认的预览基础上进行，不能改变原图后仍显示原始大小。

### Action resolve

打开 action sheet 时再次确认 action 仍存在。提交后按钮进入不可重复点击状态；成功 response 或 snapshot 中 action 消失后显示完成。`already_resolved` 不是错误重试，而是“已在电脑或另一台手机处理”，关闭 sheet 并刷新。

问题回答严格按 DTO schema 校验必填/单选/多选/文本；approval 必须清楚区分“仅本次允许”“拒绝”等真实 provider 选项，不自造永久授权。

### Interrupt

点击后弹出轻确认，提交一次 `turn.interrupt`。response 不代表 Turn 已停止；直到 interrupted/completed/failed event 或 snapshot 才移除“正在停止”。断线时不自动重发 interrupt，重连后若仍 active 再允许用户操作。

## 请求安全与重试表

| 类型 | 自动重试 | 断线后的处理 |
| --- | --- | --- |
| status/list/read/catalog/subscribe | 是，限前台且有退避 | 丢弃旧 response，重新同步 |
| pair join/auth/Noise handshake | 仅协议允许的全新 attempt/session | 旧 session 全部销毁 |
| conversation.create | 否 | 结果未知时重新同步项目列表，不自动创建第二个 thread |
| turn.start/action.resolve/turn.interrupt | 否 | 若返回 `ambiguous_write` 或响应丢失，刷新 snapshot 对账；确认已执行后不再提示失败，也不自动重发 |
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
- 不提供离线 outbox、自动重放写命令、exactly-once 承诺或通知内审批。`conversation.create` 只接受当前项目 ID，结果未知时不自动重放。
- V1 只发布 Codex `projects` adapter；Hermes `bots` 和其他 Provider 仅保留 capability/DTO 边界，不作为已实现能力。
- Mobile 不拥有 Relay schema 或 Desktop 业务 DTO 的平行副本。

## 代码归属

当前路径：`mobile/core/mobilecore/`、`relay/pkg/endpoint/{client,e2ee,protocol}/`、`mobile/app/pigeons/`、`mobile/app/lib/core/`、`mobile/app/android/`。权威 schema 与 control DTO 仍归属 Relay/Desktop。

## 验证方式

- 对 `gomobile` AAR/XCFramework 中的真实 core 跑 auth、pair proof/grant、Noise prologue/handshake/transport 的相同 golden vectors与 Relay/Desktop E2E，不能只测 host Go package。
- 模拟 duplicate/reorder/gap/expiry/tamper、late response、connection epoch race、route ACK 后掉线和 revoke。
- 验证上次 Daemon/Agent 恢复、删除后的 fallback、未选中 Daemon status probe、Agent 切换 generation、project 10 条分页、project conversation 15 条分页、recent 20 条和 project/conversation 全集搜索。
- 弱网 E2E 验证所有写操作不自动重复、断线轮询不会重复拨号，并验证 Relay 429 映射为 `rate_limited` 后按冷却窗口恢复；覆盖 `SessionOpen` route 拒绝、握手超时、旧 session handshake/data 迟到和启动 restore 首次失败后的重试；action 多端 race 正确归类 `already_resolved`。
- 48 MiB 边界、1 MiB route 分块、取消上传、图片 MIME 和跨 instance attachment 拒绝测试。
- push cold/background/terminated/token rotation/permission denied 场景，确认点击必先同步且 payload/log 无业务正文。

## 文档更新触发

修改 frame/content type、canonical binding、pairing、scope、control method/DTO、cursor/revision、retry/idempotency、attachment、push metadata 或 revoke 行为时，必须更新本文档、Mobile 架构/交互、Relay wire/E2EE/push 和 Desktop control/connector 设计。
