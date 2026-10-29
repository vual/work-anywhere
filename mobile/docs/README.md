# Mobile design documentation

> 状态：Android-first 代码已建立并通过本地 Go/Flutter 测试，多电脑 catalog、锚定选择菜单、管理页排序/启停/编辑/本机删除和会话设置已接入 Desktop 权威状态；真机、iOS、push、安全审查和发布验收仍未完成。文档中的未完成目标不得被表述为当前可用能力。

本目录是 Mobile 模块设计的唯一主索引。运行/范围入口见 [`mobile/README.md`](../README.md)，仓库规则见 [`AGENTS.md`](../../AGENTS.md)。

## Feature index

| 功能 | 主设计文档 | 关联设计 | 计划代码归属 |
| --- | --- | --- | --- |
| 技术栈、分层与平台边界 | [`architecture/design.md`](architecture/design.md) | 协议同步、交互、交付计划 | `core/`, `app/lib/`, `app/android/`, `app/ios/` |
| 配对、E2EE、请求与状态同步 | [`protocol-and-sync/design.md`](protocol-and-sync/design.md) | Relay wire/E2EE、Desktop control、交互 | `core/mobilecore/`, `app/lib/core/` |
| 单工作台、Daemon/Agent/Project/Conversation 交互 | [`product-interaction/design.md`](product-interaction/design.md) | 架构、协议同步 | `app/lib/features/`, `app/lib/app/` |
| 分阶段实施与验收 | [`delivery-plan/design.md`](delivery-plan/design.md) | [当前验收记录](delivery-plan/verification.md)、全部设计 | `mobile/app/` 与跨模块前置改动 |

## Cross-module ownership

- 外层 protobuf schema、配对 transcript、Noise binding 和 push frame 由 [Relay wire protocol](../../relay/docs/wire-protocol/design.md) 所有；Mobile 不维护 fork。
- 密文内 control method、Agent/Project/Conversation DTO、业务错误、revision/cursor 和 action 仲裁由 [Desktop local control](../../desktop/docs/local-control/design.md) 所有；Codex-first 查询扩展在实现前同步写入 Desktop 设计。
- Mobile 负责端上身份私钥、连接状态机、视图 projection、系统权限和交互；不得把 Relay 当作历史或命令队列。
- Mobile Go core 与 Relay/Desktop 由同一组 repository golden vectors 和端到端测试验证；复用语言/包不能替代真实产物互操作测试。

## Documentation rules

- 每个功能目录只保留当前目标方案与明确未支持项，不记录日期型进度日志。
- Flutter scaffold 落地后，把“计划代码归属”改为真实路径，并在同一改动补齐构建、测试和发布命令。
- 修改 scope、control DTO、event/error、二维码字段、密钥存储、后台策略、通知 metadata、导航或关键文案时，必须更新所有受影响文档和跨模块链接。
