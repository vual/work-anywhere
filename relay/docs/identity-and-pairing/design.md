# Device identity and QR pairing

> 状态：Relay 与 Desktop pairing V1 当前实现。

## Goal and scope

V1 不要求 Work Anywhere 用户账号。每个 daemon 和 Mobile installation 都是独立设备身份；二维码只创建一条 daemon—Mobile 授权边。该模型天然支持一个 daemon 配多个 Mobile、一个 Mobile 配多个 daemon，并允许逐边撤销。

## Device keys

每个 endpoint 首次运行生成两组长期密钥：

- Ed25519 identity key：用于向 Relay 证明设备身份、签署 pairing grant 和 key binding。
- X25519 Noise static key：用于 daemon—Mobile 的 Noise KK 会话。

Relay 只保存公钥。daemon 私钥由 Desktop 平台 secret store 保护（Windows 为 current-user DPAPI）；Mobile 私钥进入 iOS Keychain/Android Keystore 可用的硬件保护层，不能进入普通 preferences、日志或云端备份。

## Daemon enrollment

daemon 自助签名注册、恢复、注册模式与错误协议由 [Daemon 自助注册设计](../daemon-enrollment/design.md) 定义。`self_service` 无需运维发 token；Desktop 先安全保存密钥，再申请 challenge、签署绑定 origin/双公钥的 transcript。相同 active 身份恢复原 ID；重复绑定、撤销和迁移规则也由该文档拥有。

公网 Relay 默认使用 `invite` enrollment mode：

1. 运维方生成一次性、高熵 enrollment token，Relay 只保存 token hash、expiry 和剩余次数。
2. daemon 通过 HTTP(S) 提交 token、两组公钥、显示名称和协议能力。
3. Relay 原子消费 token，创建 `daemon` device，返回随机 daemon ID。
4. 此后 daemon 不使用长期 bearer token，而是在每次 WS(S) 连接中签署 server challenge。

开发环境可显式启用 `open` enrollment；公共自助服务显式使用 `self_service`，默认仍为 `invite`。`disabled` 模式允许已有 daemon 重连和有私钥证明的 active 身份恢复，拒绝新增。

## Connection authentication

`WSS /v1/connect` 建立后：

1. Relay 发送 32 字节随机 challenge、connection ID、server time 和短 expiry。
2. client 返回 device ID、capabilities 和 Ed25519 签名。签名输入为固定 domain separator、wire major version、connection ID、challenge、expiry 和 capability hash。
3. Relay 查询 active device public key，验证签名、expiry 和 capability limits。
4. 成功后 socket 才登记到进程内 device registry；失败立即关闭。

challenge 只属于当前 socket process，不复用、不写 URL、不生成长期 session token，因此不能跨连接 replay。

## QR pairing flow

daemon 在线时发起 pairing：

1. daemon 生成 128-bit pairing ID 和 256-bit pairing secret，请 Relay 建立五分钟 pairing session。secret 不上传 Relay。
2. Desktop CLI 返回 `waw://pair` URI（管理页当前只显示状态，不渲染 QR），其中包含 Relay origin、pairing ID、secret、daemon ID、daemon Ed25519/X25519 public keys、capability hash 和 expiry。
3. Mobile 扫码，在本机生成密钥，通过受限 `WSS /v1/pair` 提交 pairing ID、两组 Mobile public keys、设备显示信息和 `HMAC-SHA256(secret, server_challenge || pairing_transcript)`。
4. Relay 不知道 secret，只把请求转给当前在线 daemon；daemon验证 HMAC、QR transcript、expiry 和用户确认状态。
5. daemon 返回 Ed25519 签名的 pairing grant，绑定双方 device ID/public keys、scope、pairing ID、issued/expiry。
6. Relay 用已登记 daemon identity key 验证 grant，原子创建 Mobile device 和 active pairing；Mobile 使用 QR 中 daemon key 验证同一 grant。
7. 两端保存 pairing ID、peer keys 和 scope，销毁 pairing secret。

pairing session 最多允许五次尝试且只能成功一次。daemon 离线、用户取消、过期、transcript/HMAC 不匹配都失败，不创建半配对设备。

## Authorization scopes

V1 scope 的权威集合定义在本节，Desktop 默认集合及 Relay 白名单由跨模块契约测试校验：

- `status.read`
- `conversation.read`
- `conversation.create`
- `turn.start`
- `turn.interrupt`
- `action.read`
- `action.resolve`
- `attachment.write`
- `catalog.read`

Desktop CLI 默认批准以上全部 scope；本机控制 API 可在批准时传入 scope 子集。`system.shutdown`、`traffic.enable/disable`、本机管理 URL、安装和 daemon 强制退出不允许远程调用。Relay 校验 pairing active，daemon 在解密后按 method 再校验 scope，daemon 是业务授权的最终权威。

## Revocation and key loss

- 任一已授权 daemon 管理入口可撤销 Mobile pairing；Mobile 也可删除自身 pairing。
- Relay 将 pairing 标为 revoked、断开目标 route 并广播 metadata-only revocation。
- daemon 持久化本地 revocation，后续即使 Relay 状态陈旧也拒绝旧 peer。
- 私钥丢失不提供服务器恢复；设备必须删除并重新配对。
- V1 密钥轮换通过重新配对完成，不设计静默恢复或共享群组密钥。

## State and persistence

Relay 保存 device public keys/status、pairing edges/scopes、pairing session expiry/attempts 和 enrollment token hash。pairing secret、endpoint private key、Noise session key和业务 payload不保存。

## Security and failure handling

- QR secret 必须来自 CSPRNG，禁止使用六位数字作为唯一秘密；短数字只可作为人工核对 fingerprint。
- 所有签名/HMAC 输入使用固定 canonical encoding 和 domain separator，防止跨协议重用。
- 设备显示名、platform、app version 都是不可信 metadata，不能进入授权决策。
- pairing endpoint 在认证前受连接数、IP、pairing ID 和尝试次数限流。

## Invariants and non-goals

- Relay 从不持有 endpoint private key 或 pairing secret。
- 一条 pairing 只连接一个 daemon 和一个 Mobile；多对多是多条独立 edge。
- Relay 用户账号、邮件/短信登录、密码恢复和团队共享不属于 V1。

## Code ownership

- Relay endpoint/flow：`internal/server/`
- canonical signing/HMAC：`internal/protocol/`
- device/pairing persistence：`internal/store/`, `migrations/`
- Desktop identity/confirmation：`desktop/internal/remote/`

## Verification

- Ed25519 challenge、过期/replay、错误 key 和 revoked device 测试。
- QR transcript/HMAC、单次成功、并发抢占、取消和过期测试。
- 一 daemon 多 Mobile、一 Mobile 多 daemon、逐边 scope 和撤销 E2E。
- 日志/数据库扫描确认无 private key/pairing secret。

## Documentation update triggers

修改设备 key、enrollment、challenge、QR 字段、pairing transcript、scope、撤销或恢复策略时，必须更新本文档、E2EE、wire、存储、Desktop connector 和未来 Mobile 文档。

## 默认授权与兼容升级

Desktop CLI 和管理页批准时均可省略 scopes，由 Desktop 使用上述九项默认授权。显式 API scope 子集仍需通过 Relay 和 Desktop 校验；非法及重复项被拒绝。旧八项 pairing 的 `turn.start` 继续兼容授权创建会话，不要求重新配对。升级顺序为 Relay → Desktop → Mobile；不自动重签或降级已提交的授权决定。跨模块测试读取 Desktop 实际默认声明并以该集合执行 Relay 配对握手，另测旧八项、显式子集和非法 scope。
