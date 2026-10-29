# Desktop outbound Relay connector

> 状态：Desktop Relay connector、pairing、Noise session 和 remote control adapter 已实现。

## 目标与范围

Desktop daemon 主动连接 Relay，把已授权 Mobile 的 E2EE 请求交给现有 control manager，并把规范化事件加密返回。Relay 永远不能连接本机 Named Pipe、loopback 管理页或 Codex App Server stdio。

## 当前架构

    Codex App Server ◄─► daemon manager/control
                             ▲
                             ▼
    desktop/internal/remote ◄═ Noise KK ═► Relay ◄═ Noise KK ═► Mobile

每个 Windows 用户 daemon 创建一个 remote Service，共享 daemon 管理的所有 Codex logical instances。connector 默认未配置；enroll 后保存 device identity 并保持一个出站 WSS。断线采用 1–30 秒 capped exponential backoff；Relay 错误不重启 Bridge/App Server。

## Configuration and management

本机 control/CLI 和嵌入式管理页均已实现：

- remote.status
- remote.configure
- remote.enroll
- remote.register
- remote.pair.start/list/resolve/cancel/revoke
- remote.reconnect

生成的 `waw://pair` URI payload 还携带配对时 daemon 所在电脑的 `computerName`，供 Mobile 在首次配对后保存显示名称；该名称不是后续连接状态的动态来源。

remote.configure 接受合法的 HTTP 或 HTTPS origin，IP 和域名均可，例如 `http://172.20.31.206:18080`、`http://relay.example:18080`。HTTP 使用明文 WS，不提供传输层机密性和完整性；公网部署仍建议使用 HTTPS/WSS。remote.enroll 的一次性 token 只用于当前 HTTP(S) request，不持久化；注册生成的新 device identity 会立即关闭旧身份的 WebSocket，待 daemon 使用新身份重新连接后才允许继续配对或发送控制帧。管理页提供 Relay 地址、设备名称、enrollment token、注册/重连操作，以及生成、复制和真实 QR 展示包含 secret 的 `waw://pair` URI；QR 由受 CSRF 保护的本机 POST 端点生成并标记 `no-store`，同时保留复制链接兜底。待 Mobile proof 到达后可在页面直接 approve/reject，已提交的决定显示为等待 Relay 确认，已授权设备可直接 revoke。CLI 仍保留全部 remote 命令用于自动化和诊断。页面仅校验 URI并清零临时 secret buffer，不持久化配对 secret。

## Identity, pairing, and storage

首次注册生成并先安全保存 Ed25519 identity 与 X25519 Noise static key。整个 remote state（配置、私钥、公钥、device ID 和 pairings）写入 `%USERPROFILE%\.work-anywhere\config.json`；每个设置值由 Windows current-user DPAPI 保护后以 Base64 保存。

pairing secret 仅在内存，最长五分钟；pairing 保存 peer keys、capability hash 和 scopes。daemon 验证 QR HMAC 后签署 grant，收到 Relay result 时再次要求 expiry、signature 和 scopes 与本机批准值完全一致。revoked pairing 从本地 state 删除并关闭相关 session。

本机管理页提供明确确认的远程身份重置；CLI 使用 `remote configure <origin> --reset-identity`，本机 control 使用 `remote.configure.resetIdentity`。重置清除全部远程密钥、device ID、手机配对和内存 session，随后重新登记并扫码。切换规范化 origin 时已有身份必须显式重置，只保存一个 Relay；普通升级/停用不删除身份。密钥不完整或公私钥不匹配时停止连接并要求本机处理，不自动生成替代身份。新 pairing 的默认 scopes 还包含 `conversation.create`，用于项目内创建 Codex thread；为兼容已授权设备，已有 `turn.start` scope 也允许该方法。

启用且无 device ID 时，remote Service 串行读取注册策略并执行 [Relay 自助注册协议](../../../relay/docs/daemon-enrollment/design.md)。先安全持久化密钥，再提交签名；响应或本地 ID 保存失败后用同一密钥恢复原 ID。invite 提示邀请码，旧 Relay 明确显示需要升级，允许用户主动执行 remote.enroll，不自动降级。状态包含 phase、registrationMode、lastErrorCode、nextRetryAt；网络/5xx 使用 1–30 秒 capped exponential backoff 和随机抖动，429 遵守 Retry-After，永久身份/协议错误暂停。邀请码不持久化。配置 generation 与取消 context 防止旧注册/连接写回；加载旧配置保留身份和配对。

配置提交先隔离配对操作、入站 frame 和事件转发，取消旧注册/连接、关闭 socket、清理临时配对与 Noise session，再原子保存新配置。保存失败保留旧配置、密钥和已授权配对，但旧任务仍失效并唤醒 Service 重新连接；旧 socket 的迟到 frame 和事件不得操作新配置。

## Remote request adapter

Desktop 解密并验证 desktop-control-json-v1 后，仅允许：

- system.status, instance.list/get
- agent.list, project.list
- conversation.list/read/timeline.list/content.read/subscribe/create
- turn.start/interrupt、plan.respond、conversation.settings.update、conversation.queue.add/update/delete/start
- action.list/resolve
- catalog.models/permissionProfiles
- attachment.begin/chunk/commit/abort

每个方法映射到 pairing scope，再调用与本机 CLI 相同的 daemon manager。未知方法、system.shutdown、traffic、UI、install/uninstall 和 force-stop fail closed。system.status 响应会移除 remote 字段，避免向 Mobile 回显其他 pairing metadata。

