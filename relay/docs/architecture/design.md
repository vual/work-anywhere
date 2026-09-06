# Relay architecture and trust boundaries

> 状态：单节点 Relay V1 当前架构。

## 目标与范围

Relay 为 NAT/防火墙后的 Desktop daemon 和 Mobile 提供设备认证、配对、在线密文路由和通用 push。Relay 不运行 Codex、不管理官方 session，也不是消息历史数据库。

## 当前架构与数据流

```text
Desktop daemon ── WSS ──┐
                         ├─ Go Relay ── pgx/PostgreSQL
Mobile ───────── WSS ───┘       └──── River ── APNs / FCM

daemon ◄════ Noise-authenticated E2EE session ════► Mobile
                 Relay routes ciphertext
```

公开 HTTP/WSS、认证、pairing、routing 和 push dispatch 当前集中在 `internal/server`。认证成功的连接进入 64-shard `routing.Registry`；每个连接有 reader、writer 和 256-frame/4-MiB 有界队列。route 在 enqueue 前校验 active pairing，并用短 TTL positive ACL cache 降低数据库查询。

`internal/store` 封装 pgx transaction 和 migration；`internal/jobs`/River 管理 push；`internal/push` 访问 provider；`internal/observability` 和 `internal/limits` 提供 scrubbed log、metrics 与 token bucket。

## 信任边界

- Desktop daemon：Codex 会话、状态和权限的最终权威；持有 endpoint 私钥与正文。
- Mobile：持有自身 endpoint 私钥，只能调用 pairing scope 允许的方法。
- Relay：受信任验证设备、active pairing、route 和 push；不受信任读取业务 payload。
- PostgreSQL/备份：可见设备关系、时间和 push token 密文，不应出现 Prompt/消息。
- APNs/FCM：只获得通用通知和平台要求字段。

公网 TLS/WSS 保护外层认证与元数据；Noise E2EE 保护 daemon—Mobile 控制正文。开发环境只允许 loopback HTTP/WS。

## 公开接口

- `GET /health/live`
- `GET /health/ready`
- `GET /metrics`（与公开 listener 共用，生产代理必须限制）
- `POST /v1/enroll/daemon`
- `WS(S) /v1/connect`
- `WS(S) /v1/pair`

具体 frame 见 [wire protocol](../wire-protocol/design.md)，身份流程见 [identity and pairing](../identity-and-pairing/design.md)。

## 状态与持久化

connection、registry、ACL cache、pending pairing socket 和 Noise session 只在进程/endpoint 内存。PostgreSQL 保存 device、enrollment token hash、pairing session/edge、push endpoint 密文、River job 和预留 audit table。Relay 不保存业务 ciphertext mailbox。

## 失败处理

- 进程/socket 退出：两端重新认证并重建 Noise session；业务恢复依赖 daemon conversation read/cursor。
- PostgreSQL unavailable：ready 失败；cache miss、enroll、pair、revoke 和 push fail closed。
- peer offline：route 返回失败，不排队命令。
- queue full：关闭慢 recipient，并向 sender 返回 `queue_full` ACK。
- revoke：数据库 edge 失效、ACL cache invalidated、在线双方收到 metadata-only revoke。
- SIGTERM：ready 关闭、registry connections 关闭、HTTP/River 在 timeout 内停止；当前没有平滑 session handoff。

## 不变量与非目标

Relay 不能调用 Desktop Pipe/App Server；业务内容和 endpoint private key 不进入 Relay；多对多由独立 pairing edges 组成。V1 不提供账号、网页聊天、云端历史、离线命令、多节点和跨区域 route。

## 代码归属

- entry/server：`cmd/relay/`, `internal/server/`
- routing：`internal/routing/`, `internal/limits/`
- persistence：`internal/store/`, `internal/secretbox/`, `migrations/`
- push：`internal/jobs/`, `internal/push/`
- endpoint E2EE references：`internal/e2ee/`, `desktop/internal/remote/`

## 验证方式

集成测试覆盖 enroll、auth、signed pairing、route、revoke、push enqueue 与错误路径；Desktop + browser simulator smoke 覆盖真实 Noise control round-trip。网络并发变更运行 race；生产前仍需 load/soak、packet/log/database canary 扫描和独立安全审查。

## 文档更新触发

修改公开 endpoint、组件/信任边界、Relay 可见元数据、持久化、离线行为、依赖服务或模块职责时，必须更新本文档及根目录中英文架构说明。
