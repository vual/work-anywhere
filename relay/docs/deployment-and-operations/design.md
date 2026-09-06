# Relay deployment and operations

> 状态：单节点 Linux container、Compose、migration 和 health 已实现；生产 TLS/备份/监控由部署环境提供。

## 目标与拓扑

```text
Internet :443
    │ trusted TLS/WSS reverse proxy
    ▼
one Work Anywhere Relay process ── PostgreSQL
    └── outbound HTTPS/HTTP2 ── APNs / FCM
```

V1 只有一个 Relay 节点。不能横向复制容器：在线 device registry、connection epoch 和 route queue 均在单进程内，当前没有跨节点 ownership/bus。

## Build and artifacts

Relay 与 Desktop 都声明 `go 1.26.0`、固定 `toolchain go1.26.5`。[`scripts/build.ps1`](../../scripts/build.ps1) 构建本地 binary；[`Dockerfile`](../../Dockerfile) 使用 Go 1.26.5 builder、`CGO_ENABLED=0` 和 distroless nonroot runtime。生产镜像只包含 `relay`，不包含 dev simulator。

## Runtime configuration

必需：

```text
RELAY_PUBLIC_URL=https://relay.example.com
DATABASE_URL=postgres://...
RELAY_DATA_KEY or RELAY_DATA_KEY_FILE
```

可选：

```text
RELAY_LISTEN_ADDR=:8080
ENROLLMENT_MODE=invite|open|disabled
LOG_LEVEL=info
RELAY_SHUTDOWN_TIMEOUT=20s
RELAY_MAX_CONNECTIONS=10000
TRUSTED_PROXY_CIDRS=...
APNS_TEAM_ID / APNS_KEY_ID / APNS_KEY_FILE
FCM_CREDENTIALS_FILE
```

`RELAY_DATA_KEY` 解码后必须恰好 32 bytes。生产应使用 `RELAY_DATA_KEY_FILE` 和 read-only secret mount；该 key 必须稳定备份，否则已有 push token 无法解密。当前没有 KMS adapter。APNs 三个参数必须同时设置。

`RELAY_MAX_CONNECTIONS` 限制 authenticated registry 中的设备连接数。`TRUSTED_PROXY_CIDRS` 当前只完成配置解析，尚未接入 forwarded-IP 判定；连接/IP 策略应由反向代理和应用现有直接 socket-IP limiter 共同承担。

## Commands, migrations, and health

```text
relay serve
relay migrate up|status
relay enrollment create [--ttl 24h] [--uses 1]
relay enrollment revoke TOKEN
relay version
```

Goose migration 包含 WAW schema；River migration 在同一 `migrate up` 流程建立 job schema。Compose 的 `migrate` service 成功后才启动 Relay。服务不会在 `serve` 时隐式迁移。

- `/health/live`：HTTP process 可响应。
- `/health/ready`：当前 PostgreSQL ping 成功。
- `/metrics`：Prometheus text；必须由代理限制为私网。

SIGTERM 取消 server 和 River context，并在配置 timeout 内关闭；客户端通过退避重连。当前未实现显式 not-ready/drain hint，因此发布替换会中断在线 session。

## Compose and production boundary

[`compose.yaml`](../../compose.yaml) 提供 PostgreSQL 18、migration 和 loopback-only Relay，默认地址 `http://127.0.0.1:18080`，只用于本机开发。公网必须配置真实 HTTPS origin、证书、WebSocket idle timeout、请求/连接限制和受控的 proxy-to-Relay 网络；PostgreSQL不得暴露公网。

生产备份、PITR、恢复演练、SBOM、镜像漏洞扫描、credential rotation、data-key rotation 和 alert 尚不是仓库自动化能力，部署者需在上线前补齐。

## 不变量与非目标

V1 不要求 Kubernetes、Redis、NATS、Kafka、对象存储或多区域数据库。多节点必须先设计 connection ownership、跨节点 enqueue ACK、partition/drain 和 revoke invalidation。

## 代码归属

- process/config/server：`cmd/relay/`, `internal/config/`, `internal/server/`
- schema/migration：`migrations/`, `internal/store/`
- image/dev stack：`Dockerfile`, `.dockerignore`, `compose.yaml`
- build/schema scripts：`scripts/`

## 验证方式

运行 fmt/test/vet/race、Buf generation diff、Docker build、Compose migration、ready/metrics probe 和 Desktop/simulator WSS smoke。生产发布前另做 backup restore、credential rotation、load/soak 和 TLS expiry 演练。

## 文档更新触发

修改 Go 版本、依赖服务、端口、配置、secret、health、migration、镜像、shutdown、backup、多节点边界或部署平台时，必须更新本文档、技术选型和模块 README。
