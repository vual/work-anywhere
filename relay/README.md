# Work Anywhere Relay

> 状态：单节点 Relay V1 已实现。Android-first Mobile 测试客户端已建立，但尚未完成全量真机 E2E、iOS host、推送和生产发布验收；`cmd/mobile-simulator` 仍只用于协议联调。

Relay 位于 Desktop daemon 与 Mobile 之间，提供公网设备注册、二维码配对、在线密文路由和 APNs/FCM 通知。它不运行 Codex，也不保存或解密 Prompt、消息、工具输出、diff、审批内容和附件正文。

## 当前能力与边界

Relay 保持 2 MiB WebSocket frame 与 1 MiB routed ciphertext 上限；超限路由返回 `payload_too_large`。Mobile endpoint 会按外层 message ID 处理 route ACK，因此 peer offline、queue full 或超限拒绝会立即结束请求，不再等待业务超时。

```text
Desktop daemon ── outbound WS(S) ──┐
                                 ├─ Go Relay ── River/PostgreSQL ── APNs / FCM
Mobile app ───── outbound WS(S) ───┘

Desktop daemon ◄════ Noise_KK E2EE remote session ════► Mobile app
```

- Ed25519 challenge-response 设备认证和一次性 daemon enrollment token。
- 显式桌面确认的二维码配对；一对一授权边组合成 daemon/Mobile 多对多关系。
- Protocol Buffers binary WebSocket、分片在线注册表、ACL cache、有界发送队列和慢连接断开。
- daemon 与 Mobile 使用 `Noise_KK_25519_ChaChaPoly_SHA256` 加密控制请求、响应和事件；Relay 只转发密文。
- PostgreSQL 保存设备公钥、配对关系、加密 push endpoint、enrollment token hash 和最小审计元数据。
- River 持久化通用 APNs/FCM 通知任务；通知不包含会话正文。
- 单节点、在线命令模式。daemon 离线时不排队执行 Turn、审批或中断。

V1 不提供 Work Anywhere 用户账号、多节点路由、离线命令 mailbox、网页聊天或生产 Mobile 客户端。Android Mobile 已接入已有项目内的新建 thread、会话设置、稳定 catalog、消息队列和问题回答；完整 approval DTO、真机 E2E 与正式发布验收尚未完成，详见 [Mobile 入口](../mobile/README.md)。

## 技术栈

Relay 与 Desktop 都声明 `go 1.26.0`、固定 `toolchain go1.27.3`。主要依赖为 `net/http`、`coder/websocket`、Protocol Buffers、PostgreSQL/pgx、Goose 和 River。生产镜像使用无发行版运行时镜像，V1 服务拓扑只有一个 Relay 进程和 PostgreSQL。

选型权衡见 [`docs/technology-selection/design.md`](docs/technology-selection/design.md)，全部功能文档见 [`docs/README.md`](docs/README.md)。

## 使用 Docker Compose 启动

要求 Docker/Compose。开发环境先创建 `.env`，并生成一个需要长期稳定保存的 32 字节数据密钥：

```powershell
cd relay
Copy-Item .env.example .env
$key = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($key)
$encoded = [Convert]::ToBase64String($key)
(Get-Content .env) -replace 'replace-with-32-byte-standard-base64-key', $encoded | Set-Content .env
docker compose up --build -d
docker compose exec relay /relay migrate status
Invoke-RestMethod http://127.0.0.1:18080/health/ready
```

Compose 默认只把端口绑定到 loopback，适合本机联调。需要从其他机器访问时，在 `.env` 设置 `RELAY_HOST_BIND_IP=0.0.0.0`，并确保防火墙放行 `RELAY_HOST_PORT`。`RELAY_PUBLIC_URL` 接受 HTTP 或 HTTPS 的 IP/域名 origin；HTTP 是明文连接，公网部署仍建议使用受信任 TLS/WSS。PostgreSQL、`/metrics` 和管理命令不得暴露公网。生产环境应通过 read-only secret 文件提供 `RELAY_DATA_KEY_FILE`、APNs 和 FCM 凭据，不能把 `.env` 提交到仓库。

