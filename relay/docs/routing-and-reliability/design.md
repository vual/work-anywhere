# Online routing, connection registry, and reliability

> 状态：单节点在线路由 V1 当前实现。

## 目标与连接模型

每个认证 WSS 对应一个 `routing.Connection`，拥有 context、唯一 writer goroutine 和有界 outbound channel。reader goroutine 顺序解析入站 protobuf；writer 负责序列化写 socket 和每 25 秒 WebSocket ping。

`Registry` 固定 64 shards。每 device 只有一个 primary connection；register 分配单调 epoch，新连接原子替换并取消旧连接。全局连接数由 `RELAY_MAX_CONNECTIONS` 限制。

## 当前路由流程

1. reader 校验 binary frame、wire major、message ID、frame rate 和 body。
2. route 从已认证 socket 覆盖 sender，并校验 recipient、pairing ID、session expiry/ciphertext size。
3. 查询 active-pairing positive cache；miss 时查询 PostgreSQL，错误 fail closed。
4. 从 registry 查找在线 recipient，把序列化 ServerFrame 非阻塞 enqueue。
5. enqueue 成功向 sender 返回 accepted `route_ack`；失败返回 error ACK。

route 是 online-only、bounded、at-most-once。ACK 只表示 enqueue，不表示目标写 socket、解密或执行成功；业务结果必须等待 E2EE response。目标可通过 `delivery_ack` 回报 frame 接收，Relay 仍不持久化重试。

## Backpressure and limits

- WebSocket read limit 2 MiB；pair socket 64 KiB。
- routed ciphertext 上限由 protocol 常量限制为 1 MiB。
- outbound queue 最多 256 frames 或 4 MiB，任一达到即失败。
- queue full 时 recipient connection 被关闭，sender 收到 `queue_full`。
- connect/pair/enroll 以直接 socket IP 限流；认证后 frame 与 notify 以 device 限流。

当前没有 volatile/control 优先级、保留队列预算、bytes/s/day quota 或 attachment-specific Relay quota。attachment 通过 Desktop control 的分块 API 在 E2EE JSON request 中传输。

## Reconnection and recovery

Desktop connector 使用指数退避重连；browser simulator 由用户重新连接。每次 socket 恢复都重新做 Ed25519 auth 和 Noise handshake，不复用 transport key。Relay 不补发已丢 frame；客户端以 daemon 的 conversation read/sequence/revision 恢复最终状态。

## Failure handling

- ACL miss/database error、peer offline、expired、not paired 和 queue full 均不写 mailbox。
- revoke 同步清除 matching ACL cache 并通知在线 peer。
- process crash 丢失全部在线 route；容器重启后客户端重连。
- sequence/decrypt gap 由 endpoint 关闭 Noise session，Relay 不解密也不补包。
- SIGTERM 关闭 registry 和 HTTP；没有跨进程 drain/connection transfer。

## 不变量与非目标

所有连接、queue、cache 和 limiter state 有 owner/上限/TTL。V1 不保证 token delta、online state、route 或 push exactly-once，不支持多 Relay 节点、离线 mailbox、Kafka log 或对象存储附件。

## 代码归属

- connection/registry/ACL：`internal/routing/`
- socket handlers/router：`internal/server/`
- limiter：`internal/limits/`
- constants/schema：`internal/protocol/`, `proto/waw/relay/v1/relay.proto`

## 验证方式

测试覆盖 connection capacity、new-connection-wins、bounded frame/byte queue、ACL cache、route、offline、revoke 和 race。生产前仍需 10k socket、slow consumer、network flap、PostgreSQL outage、leak 与 long-running soak。

## 文档更新触发

修改 primary connection、registry/epoch、route/ACK、queue/limit、heartbeat、reconnect/cursor、多节点或 offline 语义时，必须更新本文档、wire、架构、部署和 Desktop connector 文档。
