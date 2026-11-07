# Relay design documentation

> 状态：本文索引单节点 Relay V1 的当前实现与明确未完成项。

本目录是 Relay 模块设计的主索引。仓库规则见 [`AGENTS.md`](../../AGENTS.md)，运行入口见 [`relay/README.md`](../README.md)。Relay wire protocol 由本模块拥有；Desktop 提交独立生成代码但不得维护另一份 schema，未来 Mobile 也必须引用同一协议。

## Feature index

| 功能 | 主设计文档 | 关联设计 | 当前代码归属 |
| --- | --- | --- | --- |
| 技术选型 | [`technology-selection/design.md`](technology-selection/design.md) | 架构、部署 | `go.mod`, `cmd/relay/`, `Dockerfile` |
| 总体架构与信任边界 | [`architecture/design.md`](architecture/design.md) | 全部设计 | `cmd/relay/`, `internal/server/` |
| 设备身份与二维码配对 | [`identity-and-pairing/design.md`](identity-and-pairing/design.md) | E2EE、存储、滥用防护 | `internal/server/`, `internal/store/`, `pkg/endpoint/protocol/` |
| Daemon 自助注册 | [`daemon-enrollment/design.md`](daemon-enrollment/design.md) | 身份、存储、滥用防护、Desktop connector | `internal/server/enrollment.go`, `internal/store/enrollment.go` |
| 端到端加密会话 | [`end-to-end-encryption/design.md`](end-to-end-encryption/design.md) | wire、Desktop connector | `pkg/endpoint/e2ee/`, `desktop/internal/remote/` |
| Relay wire protocol | [`wire-protocol/design.md`](wire-protocol/design.md) | E2EE、路由、Desktop connector | `proto/waw/relay/v1/`, `internal/gen/` |
| 在线路由与可靠性 | [`routing-and-reliability/design.md`](routing-and-reliability/design.md) | wire、部署 | `internal/routing/`, `internal/server/` |
| 移动推送 | [`push-notifications/design.md`](push-notifications/design.md) | 存储、可靠性、运维 | `internal/push/`, `internal/jobs/` |
| 数据、隐私与保留 | [`persistence-and-privacy/design.md`](persistence-and-privacy/design.md) | 身份、通知、滥用防护 | `internal/store/`, `internal/secretbox/`, `migrations/` |
| 滥用防护与可观测性 | [`abuse-and-observability/design.md`](abuse-and-observability/design.md) | 公网接口、部署 | `internal/limits/`, `internal/observability/` |
| 部署与运维 | [`deployment-and-operations/design.md`](deployment-and-operations/design.md) | 技术选型、可靠性、通知 | `Dockerfile`, `compose.yaml`, `scripts/` |
| 浏览器 Mobile simulator | [`mobile-simulator/design.md`](mobile-simulator/design.md) | wire、配对、E2EE | `cmd/mobile-simulator/`, `internal/devmobile/` |
| 剩余实施与验收 | [`implementation-plan/design.md`](implementation-plan/design.md) | 全部设计 | 生产 Mobile、加固和运维工作 |

## Cross-module ownership

- Relay 外层连接、身份、配对、路由、push 和加密 envelope 由 [`relay.proto`](../proto/waw/relay/v1/relay.proto) 定义。
- 密文内的业务请求遵循 [Desktop 本机控制语义](../../desktop/docs/local-control/design.md)，Remote 只开放明确 allowlist。
- Desktop 出站连接器的实现与限制见 [`desktop/docs/remote-relay-connector/design.md`](../../desktop/docs/remote-relay-connector/design.md)。
- 正式 Mobile 的方案与文档索引见 [`mobile/docs/README.md`](../../mobile/docs/README.md)；其生成代码必须继续引用本模块 wire/E2EE 规范，不得复制 schema。

## Current invariants

- 双端主动 WS(S)；家庭网络不需要端口映射。
- 无 WAW 用户账号；设备 Ed25519 公钥是服务身份，daemon 默认通过自助签名注册登记，显式 `invite` 模式由 enrollment token 约束。
- 每条 daemon—Mobile pairing 独立授权，天然支持多对多。
- 在线业务 payload 使用双方静态身份绑定的 Noise KK 端到端加密；Relay 不持有解密 key。
- Relay 不保存会话 payload，也不提供离线写命令。
- Push 只包含通用提示，Mobile 重连后从 daemon 读取权威状态。
- V1 单节点只依赖 PostgreSQL；在线连接只存在 Go 进程内。

## Documentation rules

每个功能目录保持一个 `design.md`。功能、协议、安全、配置、存储或部署行为改变时，代码与所有相关文档必须同一项工作更新。文档只描述当前状态和仍未支持的边界，不记录按日期排列的历史。
