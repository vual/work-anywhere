# Daemon 自助注册

## 目标与范围

设备通过 `daemon-enrollment-v1` 签名注册，无需管理员逐人发 token。`self_service/open` 允许新增，`invite` 要求邀请码，`disabled` 只允许已登记 active 身份的签名恢复。注册资格不授予手机控制权限，手机仍须扫码并由 Desktop 批准。

## 架构、接口与协议

`GET /v1/enrollment/policy` 返回 `protocol`、`mode`、`relayOrigin`。`POST /v1/enrollment/challenges` 接收 `ed25519PublicKey`、`x25519PublicKey`、`capabilityHash`（standard base64）和 `displayName`；返回 `requestId`、`challenge`（standard base64）、`expiresAtMs`。`POST /v1/enrollment/complete` 接收 `requestId`、`signature`（standard base64）和可选 `token`，新建返回 201、恢复返回 200，正文均为 `deviceId`、`created`。

签名域为 `waw-relay-daemon-enrollment-v1\0`。其后按顺序追加 origin UTF-8、request UUID 的 16 bytes、challenge 的 32 bytes，各字段用 uint32 big-endian 长度前缀；再追加 uint64 big-endian expiresAtMs，最后追加 Ed25519 public key、X25519 public key、capability hash、displayName UTF-8，均使用同样长度前缀。权威编码为 [enrollment.go](../../pkg/endpoint/protocol/enrollment.go)。

challenge 存于单节点内存，TTL 60 秒、上限 10,000，每 30 秒清理。完成请求原子取出 challenge，无论签名验证或数据库操作结果均不能再次使用；重试重新申请。客户端必须先安全保存私钥。Relay 仅保存公钥，签名只证明私钥持有，不证明真实用户或收费资格。

## 数据、失败与安全

daemon Ed25519 公钥在 active/revoked 记录中唯一。注册与邀请码消费在同一数据库事务中，额度检查串行化。相同 active 公钥及绑定恢复原 ID，不消耗邀请码、不覆盖名称；绑定冲突或 revoked 不恢复。迁移检测重复身份并失败，不自动合并。所有登记入口共享总设备上限（默认 10,000，含 revoked），已有恢复不受新增额度限制。

请求体最多 16 KiB，challenge/完成各按 IP 0.2/s burst 5，全局分别 10/s burst 50/20。429 含 Retry-After；网络/5xx 可以退避重试，身份错误停止。公网部署使用 HTTPS，开发可显式配置 HTTP。代理策略、连接限制与监控见 [滥用防护](../abuse-and-observability/design.md)。

HTTP 错误使用稳定 `code`：invalid_request、invalid_signature、challenge_expired、invite_required、invalid_invite、enrollment_disabled、identity_conflict、device_revoked、daemon_capacity_reached、rate_limited、temporarily_unavailable。旧登记入口同样报告身份冲突/撤销，不替换已登记密钥；self_service 部署拒绝旧无签名入口。注册字段中的两组公钥和 capability hash 均为 32 bytes。

## 不变量与非目标

不改变 WS protobuf、设备连接签名、Noise 或手机授权，不实现用户账号、多节点 challenge 或离线控制。旧无签名接口只用于既有邀请/open 兼容，self_service 新增必须使用新协议。

## 代码归属

`internal/server/enrollment.go`、`internal/store/enrollment.go`、`pkg/endpoint/protocol/enrollment.go`、`migrations/00002_daemon_identity.sql`；Desktop 消费端见 [连接器](../../../desktop/docs/remote-relay-connector/design.md)。

## 验证方式

共享 [签名向量](vector.json) 使用 seed bytes 0..31、request ID bytes 全为 5、challenge 全为 2、Noise public key 全为 3、capability 全为 4；两模块测试读取同一 fixture。测试错误签名、过期/并发/replay、限流、代理伪造；真实 PostgreSQL 验证唯一登记、邀请码原子消费、恢复、撤销、额度和重复迁移。设置 `RELAY_TEST_DATABASE_URL` 后运行 `go test ./internal/store`，测试只创建并删除独立随机 schema。执行模块 test/vet/race 与容器 smoke。

## 文档更新触发

接口、编码、TTL、注册模式、唯一性、额度、错误、持久化或恢复行为改变时同步更新本文、模块索引、身份/存储/运维及 Desktop 文档。
