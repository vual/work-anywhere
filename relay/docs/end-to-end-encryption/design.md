# End-to-end encrypted remote sessions

> 状态：Go reference implementation 已完成；正式 Mobile 接入和独立安全审查尚未完成。

## Goal and scope

daemon 与 Mobile 之间的控制请求、响应、事件和附件 chunk 必须端到端保密并防篡改。Relay 只路由 ciphertext，即使 Relay 应用或数据库被读取，也不能恢复 Codex 内容。

## Session establishment

每条 active pairing 使用 `Noise_KK_25519_ChaChaPoly_SHA256` 建立在线 session。KK 适用于双方已通过 pairing grant 获得对方 static X25519 key 的场景：

1. Mobile 发起 `session.open`，携带 pairing ID、随机 session ID 和第一条 Noise handshake message。
2. Relay 验证 active pairing 后原样转给在线 daemon。
3. daemon 验证 prologue、peer static key 和本地 scope，返回第二条 handshake message。
4. 双方完成 mutual authentication，得到独立发送/接收 transport cipher state。
5. session key 只保存在 endpoint 内存；任一 WSS 断开、sequence gap、decrypt failure、revocation 或显式 close 都销毁 key。

Noise prologue 固定绑定：`WAW relay wire major`、pairing ID、daemon ID、Mobile ID、session ID 和双方 capability hash，避免跨版本、跨配对或 capability downgrade 复用握手。

## Transport payload

每个 E2EE data frame 内部包含：

- session ID 和单调 direction sequence
- request/response/event 类型
- content type（V1 为 `desktop-control-json-v1` 或 attachment chunk）
- created/expiry
- Desktop control payload bytes

outer header 只保留路由所需的 sender、recipient、pairing/session ID、message ID、ciphertext length、expiry、traffic class 和可选通用 notification hint。接收端解密后比较 inner routing binding，发现不一致立即关闭 session。

加密 payload 禁用 WebSocket/per-message compression：密文不可有效压缩，并避免将 secret 与攻击者可控输入共同压缩产生侧信道。

## Key storage and lifecycle

- Desktop static X25519 private key 保存在由 DPAPI master key 保护的加密 settings；开发 simulator 只保存在进程内。正式 Mobile 必须使用 Keychain/Keystore。
- Noise ephemeral/transport key 只在内存，session 结束后清零可控 buffer。
- V1 不持久化 session ticket 或业务 ciphertext，因此每次重连执行完整握手并获得前向安全的新 session。
- static key 轮换通过撤销并重新配对；后续若引入 signed rekey 必须更新协议 major/minor 和 threat model。

## Relay-visible notification metadata

为触发 APNs/FCM，daemon 可提交有限 hint：`generic`、`action_required`、`turn_completed`。Relay 不接收 conversation ID、Prompt、action 文本或结果。统一降级为 `generic` 的隐私模式尚未实现。Relay 本身可以制造或丢弃 push，因此 E2EE 不承诺抵抗 Relay 的通知 spam/denial-of-service。

## Security properties

- Confidentiality/integrity：Noise transport cipher 保护 payload。
- Mutual endpoint authentication：pairing grant 绑定的 static keys 参与 KK handshake。
- Forward secrecy：每个在线 session 使用新的 ephemeral handshake state；Relay 不保存 session key。
- Replay/order：每方向 sequence 单调，duplicate/gap 导致拒绝或 session 重建。
- Endpoint compromise：被攻陷 endpoint 可读取该端当前数据；E2EE 不保护终端自身。

## Failure handling

- decrypt、authentication、sequence 或 transcript 失败：不重试当前 ciphertext，关闭 session，记录无 payload 的安全事件。
- WSS 重连：重新认证设备、重新 Noise handshake，再按 conversation cursor 从 daemon 同步。
- peer 离线：不创建可离线解密的 mailbox；发起方收到 `peer_offline`。
- 协议版本不匹配：在发送业务 payload 前失败，并返回支持版本范围。

## Invariants and non-goals

- private/static/session key 不进入 Relay。
- 不自创底层密码 primitive；实现必须使用成熟 Noise library，并锁定算法 suite。
- V1 不支持离线 E2EE mailbox、群组密钥、多方会话、云端 key recovery 或导出私钥。
- 元数据隐藏、流量填充和抗全局流量分析不属于 V1。

## Code ownership

- Relay Go reference：`relay/internal/e2ee/`；服务端只路由 handshake/ciphertext，不解密。
- Desktop：`desktop/internal/remote/`，长期 key 通过 Desktop 加密 settings 保存。
- 开发 Mobile reference：`relay/internal/devmobile/`；正式 Mobile crypto adapter 尚未实现。

## Verification

- Relay reference、Desktop 和 dev simulator 的 round-trip/tamper tests 使用相同 canonical binding；跨语言 Mobile golden vectors 尚未建立。
- transcript/capability/key 被任意修改时握手失败。
- replay、reorder、nonce exhaustion、reconnect 和 revocation 测试。
- Relay 内存/日志/数据库 fixture 扫描不得出现测试明文。
- 正式 Mobile 发布前完成独立 threat-model review；不得用“TLS 已加密”替代 E2EE 测试。

## Documentation update triggers

修改 Noise pattern/suite、prologue、key binding、sequence、inner payload、通知 metadata、key storage/rotation 或安全声明时，必须更新本文档、wire、identity、Desktop connector 和 Mobile crypto 文档。
