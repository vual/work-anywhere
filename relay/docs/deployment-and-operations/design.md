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

Relay 与 Desktop 都声明 `go 1.26.0`、固定 `toolchain go1.27.3`。[`scripts/build.ps1`](../../scripts/build.ps1) 构建本地 binary；[`Dockerfile`](../../Dockerfile) 使用 Go 1.27.3 builder、`CGO_ENABLED=0` 和 distroless nonroot runtime。生产镜像只包含 `relay`，不包含 dev simulator。builder 阶段以 `$BUILDPLATFORM` 原生运行，并按 `TARGETOS`/`TARGETARCH` 交叉编译（未使用 buildx 时默认 linux/amd64）。runtime 阶段不执行任何命令，因此多架构构建不需要 QEMU。

发布 GitHub Release（或通过 `workflow_dispatch` 指定已有 tag）时，[release.yml](../../../.github/workflows/release.yml) 的 `relay-image` job 用 buildx 构建 linux/amd64 + linux/arm64 镜像，并推送到 `ghcr.io/<owner>/work-anywhere-relay`：

- tag 为 `<version>`（去掉前缀 `v`）和 `<major>.<minor>`；非预发布 semver 版本同时更新 `latest`。
- build args 写入 version、tag 对应的 commit 和 UTC 构建时间，可通过 `buildinfo` 查看。
- GHCR 包首次推送后默认为 private，需要在包设置中改为 public 才能匿名拉取。

[`compose.yaml`](../../compose.yaml) 的 relay/migrate 服务使用 `image: ${RELAY_IMAGE:-work-anywhere-relay:local}`：未设置时从本地构建，设置后可以 `docker compose pull` 并以 `up --no-build` 运行已发布的镜像。镜像目前不做签名、SBOM 或漏洞扫描。

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
ENROLLMENT_MODE=self_service|invite|open|disabled
RELAY_MAX_DAEMONS=10000
LOG_LEVEL=info
RELAY_SHUTDOWN_TIMEOUT=20s
RELAY_MAX_CONNECTIONS=10000
TRUSTED_PROXY_CIDRS=...
APNS_TEAM_ID / APNS_KEY_ID / APNS_KEY_FILE
FCM_CREDENTIALS_FILE
```

`RELAY_PUBLIC_URL` 接受 HTTP 或 HTTPS 的 IP/域名 origin，但 HTTP 不提供传输层机密性和完整性，公网部署应使用 HTTPS/WSS。`RELAY_DATA_KEY` 解码后必须恰好 32 bytes。生产应使用 `RELAY_DATA_KEY_FILE` 和 read-only secret mount；该 key 必须稳定备份，否则已有 push token 无法解密。当前没有 KMS adapter。APNs 三个参数必须同时设置。

`RELAY_MAX_CONNECTIONS` 限制 authenticated registry 中的设备连接数；认证前连接限制由反向代理承担。`TRUSTED_PROXY_CIDRS` 接入受信任 X-Forwarded-For 链解析，未配置时只使用 socket IP。`RELAY_MAX_DAEMONS` 限制 active/revoked daemon 登记总量，签名恢复不占新增额度。Relay 与 Compose 未设置 `ENROLLMENT_MODE` 时默认 `self_service`，daemon 无需邀请码即可签名注册；需要运维逐台发 token 的部署必须显式设置 `invite`。

设备注册、恢复和迁移升级步骤见 [自助注册设计](../daemon-enrollment/design.md)。先备份数据库，再升级 Relay 并迁移。未设置 `ENROLLMENT_MODE` 的既有部署升级后会直接进入 `self_service` 并接受新 daemon 注册；若需继续限制新增，升级前显式设置 `invite`，升级 Desktop 并验证扫码闭环后再切换为 `self_service`。重复 daemon 身份会使 migration 失败，须人工解决冲突后重试。

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

[`compose.yaml`](../../compose.yaml) 提供 PostgreSQL 18、migration 和默认 loopback-only Relay，默认地址 `http://127.0.0.1:18080`。需要从其他机器访问时设置 `RELAY_HOST_BIND_IP=0.0.0.0` 并放行端口；`RELAY_PUBLIC_URL` 可使用 HTTP 或 HTTPS 的 IP/域名，但公网应配置 HTTPS/WSS、证书、WebSocket idle timeout、请求/连接限制和受控的 proxy-to-Relay 网络；PostgreSQL 不得暴露公网。

生产备份、PITR、恢复演练、SBOM、镜像漏洞扫描、credential rotation、data-key rotation 和 alert 尚不是仓库自动化能力，部署者需在上线前补齐。

## 不变量与非目标

V1 不要求 Kubernetes、Redis、NATS、Kafka、对象存储或多区域数据库。多节点必须先设计 connection ownership、跨节点 enqueue ACK、partition/drain 和 revoke invalidation。

## 代码归属

- process/config/server：`cmd/relay/`, `internal/config/`, `internal/server/`
- schema/migration：`migrations/`, `internal/store/`
- image/dev stack：`Dockerfile`, `.dockerignore`, `compose.yaml`
- release image publishing：`.github/workflows/release.yml`（`relay-image` job）
- build/schema scripts：`scripts/`

## 验证方式

运行 fmt/test/vet/race、Buf generation diff、Docker build、Compose migration、ready/metrics probe 和 Desktop/Mobile WSS smoke。路由回归必须覆盖 1 MiB 超限的 `payload_too_large`、peer offline、queue full、拒绝 ACK 立即完成和迟到 ACK 隔离。生产发布前另做 backup restore、credential rotation、load/soak 和 TLS expiry 演练。

Relay Linux CI 使用 PostgreSQL 18 执行真实注册事务和迁移冲突测试，随后启动独立 Compose project 验证迁移、ready 与 self_service policy；Desktop 连接器同时验证共享签名向量。CI 定义见 [relay.yml](../../../.github/workflows/relay.yml)。手机扫码、电脑批准和真实加密调用仍需部署后的端到端验收，不能用 policy probe 替代。

## 文档更新触发

修改 Go 版本、依赖服务、端口、配置、secret、health、migration、镜像、shutdown、backup、多节点边界或部署平台时，必须更新本文档、技术选型和模块 README。