每次发布 GitHub Release 时，[`Release` workflow](../.github/workflows/release.yml) 会把 linux/amd64 + linux/arm64 镜像推送到 `ghcr.io/vual/work-anywhere-relay`。镜像 tag 为 `<version>`（tag 去掉前缀 `v`）和 `<major>.<minor>`；非预发布版本还会更新 `latest`。不想在服务器上构建时，在 `.env` 中设置 `RELAY_IMAGE=ghcr.io/vual/work-anywhere-relay:<version>`，然后执行：

```bash
docker compose pull
docker compose up -d --no-build
```

未设置 `RELAY_IMAGE` 时，Compose 仍从本地 `Dockerfile` 构建。

Relay 默认使用自助模式（`ENROLLMENT_MODE=self_service`），无需邀请码。Desktop 管理页填写与 `RELAY_PUBLIC_URL` 相同的规范化 origin，点击“启用远程访问”即可自动签名注册，然后扫码并在电脑批准手机。需要逐台授权新增设备时，在 `.env` 中设置 `ENROLLMENT_MODE=invite` 并重建 Compose，再用下方命令创建邀请码。`open` 仍供可信开发环境使用，`disabled` 拒绝新设备但允许签名恢复已有 active 身份。协议见 [Daemon 自助注册](docs/daemon-enrollment/design.md)。

升级顺序：先备份 PostgreSQL，执行 `migrate up` 并升级 Relay。未设置 `ENROLLMENT_MODE` 的既有部署升级后会直接开放自助注册；若要继续限制新增，升级前显式设置 `invite`，升级 Desktop 并联调后再切换为 `self_service`。迁移检测到重复 daemon 公钥会报告设备 ID 并停止，必须人工处理，不自动删除身份或配对。`RELAY_MAX_DAEMONS` 默认 10,000，包含 revoked 登记；恢复原身份不占新增额度。

真实 PostgreSQL 注册测试通过 `RELAY_TEST_DATABASE_URL` 指向测试数据库后运行 `go test ./internal/store`，测试使用独立随机 schema，不修改既有表。未提供该变量时测试会明确跳过。

```powershell
docker compose exec relay /relay enrollment create --ttl 24h --uses 1
```

迁移、版本和撤销命令：

```text
relay migrate up|status
relay enrollment create [--ttl 24h] [--uses 1]
relay enrollment revoke TOKEN
relay version
relay serve
```

## Desktop 与模拟器联调

启动 Desktop daemon 后，使用本机 Relay：

```powershell
work-anywhere.exe remote configure http://127.0.0.1:18080
work-anywhere.exe remote enroll <enrollment-token> "My PC"
work-anywhere.exe remote status
work-anywhere.exe remote pair start
```

复制 `remote pair start` 返回的 `waw://pair?...` URI，再启动开发模拟器：

```powershell
go run ./cmd/mobile-simulator
```

在浏览器模拟器粘贴 URI 后，Desktop 会出现待确认 pairing；通过以下命令批准或拒绝：

```powershell
work-anywhere.exe remote pair list
work-anywhere.exe remote pair approve <pairing-id>
work-anywhere.exe remote pair reject <pairing-id>
```

模拟器可建立真实 Noise 会话并调用受 scope 限制的 Desktop control 方法。它不实现平台安全存储、系统推送或生产 Mobile UX。

## 本机构建与验证

```powershell
..\desktop\.tools\go-sdk\go\bin\go.exe fmt ./...
..\desktop\.tools\go-sdk\go\bin\go.exe test ./...
..\desktop\.tools\go-sdk\go\bin\go.exe vet ./...
.\scripts\generate.ps1
.\scripts\build.ps1 -Version 0.1.0 -Revision <git-sha>
```

网络、路由或并发改动还要运行 `go test -race ./...`（Windows 无 race toolchain 时使用官方 Go Linux 容器）。协议生成后必须确认提交的 Relay/Desktop 生成代码没有未提交差异。部署和安全限制见 [`docs/deployment-and-operations/design.md`](docs/deployment-and-operations/design.md)。
