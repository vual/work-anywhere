# Desktop design documentation

本目录是 Desktop 模块设计的唯一主索引。文档描述当前代码的真实状态；命令使用说明放在模块 [`README.md`](../README.md)，仓库级规则见 [`AGENTS.md`](../../AGENTS.md)。

## How to use this index

修改功能前，先从下表找到该功能的主设计文档，再沿“关联设计”检查受影响文档。一次改动涉及多个功能时，必须同步更新所有相关设计，不能只更新最直接的一份。

| 功能 | 主设计文档 | 关联设计 | 主要代码归属 |
| --- | --- | --- | --- |
| 单 EXE 与总体架构 | [`architecture/design.md`](architecture/design.md) | 全部设计 | `cmd/work-anywhere`, `internal/app`, `internal/provider` |
| 平台支持与适配边界 | [`platform-support/design.md`](platform-support/design.md) | 构建兼容、生命周期、安全 | `internal/platform`, `packaging` |
| Codex 运行时发现与实例身份 | [`runtime-and-instance/design.md`](runtime-and-instance/design.md) | 平台支持、App Server 托管、构建兼容 | `internal/codex`, `internal/platform/identity`, `internal/platform/processinfo` |
| Desktop Bridge 与 Named Pipe IPC | [`bridge-ipc/design.md`](bridge-ipc/design.md) | 平台支持、JSON-RPC、App Server 托管 | `internal/bridge`, `internal/ipc`, `internal/platform/localipc` |
| App Server 监督与升级 | [`app-server-supervision/design.md`](app-server-supervision/design.md) | 运行时、JSON-RPC、管理生命周期 | `internal/daemon`, `internal/codex` |
| JSON-RPC 多路复用、会话与审批 | [`json-rpc-and-actions/design.md`](json-rpc-and-actions/design.md) | Bridge IPC、控制面、持久化 | `internal/daemon/instance.go`, `internal/jsonrpc` |
| 本机控制 API、CLI 与 Provider | [`local-control/design.md`](local-control/design.md) | JSON-RPC、存储、管理入口 | `internal/daemon/admin.go`, `internal/cli`, `internal/provider` |
| SQLite、加密、日志与附件 | [`persistence-and-security/design.md`](persistence-and-security/design.md) | 平台支持、控制面、JSON-RPC | `internal/store`, `internal/platform/secrets`, `internal/logging` |
| 管理页、托盘与进程生命周期 | [`administration-and-lifecycle/design.md`](administration-and-lifecycle/design.md) | 平台支持、App Server 托管、控制面 | `internal/daemon/admin.go`, `internal/platform/tray`, `internal/platform/process` |
| 构建、安装和官方版本兼容 | [`build-install-and-compatibility/design.md`](build-install-and-compatibility/design.md) | 平台支持、运行时发现、Bridge IPC | `scripts`, `packaging`, `internal/platform/installation`, `testdata` |

## Directory rules

- 每个功能使用一个稳定、短横线命名的目录：`docs/<feature>/`。
- 每个功能目录必须有 `design.md`；其他文件只有在被设计文档引用时才应存在。
- 一个设计点只有一个主归属。其他功能需要它时使用链接并描述自身依赖，不复制整份协议。
- 目录、功能或协议重命名时必须同时修复本索引、[`AGENTS.md`](../../AGENTS.md) 和所有反向链接。

## Writing a feature design

新增功能时复制以下结构，并将新文档加入上方功能表：

```markdown
# Feature name

## Goal and scope
## Current architecture and data flow
## Interfaces
## State and persistence
## Security and failure handling
## Invariants and non-goals
## Code ownership
## Verification
## Documentation update triggers
```

只保留当前实现。如果实现替换了旧路径，应直接重写对应章节并移除失效描述；不要增加“变更记录”“历史版本”或按日期排列的说明。计划能力必须明确标为尚未实现，且不能混入当前数据流。

## Review checklist

- 文档中的命令、方法名、状态名、目录和环境变量与代码一致。
- 入口 README 能导航到新设计，设计之间的链接没有断裂。
- 数据保存范围、加密边界、日志开关和权限边界描述准确。
- 故障与 fallback 行为有明确边界，不作超出实现的保证。
- 单元测试、集成测试或人工 E2E 覆盖了设计中的关键不变量。
