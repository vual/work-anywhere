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
- remote.pair.start/list/resolve/cancel/revoke
- remote.reconnect

remote.configure 接受合法的 HTTP 或 HTTPS origin，IP 和域名均可，例如 `http://172.20.31.206:18080`、`http://relay.example:18080`。HTTP 使用明文 WS，不提供传输层机密性和完整性；公网部署仍建议使用 HTTPS/WSS。remote.enroll 的一次性 token 只用于当前 HTTP(S) request，不持久化；注册生成的新 device identity 会立即关闭旧身份的 WebSocket，待 daemon 使用新身份重新连接后才允许继续配对或发送控制帧。管理页提供 Relay 地址、设备名称、enrollment token、注册/重连操作，以及生成、复制和真实 QR 展示包含 secret 的 `waw://pair` URI；QR 由受 CSRF 保护的本机 POST 端点生成并标记 `no-store`，同时保留复制链接兜底。待 Mobile proof 到达后可在页面直接 approve/reject，已提交的决定显示为等待 Relay 确认，已授权设备可直接 revoke。CLI 仍保留全部 remote 命令用于自动化和诊断。页面仅校验 URI并清零临时 secret buffer，不持久化配对 secret。

## Identity, pairing, and storage

首次 enroll 生成 Ed25519 identity 与 X25519 Noise static key。整个 remote state（配置、私钥、公钥、device ID 和 pairings）写入 Desktop encrypted settings；SQLite row 使用 AES-256-GCM，master key 由 Windows current-user DPAPI 保护。

pairing secret 仅在内存，最长五分钟；pairing 保存 peer keys、capability hash 和 scopes。daemon 验证 QR HMAC 后签署 grant，收到 Relay result 时再次要求 expiry、signature 和 scopes 与本机批准值完全一致。revoked pairing 从本地 state 删除并关闭相关 session。

当前没有“删除远程身份/轮换 key”命令；需重新建立身份的恢复流程尚未产品化。新 pairing 的默认 scopes 还包含 `conversation.create`，用于项目内创建 Codex thread；为兼容已授权设备，已有 `turn.start` scope 也允许该方法。

## Remote request adapter

Desktop 解密并验证 desktop-control-json-v1 后，仅允许：

- system.status, instance.list/get
- agent.list, project.list
- conversation.list/read/timeline.list/content.read/subscribe/create
- turn.start/interrupt
- action.list/resolve
- catalog.models/permissionProfiles
- attachment.begin/chunk/commit/abort

每个方法映射到 pairing scope，再调用与本机 CLI 相同的 daemon manager。未知方法、system.shutdown、traffic、UI、install/uninstall 和 force-stop fail closed。system.status 响应会移除 remote 字段，避免向 Mobile 回显其他 pairing metadata。

现有 Desktop 不变量仍由 manager/supervisor 执行：idle 才开始新 Turn、clientMessageId/expectedRevision、action first-valid-wins、App Server/session 权威等。Relay ACK 不代表业务完成。Mobile 时间线响应在加密前已受 512 KiB 上限保护，从而为 Relay 的 1 MiB ciphertext 上限保留余量。

## Events, push, and reconnect

remote Service 订阅 daemon event bus。action 相关事件要求 action.read，其他会话事件要求 conversation.read；每个 active Noise session 单独加密发送。action request 和 Turn complete/fail/interrupt 生成 metadata-only notify hint。

socket 断开会销毁内存 Noise sessions，不中断 Desktop Turn。重连后 Mobile 必须重新握手，并使用 conversation read/sequence/revision 获取 snapshot 或增量；同一 pairing 的新 session 会替换 daemon 内存中的旧 session，Mobile 也会丢弃跨 session 的迟到 frame，避免旧密文被投递到新 WebSocket 后触发解密断线。Relay 不保存丢失 event。daemon/PC 离线时 Mobile 写命令立即失败。

## Security and failure handling

- production URL 必须 HTTPS/WSS，不提供跳过证书校验。
- pairing secret、enrollment token、private key、ciphertext 和 control body不写结构化日志。
- malformed JSON、Noise tamper、binding/sequence/expiry 或 endpoint mismatch 会拒绝 frame；无效 control request 关闭 session。旧 session 的迟到 frame 不会迁移到新 session。Relay route ACK 明确拒绝配对决定时，daemon 清除本地 submitted 状态并允许重新提交；response/event 的拒绝 ACK 只记录 kind 和 error code，业务消息不自动重发。发送前若发现本地 device identity 与当前 WebSocket 身份不一致，则拒绝发送并要求重连。
- socket writes 串行化并有 10 秒 timeout；失败只影响 remote connector。
- 当前没有独立 attachment content type、远程 traffic capture 或 Mobile 离线 queue。

## 不变量与非目标

connector 是 daemon 的附加 frontend，不成为 App Server owner，不创建桌面聊天 UI，也不改变 daemon 登录自启动策略。本功能不实现正式 Mobile UI、账号系统、多 Relay 节点或离线命令。

## 代码归属

- connector/pairing/crypto/adapter：internal/remote/
- manager integration/control methods：internal/daemon/daemon.go
- encrypted settings：internal/store/, internal/platform/secrets/
- schema generated copy：internal/remote/gen/

Relay schema 主归属为 [Relay wire protocol](../../../relay/docs/wire-protocol/design.md)。

## 验证方式

单元测试覆盖 URL、identity persistence、scope allowlist、QR/canonical inputs、normalized Mobile summary、旧 session 替换和 crypto；Relay server tests 覆盖 auth/pair/route。人工 E2E 使用 browser simulator 完成 enroll、明确批准、Noise system.status、forbidden system.shutdown 和 revoke。仍需 Android 真机 Mobile、多设备 action race、图片 Turn 和长时间断网/恢复测试。

## 文档更新触发

修改 remote 配置/方法、device key、Relay protocol、pairing、scope、事件过滤、reconnect、push hint、存储或管理入口时，必须更新本文档以及 Desktop local-control/security 和 Relay 对应设计。