现有 Desktop 不变量仍由 manager/supervisor 执行：目标 thread idle 才开始新 Turn，目标 thread idle 或 active 且无阻塞 action 时可修改后续 Turn 设置；同时保持 clientMessageId/expectedRevision、action first-valid-wins、App Server/session 权威等。`conversation.settings.update`、`plan.respond` 和全部 `conversation.queue.*` 方法都复用 `turn.start` scope；队列新增与立即引导允许在目标 thread 运行时执行，但等待阻塞 action 时仍拒绝。同一 Agent 的其他 thread 活跃时仍可向空闲 thread 开始 Turn。Relay ACK 不代表业务完成。Mobile 时间线响应在加密前已受 512 KiB 上限保护，从而为 Relay 的 1 MiB ciphertext 上限保留余量。

## Events, push, and reconnect

remote Service 订阅 daemon event bus。可识别的文字、计划、推理、命令和文件输出 delta 在加密前按语义批量形成 `conversation.delta`；key、Turn、item 或 kind 变化以及审批、提问、队列、item 完成和终态事件会先 flush。时间阈值为 200–250 ms，批次上限按内容类型为 2–8 KiB，硬上限 32 KiB；聚合器最多保留 128 个活跃会话流和 8 MiB。未知事件继续发 `conversation.invalidated`。

App Server 事件处理只归一化并入队，不执行配置写入、Noise 加密或 Relay 网络发送。每个 Mobile Noise session 有独立高低优先级队列与 writer，各队列 1 MiB；response、审批和终态走高优先级，delta 走低优先级，Noise sequence 由单 writer 串行生成。低队列溢出降级为一次会话失效提示，高队列溢出关闭 session。事件订阅因慢消费者关闭时立即重新订阅并发送失效提示。action request 和 Turn complete/fail/interrupt 仍生成 metadata-only notify hint。

socket 断开会销毁内存 Noise sessions，不中断 Desktop Turn。重连后 Mobile 必须重新握手，并使用 conversation read/sequence/revision 获取 snapshot 或增量；同一 pairing 的新 session 会替换 daemon 内存中的旧 session，Mobile 也会丢弃跨 session 的迟到 frame，避免旧密文被投递到新 WebSocket 后触发解密断线。若 Relay 对某个 response/event 返回 `peer_offline`，daemon 立即移除该消息所属 Noise session 并清理它的待确认投递，不再向离线 Mobile 持续发送流式事件。Relay 不保存丢失 event。daemon/PC 离线时 Mobile 写命令立即失败。

## Security and failure handling

- 公网部署应使用 HTTPS/WSS，不提供跳过证书校验；保留显式 HTTP/WS 开发配置。
- pairing secret、enrollment token、private key、ciphertext 和 control body不写结构化日志。
- malformed JSON、Noise tamper、binding/sequence/expiry 或 endpoint mismatch 会拒绝 frame；无效 control request 关闭 session。旧 session 的迟到 frame 不会迁移到新 session。Relay route ACK 明确拒绝配对决定时，daemon 清除本地 submitted 状态并允许重新提交；response/event 的拒绝 ACK 只记录 kind 和 error code，业务消息不自动重发，其中 `peer_offline` 还会使对应 session 失效以阻止后续事件风暴。发送前若发现本地 device identity 与当前 WebSocket 身份不一致，则拒绝发送并要求重连。
- socket writes 串行化并有 10 秒 timeout；失败只影响 remote connector。
- 当前没有独立 attachment content type、远程 traffic capture 或 Mobile 离线 queue。

## 不变量与非目标

connector 是 daemon 的附加 frontend，不成为 App Server owner，不创建桌面聊天 UI，也不改变 daemon 登录自启动策略。本功能不实现正式 Mobile UI、账号系统、多 Relay 节点或离线命令。

## 代码归属

- connector/pairing/crypto/adapter：internal/remote/
- manager integration/control methods：internal/daemon/daemon.go
- encrypted settings：internal/config/, internal/platform/secrets/
- schema generated copy：internal/remote/gen/

Relay schema 主归属为 [Relay wire protocol](../../../relay/docs/wire-protocol/design.md)。

## 验证方式

单元测试覆盖 URL、identity persistence、scope allowlist、QR/canonical inputs、normalized Mobile summary、旧 session 替换和 crypto；Relay server tests 覆盖 auth/pair/route。人工 E2E 使用 browser simulator 完成 enroll、明确批准、Noise system.status、forbidden system.shutdown 和 revoke。仍需 Android 真机 Mobile、多设备 action race、图片 Turn 和长时间断网/恢复测试。

## 文档更新触发

修改 remote 配置/方法、device key、Relay protocol、pairing、scope、事件过滤、reconnect、push hint、存储或管理入口时，必须更新本文档以及 Desktop local-control/security 和 Relay 对应设计。

## 默认 scope 契约

默认授权使用 [Relay 配对规范](../../../relay/docs/identity-and-pairing/design.md#authorization-scopes) 的九项集合。CLI 与管理页均省略 scopes 以复用同一默认值；显式 API 子集仍校验。旧 pairing 的 `turn.start` 保持创建会话的兼容授权。应先升级 Relay，再升级 Desktop/Mobile；已提交授权不自动重签或降级。Relay 契约测试读取 Desktop 的实际默认值并完成签名配对握手。
