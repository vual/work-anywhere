# Claude Code provider

## Goal and scope

让 Mobile 通过 `mobile-control-v1` 浏览当前 Windows 用户本机 Claude Code 的项目和会话历史，并识别正在电脑端其他 Claude Code 进程中打开的会话。当前实现是**只读**后端：项目列表、会话列表、时间线、大内容读取、订阅版本和电脑端占用状态。发送消息、流式输出、工具审批、提问、中断、新建会话、模型/权限选择尚未实现，相关方法返回稳定错误 `unsupported_capability`。

## Current architecture and data flow

```text
Mobile ──Relay(E2EE)──► daemon.control()
                          ├─ agentId 属于 Codex 实例 ─► 现有 App Server 投影（不变）
                          └─ agentId 属于 provider.Backend ─► backendControl()
                                 └─ claudecode.Backend
                                      ├─ <configDir>/projects/*/<sessionId>.jsonl  （转录，只读）
                                      └─ <configDir>/sessions/<pid>.json             （活跃会话登记，只读）
```

- daemon 启动时由 `startBackends` 创建 Claude Code 后端：配置项 `claudeCode.enabled` 为 `false` 时跳过；未配置时仅在 `<configDir>/projects` 存在时启用。`configDir` 依次取配置 `claudeCode.configDir`、环境变量 `CLAUDE_CONFIG_DIR`、`~/.claude`。
- `control()` 对 Mobile contract 请求先按 `agentId` 查后端注册表；命中且方法属于会话方法时由 `backendControl` 处理，否则落回 Codex 路径。未知 `agentId` 的行为与以前一致（`invalid_target`）。
- 后端每 2 秒轮询：增量扫描转录文件（按文件偏移继续读取完整行，尾部半行留到下次），并重读活跃会话登记。会话变化通过 `provider.Notifier` 通知 daemon；daemon 递增该会话的 revision/sequence 并发布 `provider.conversation.updated` 事件，列表级变化发布 `provider.conversations.changed`。Remote connector 把它们转为无 payload 的 `conversation.invalidated`，Mobile 重新读取快照。
- 时间线在首次读取时解析整份转录并缓存（最多 8 份，LRU），之后按偏移增量追加。

## Interfaces

### provider.Backend

[`internal/provider/provider.go`](../../internal/provider/provider.go) 定义 Mobile DTO（`AgentSummary`、`ProjectSummary`、`ConversationSummary`、`ConversationMetadata`、`TimelineItem/Block/Page`、`Page[T]`）和 `Backend` 接口。daemon 的 `mobile*` 类型是这些类型的别名，Codex 与其他后端共用同一份 JSON 形状。后端错误使用 `provider.Error{Code, Message}`，由 `mobileControlError` 原样映射为 control error code。

### Mobile 方法映射

| 方法 | Claude Code 行为 |
| --- | --- |
| `agent.list` | 追加 `{kind:"claude-code", name:"Claude Code", capabilities:{history:true, desktopMirror:true}}`；`status` 为 `ready` 或转录目录缺失时的 `unavailable` |
| `project.list` | 以转录记录中的真实 `cwd` 分组，只含至少有一条用户提问的会话；排序与 cursor 与 Codex 相同 |
| `conversation.list` | 支持 `projectId`、`query`（标题子串，不区分大小写）、`limit`（默认 20，最大 100）、cursor；排序 `updatedAt desc, id desc` |
| `conversation.read` | 快照：metadata、时间线首页、`settings.mode="default"`、空队列、`capabilities.queue=false`、daemon 版本号 |
| `conversation.timeline.list` | 最新优先；cursor 为本页最旧条目 ID 的 base64url，新追加的条目不会使旧页偏移；ID 不存在返回 `invalid_params` |
| `conversation.content.read/resolve` | 复用 daemon 的 contentRef 与 512 KiB 响应预算；正文由 `Backend.TimelineBlock` 读取 |
| `conversation.subscribe` | 无事件回放；`cursor` 与当前 sequence 不同即 `snapshot=true` |
| `action.list` | 空数组 |
| `catalog.models`、`catalog.permissionProfiles` | 空 `items` |
| `conversation.create`、`turn.start`、`turn.interrupt`、`action.resolve`、`plan.respond`、`conversation.settings.update`、`conversation.queue.*`、`attachment.begin` | `unsupported_capability` |

### 转录投影

