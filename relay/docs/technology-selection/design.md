# Relay technology selection

> 状态：Relay V1 当前技术基线。

## Goal and scope

Relay 的主要负载是长期 WSS、连接状态、短消息转发、配对事务和移动推送，不是 CPU 密集计算。技术栈必须让连接生命周期、背压、资源上限、可测试性和单机部署保持清晰，同时与 Desktop 的 Go 工程能力复用。

## Decision

V1 采用：

- Go 1.26.5，与 Desktop daemon 的仓库工具链完全一致；Relay `go.mod` 使用 `go 1.26.0` 和 `toolchain go1.26.5`，CI 和镜像也锁定 1.26.5。
- 标准库 `net/http`、`context`、`crypto/*`、`log/slog` 和 `runtime/metrics`。
- `github.com/coder/websocket` 处理 WSS；业务层禁止压缩并设置显式 read/write limit、deadline 和 close code。
- `google.golang.org/protobuf` + Buf 管理公开 schema、lint 和生成代码；针对已发布基线的 breaking check 尚未接入。
- PostgreSQL + `github.com/jackc/pgx/v5/pgxpool` 保存身份、配对、push endpoint 和有限审计元数据。
- River + PostgreSQL 执行 APNs/FCM push job，利用事务入队、重试、唯一 job 和并发队列。
- Go `testing`、race detector、fuzz、`httptest` 和 Testcontainers/Compose 完成测试。
- Prometheus-compatible metrics；结构化日志统一经 `slog` scrubber。OpenTelemetry 尚未接入。
- 单个 Linux Go binary + multi-stage Docker image 发布；默认 `CGO_ENABLED=0`。

依赖版本在代码落地时锁定，设计文档维护支持线和替换约束，不逐个记录 patch 历史。Relay 不单独升级 Go；安全补丁或 major/minor 升级必须与 Desktop 一起验证、更新仓库工具链和 CI。Go 官方按受支持 release line 提供修复，因此仓库需要定期执行统一升级。[Go release policy](https://go.dev/doc/devel/release)

## Process and concurrency model

一个 Relay 进程包含：

- `Server`：根 `context.Context`、HTTP server、数据库、job client 和 shutdown 编排。
- `Connection`：每个 WSS 一个 reader goroutine、一个 writer goroutine和有界 outbound channel；只有 writer 写 socket。
- `Registry`：固定数量分片的 `map[deviceID]*Connection`，分片独立加锁；新 epoch 原子替换旧连接。
- `Router`：校验 sender、pairing ACL、scope、expiry、rate/size 后，把 frame 入队给本节点目标连接。
- `PairingService`、`DeviceService`、`PushService`、`Store`：通过窄 interface 组合，不使用全局 service locator。

根 context 取消会停止接收新连接、关闭 HTTP listener、drain writer/job worker，并在 deadline 后强制关闭。goroutine panic 必须被连接边界捕获并记录，不能让异常业务 frame 终止整个进程。

## Why Go

- 与 Desktop 共用语言、lint/test/fuzz 习惯和 protobuf 生成链，跨模块排障成本低。
- 单二进制、较小运行时依赖和成熟的标准网络库适合自托管与云容器。
- goroutine 很适合把 socket 读、写和取消建模为独立阻塞流程；有界 channel 能把慢消费者策略写成显式不变量。
- pgxpool 是并发安全的 PostgreSQL pool；River 可把 push 状态与 endpoint 更新放入同一数据库事务，避免再部署独立队列。[pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool) [River](https://pkg.go.dev/github.com/riverqueue/river)
- `coder/websocket` 提供 context-based API、读限制和 ping/close 支持，且不强迫客户端采用框架私有协议。[coder/websocket](https://pkg.go.dev/github.com/coder/websocket)

## Alternatives

### Elixir/Phoenix

BEAM 的进程隔离、监督树和分布式 Presence 非常适合大规模长连接。如果首要目标一开始就是多节点、跨节点在线路由，它会降低部分运行时编排成本。但 V1 明确为单节点，采用 Elixir 会新增语言、构建、发布和监控体系；当前收益不足以抵消双栈维护。未来出现可量化的单节点连接上限时再基于压测复评，而不是预先切换。

### Rust

Rust 提供更强的内存/类型安全和更低运行开销，但 async runtime、应用层迭代、数据库和 push 集成的开发成本更高。Relay 数据面只转发有上限的小 frame，Go 已足够。可把需要审计的密码实现限制为跨语言 vectors 验证的端侧库，不需要整套 Relay 使用 Rust。

### Node.js

Node.js WebSocket 生态成熟，迭代快，但必须额外约束 event-loop 阻塞、CPU 工作隔离、进程扩展和慢消费者。Go 的阻塞式 goroutine、race detector 和单 binary 更贴合当前团队与部署目标。

## Scaling boundary

V1 只支持一个 active Relay 节点。在线 registry 是进程内状态，PostgreSQL 不是 Presence 总线，也不用于每帧路由。负载或可用性要求达到单节点边界后，先设计 `node_id + connection_epoch` 所有权和内部 route ACK，再选择 NATS Core 或内部 gRPC route mesh；该扩展不能改变公网 protobuf/E2EE 协议。

## Invariants and non-goals

- 语言选型不能改变 Relay 不解密或持久化业务 payload 的边界。
- 不因采用 Go 而共享未版本化的内存结构；跨 Desktop/Mobile 的契约仍是 schema 与 golden vectors。
- V1 不引入 Web framework、ORM、Redis、NATS、Kafka、Kubernetes 或对象存储作为必需组件。
- channel、goroutine、timer、cache 和连接都必须有 owner、取消路径和硬上限。

## Code ownership

- `relay/go.mod`, `relay/go.sum`
- `relay/cmd/relay/`
- `relay/internal/{server,routing,push,jobs,store,observability,limits,e2ee,protocol}/`
- `relay/proto/waw/relay/v1/`, `relay/gen/`
- `relay/Dockerfile`, `relay/compose.yaml`, `relay/scripts/`

## Verification

- `go fmt ./...` 不遗留格式改动，`go test ./...`、`go vet ./...` 和 `go test -race ./...` 通过。
- 10k 持久 WSS 基线、慢客户端、重复连接替换、重连和 shutdown leak tests。
- pprof/goroutine dump 证明断连、超时和失败 pairing 不泄漏 goroutine/timer。
- 单节点无 Redis/NATS 环境完成 pairing、route 和 push job smoke test。
- 只有 Go runtime、GC 或连接模型出现可复现瓶颈时，才重新评估语言或拆分数据面。

## Documentation update triggers

修改仓库统一 Go 版本、WebSocket/protobuf/database/job stack、并发模型、构建产物或多节点边界时，必须更新本文档、Desktop 工具链文档、架构、路由、部署设计和模块 README。
