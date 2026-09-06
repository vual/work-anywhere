# Relay remaining implementation plan

> 状态：Relay、Desktop connector 与开发 simulator 的本机控制闭环已完成；本文只保留达到可发布产品仍需实施的工作。

## 目标与范围

当前 Go server 已覆盖 enrollment、认证、显式 pairing、多对多授权边、Noise E2EE、在线路由、Desktop remote adapter、River push adapters、PostgreSQL、容器与开发 simulator。下一阶段目标是生产 Mobile、真实推送验证和生产安全/运维加固，而不是重做已存在的 Relay 基础。

## Phase 1 — protocol release gate

- 固化 wire v1 reserved fields 和兼容策略，加入针对主分支基线的 Buf breaking check。
- 把 canonical auth、pair grant、pair proof、Noise prologue/transport 形成语言无关 golden vectors。
- 对 pairing、Noise binding、metadata leakage、replay、revocation 和 DoS 做独立 threat-model review。
- 增加 protobuf/fuzz/property tests，确认未知 minor/field 不导致连接进程崩溃。

验收：Relay、Desktop 和 Mobile 对同一 vectors 输出一致；schema regeneration 无 diff；安全 review 的阻断项关闭。

## Phase 2 — production Mobile

- 建立 `mobile/` 模块与文档索引，选择 iOS/Android 技术栈和 secure storage。
- 实现 Ed25519/X25519 identity、扫码、grant 验证、Noise session、scope UX 和 revoke。
- 实现任务状态、完整历史、实时输出、审批/问题、interrupt、文字/图片输入、模型/思考/权限选择。
- 实现前后台重连、cursor/snapshot 恢复和禁止 daemon offline 时提交写命令。

验收：真实 iOS/Android 设备与 Desktop 完成同一控制闭环；Keychain/Keystore、锁屏隐私和进程恢复测试通过。

## Phase 3 — provider and notification validation

- 使用 Apple sandbox 和 FCM test project 验证 registration、action/completion、invalid token、credential rotation 和 provider outage。
- 完善 Mobile notification permission/channel/click-to-sync 行为。
- 增加 privacy mode、按 pairing/target 的速率限制与 notification collapse 策略。

验收：push payload/log/database 无业务正文，provider 接受与最终状态严格解耦，关闭通知权限不影响控制状态。

## Phase 4 — production hardening

- 接入 `RELAY_MAX_CONNECTIONS` 和可信代理解析，明确 proxy spoofing 边界。
- 建立 10k socket、慢消费者、洪泛、24h soak 和容量基线。
- 增加 dashboard、alerts、SLO、trace（无 payload）以及 stable error 分类。
- 实现 retention cleanup、device/privacy delete、audit writes、data-key rotation/KMS。
- 完成 deploy/rollback、drain、migration、backup restore、certificate/provider credential rotation runbooks。
- 增加 SBOM、dependency/container/secret scan 和 release checksum/signing。

验收：所有公网 endpoint 有验证过的 rate/size/connection limits；恢复与轮换演练、race/load/soak 和安全审查通过。

## Optional later work

只有单节点容量或可用性数据证明需要时，才设计多节点 connection ownership、route bus、enqueue ACK、partition/drain 和 revoke invalidation。账号系统、离线 command mailbox、跨区域 active-active 和其他 AI provider 不属于 Relay V1。

## Code ownership

- Relay server/protocol/deployment：`relay/cmd/relay/`, `relay/internal/`, `relay/proto/`, `relay/migrations/`
- Desktop adapter：`desktop/internal/remote/`, `desktop/internal/daemon/`
- Mobile：未来 `mobile/`
- CI/release：`.github/workflows/`, `relay/scripts/`

## 验证方式

每项功能遵循对应设计的测试要求；Relay 基线为 fmt/test/vet，网络与并发改动加 race，schema 改动加 Buf lint/regeneration/breaking 和跨语言 vectors，数据库/镜像改动加 migration/Compose/container smoke。

## 文档更新触发

剩余范围、阶段依赖、验收、工具链或跨模块职责改变时直接重写本文档。已实现能力应描述在对应功能设计中，不在本文保留完成历史。