| 转录记录 | Timeline |
| --- | --- |
| 用户提问（非 meta、非 tool_result） | `userMessage`/`text`，开启新 Turn（`turnId` = 该记录 `uuid`）；斜杠命令显示为 `/name args`，图片显示为 `[image]` |
| assistant `text` | `agentMessage`/`text`；API 错误消息为 `error` block |
| assistant `thinking` | `reasoning`/`reasoning`（空内容跳过） |
| `Bash`、`PowerShell` | `commandExecution`：`command` block，结果到达后追加 `output` block |
| `Edit`、`MultiEdit`、`Write`、`NotebookEdit` | `fileChange`：`diff` block（首行路径，`-`/`+` 行） |
| `mcp__<server>__<tool>` | `mcpToolCall` summary，值 `server/tool` |
| `WebSearch` | `webSearch` summary，值为查询 |
| `Task`、`Agent` | `collabAgentToolCall` summary |
| 其他工具 | `dynamicToolCall` summary，值为工具名加文件名/模式等单行信息 |
| `compact_boundary`、压缩摘要 | `contextCompaction` summary |

工具条目状态为 `inProgress`，对应 `tool_result` 出现后变为 `completed` 或 `failed`（`is_error`）。忽略 `isSidechain`（子 agent）、`isMeta`、`isVisibleInTranscriptOnly` 记录以及 `api-request*`、`queue-operation`、`file-history-*` 等簿记记录。标题优先级：最后一条 `custom-title` > 最后一条 `ai-title` > 首条用户提问首行（80 字符）。

## State and persistence

全部状态在 daemon 内存中：会话摘要索引、最多 8 份已解析时间线、活跃会话集合、每会话 revision/sequence。daemon 不写 `~/.claude`，不复制或持久化转录内容；配置只新增可选的 `claudeCode` 设置（`enabled`、`configDir`），与其他设置一样经 DPAPI 保护。

## Security and failure handling

- 只读取当前 OS 用户的 Claude Code 配置目录；会话 ID 必须是 UUID 文件名，Mobile 只能用索引中已存在的 ID 读取，不能传入路径。
- 项目 `cwd` 只来自转录索引（`Backend.ProjectRoot`），供后续写路径使用，Mobile 不能指定目录。
- 活跃判定：`sessions/<pid>.json` 中的进程仍存活，且（Windows 上）进程创建时间与 `procStart` 一致，防止 PID 复用。进程退出后遗留的登记文件不再锁定会话。
- 转录文件缩短或同尺寸被改写时整份重新解析；无法解析的行被跳过，未知记录类型和字段被忽略。读取失败返回 `provider_error`，不影响 Codex 路径。
- 大内容遵循与 Codex 相同的 contentRef、预览和 512 KiB 响应上限。

## Invariants and non-goals

- 后端失败或被禁用不影响 Codex 实例与 Bridge。
- `ExternallyActive=true` 的会话在写路径实现后也只读；不接管电脑端正在使用的会话。
- 当前不支持：发送与流式输出、工具审批、`AskUserQuestion`、计划确认、中断、新建会话、模型/effort/权限选择、图片附件、队列与 steer、子 agent 时间线展开、Claude Desktop 应用 UI 状态镜像。写路径所需的 headless CLI 协议已经实测并记录在 [`protocol.md`](protocol.md)。

## Code ownership

- DTO 与后端接口：`internal/provider/provider.go`
- Claude Code 后端：`internal/provider/claudecode/`（`backend.go` 索引、活跃判定、Backend 方法；`transcript.go` 转录解析与投影）
- daemon 路由、版本与事件：`internal/daemon/backends.go`
- 进程存活检查：`internal/platform/processinfo/alive_*.go`
- 测试数据：`testdata/claude-code/transcripts/`（合成转录）、`testdata/claude-code/<version>/`（headless 协议录制，见 [`protocol.md`](protocol.md)）

## Verification

```powershell
cd desktop
.\.tools\go-sdk\go\bin\go.exe test ./internal/provider/... ./internal/daemon/...
.\.tools\go-sdk\go\bin\go.exe vet ./...
```

- `internal/provider/claudecode/backend_test.go`：项目/会话聚合、标题优先级、时间线映射与状态、稳定 cursor、增量追加、半行延迟解析、活跃登记与进程退出、文件删除通知。
- `internal/daemon/backends_test.go`：`agent.list` 合并、分页、快照与 contentRef、订阅版本与事件发布、不支持写方法的错误码、未知 agent 落回 Codex 路径。
- 手动：daemon 运行后用 Mobile 或 `relay/internal/devmobile` 选择 Claude Code agent，确认项目/会话与本机一致；在电脑端打开一个会话，Mobile 显示电脑端占用并在新回复写入后约 2 秒内刷新。

## Documentation update triggers

增加或改变 Mobile 方法映射、转录记录映射、活跃判定、配置项、轮询/缓存边界，或实现任一写路径能力时，必须更新本文档、[`protocol.md`](protocol.md)、[本机控制设计](../local-control/design.md) 和 [Mobile 协议同步设计](../../../mobile/docs/protocol-and-sync/design.md)。
