# Claude Code provider

## Goal and scope

让 Mobile 通过 `mobile-control-v1` 使用当前 Windows 用户本机的 Claude Code：浏览项目、会话和完整历史，在已有项目中新建会话，发送文字/图片，实时查看回复和思考，处理工具审批、`AskUserQuestion` 提问和计划确认，中断运行中的 Turn，并选择模型、思考等级和权限模式。正在电脑端其他 Claude Code 进程（终端或 Claude 桌面应用）中打开的会话只读镜像，不接管。

## Current architecture and data flow

```text
Mobile ──Relay(E2EE)──► daemon.control()
                          ├─ agentId 属于 Codex 实例 ─► 现有 App Server 投影（不变）
                          └─ agentId 属于 provider.Backend ─► backendControl()
                                 └─ claudecode.Backend
                                      ├─ 读：<configDir>/projects/*/<sessionId>.jsonl     转录（只读）
                                      ├─ 读：<configDir>/sessions/<pid>.json              活跃会话登记（只读）
                                      └─ 写：每会话一个 headless claude.exe（stream-json stdio）
```

- daemon 启动时由 `startBackends` 创建后端：配置 `claudeCode.enabled=false` 时跳过；未配置时仅在 `<configDir>/projects` 存在时启用。`configDir` 依次取配置 `claudeCode.configDir`、`CLAUDE_CONFIG_DIR`、`~/.claude`。后端在后台解析 CLI（见下文），解析完成前 agent 只声明 `history`/`desktopMirror` 能力。
- `control()` 对 Mobile contract 请求先按 `agentId` 查后端；命中时由 `backendControl` 处理 agent 方法，未知 agent 仍走 Codex 路径（`invalid_target`）。
- **读路径**：后端每 2 秒轮询，增量扫描转录（按文件偏移续读完整行，尾部半行留到下次）并重读活跃会话登记；时间线首次读取时解析整份转录并缓存（最多 8 份，LRU）。
- **写路径**：首个 `turn.start` 为会话启动一个 `claude.exe` 子进程（已有转录用 `--resume`，新会话用 `--session-id`），之后同一进程连续服务多轮；无 Turn、无待处理请求超过 10 分钟后关闭 stdin 回收，下次发送再 `--resume`。进程隐藏窗口并加入 daemon 的 job object，daemon 退出时一并终止。CLI 输出逐行解析：
  - `stream_event` 的 `text_delta`/`thinking_delta` → `assistantText`/`reasoning` delta；
  - `assistant`/`user` 消息 → 会话失效（Mobile 读取权威快照）；
  - `control_request{can_use_tool}` → 待处理请求（见“交互映射”）；其他 host 回调以 error 响应，避免 CLI 阻塞；
  - `result` → Turn 完成、失败或被中断。
- **ID 一致性**：daemon 用 Mobile `clientMessageId` 派生用户消息 UUID 并作为 stdin `uuid` 发送，CLI 把它原样写入转录，所以 `turnId` 在实时事件、快照和历史中一致；assistant 条目 ID 为 `<API message id>:<block index>`，与流式 delta 的 `api_message_id` + `index` 相同，快照替换实时条目时不会出现重复项。用户消息条目在 daemon 进程内回填 `clientMessageId`，供 Mobile 合并本地发送气泡。
- 后端事件经 `provider.Notifier` 交给 daemon。daemon 为每个会话维护 `sequence`（所有事件递增）和 `revision`（仅 Turn 与 action 变化递增），事件名沿用 Remote 已识别的名称，因此 delta 批处理、失效合并和推送提示无需改动。

## Interfaces

### provider.Backend

[`internal/provider/provider.go`](../../internal/provider/provider.go) 定义共享 Mobile DTO（agent、项目、会话、时间线、action、catalog、设置、计划确认）、`Event`/`Notifier` 和 `Backend` 接口。daemon 的 `mobile*` 类型是这些类型的别名。后端错误使用 `provider.Error{Code, Message}`，由 `mobileControlError` 原样映射为 control error code。

### Mobile 方法映射

| 方法 | Claude Code 行为 |
| --- | --- |
| `agent.list` | `{kind:"claude-code", name:"Claude Code"}`；CLI 可用时 `capabilities` 为 history、liveStream、textInput、imageInput、approval、question、interrupt、plan、createConversation、modelSelection、reasoningSelection、permissionMode、desktopMirror，`queue`/`steer` 为 false；附带活跃 Turn 与待处理请求计数 |
| `project.list` | 以转录中的真实 `cwd` 分组，只含有用户提问的会话；排序与 cursor 同 Codex |
| `conversation.list` | `projectId`、`query`（标题子串）、`limit`（默认 20，最大 100）、cursor；`updatedAt desc, id desc`；`running`/`status`/`needsAttention` 来自 daemon 自有进程；`externallyActive` 来自活跃登记 |
| `conversation.read` | metadata、时间线首页、当前设置、空队列、`capabilities.queue=false`、待确认计划时的 `planConfirmation`、daemon 版本号 |
| `conversation.timeline.list` | 最新优先；cursor 为本页最旧条目 ID；ID 不存在返回 `invalid_params` |
| `conversation.content.read/resolve` | 复用 daemon contentRef 和 512 KiB 响应预算 |
| `conversation.subscribe` | 无事件回放；cursor 与当前 sequence 不同即 `snapshot=true` |
| `conversation.create` | 只接受索引中的 `projectId`，生成新会话 UUID 并记住项目目录；首个 Turn 才启动进程。未知项目或目录不可访问返回 `project_not_resolved` |
| `turn.start` | 校验 `expectedRevision`；可带 `settings`（先保存再发送）和已提交的图片附件；返回 `{turn:{id}}` |
| `turn.interrupt` | 发送 `interrupt` 控制请求；10 秒内未结束则终止进程 |
| `action.list` / `action.resolve` | 见交互映射；第二次解析返回 `already_resolved` |
| `plan.respond` | `implement` 批准计划，`revise` 把 `text` 作为修订意见返回模型；不另起新 Turn |
| `conversation.settings.update` | 校验后保存；空闲进程停止，下一 Turn 以新参数启动；运行中的 Turn 保持启动参数 |
| `catalog.models` | `default`（CLI 配置模型）及 `opus`/`sonnet`/`haiku` 别名；别名被 `ANTHROPIC_DEFAULT_*_MODEL`（环境或用户 settings）映射时作为 description 显示；effort 为 low/medium/high/xhigh/max |
| `catalog.permissionProfiles` | `default`、`autoReview` 可用；`fullAccess` 可用且 `dangerous=true`；`readOnly` 不可用（Claude Code 无等价模式） |
| `attachment.begin` | 目录取自后端可信项目根；上传绑定到该 agent，只能被同一会话的 `turn.start` 使用，仅接受 `image/*` |
| `conversation.queue.*` | `unsupported_capability` |

设置到 CLI 参数：协作模式 `plan` → `--permission-mode plan`；否则 `default`→`default`、`autoReview`→`auto`、`fullAccess`→`bypassPermissions` + `--allow-dangerously-skip-permissions`；`modelId` 非 `default` 时传 `--model`，非空 effort 传 `--effort`。

### 交互映射

| CLI 请求 | Mobile 表现 | 回复 |
| --- | --- | --- |
| `can_use_tool`（普通工具） | `action.list` 中 `kind=approval`，`method=claude/canUseTool/<tool>`，`detail` 含工具名、描述、命令或文件路径、受阻路径 | `{decision:"accept"}` → `allow` 原输入；`{decision:"decline"}` → `deny` 固定说明 |
| `can_use_tool`（`AskUserQuestion`） | `kind=userInput`，问题 ID 为 `q0..`，选项来自 CLI，`isOther=true` | `{answers:{q0:{answers:[...]}}}` → `allow`，`updatedInput.answers[<问题文本>]` 为以 `, ` 连接的答案 |
| `can_use_tool`（`ExitPlanMode`） | 不进入 `action.list`；快照 `planConfirmation.planItemId` 为该 tool_use ID，时间线中为 `plan` 条目 | `plan.respond` |

用户 `permissions.allow` 中的工具由 CLI 直接放行，不产生请求。

### 转录投影

| 转录记录 | Timeline |
| --- | --- |
| 用户提问（非 meta、非 tool_result） | `userMessage`，开启新 Turn（`turnId` = 记录 `uuid`）；斜杠命令显示为 `/name args`，图片为 `[image]` |
| assistant `text` / `thinking` | `agentMessage` / `reasoning`，ID 为 `<message.id>:<apiBlockIndex>`；API 错误消息为 `error` block |
| `Bash`、`PowerShell` | `commandExecution`：`command` block，结果后追加 `output` block |
| `Edit`、`MultiEdit`、`Write`、`NotebookEdit` | `fileChange`：`diff` block（首行路径，`-`/`+` 行） |
| `ExitPlanMode` | `plan` block（Markdown 计划）；被要求修订时仍为 `completed` |
| `mcp__<server>__<tool>`、`WebSearch`、`Task`/`Agent`、其他工具 | `mcpToolCall`、`webSearch`、`collabAgentToolCall`、`dynamicToolCall` summary |
| `compact_boundary`、压缩摘要 | `contextCompaction` summary |

工具条目在 `tool_result` 前为 `inProgress`，之后为 `completed` 或 `failed`。忽略子 agent（`isSidechain`）、meta、仅转录可见记录和簿记记录。标题优先级：最后一条 `custom-title` > 最后一条 `ai-title` > 首条用户提问首行。

## State and persistence

全部状态在 daemon 内存中：会话摘要索引、最多 8 份已解析时间线、活跃会话集合、会话进程与待处理请求、每会话设置、`clientMessageId` 映射（最多 1024 条）和 revision/sequence。daemon 不直接写 `~/.claude`；转录由 daemon 启动的 CLI 自身写入。会话设置不持久化，daemon 重启后回到默认（CLI 配置模型、默认权限）。配置只新增可选 `claudeCode`（`enabled`、`configDir`、`executable`），与其他设置一样经 DPAPI 保护。

## Security and failure handling

- **CLI 解析**：配置的 `claudeCode.executable` 优先；否则在 PATH 中找 `claude.exe`，或把 npm 的 `claude.cmd` 包装解析为包内 `bin/claude.exe`，最后尝试 `~/.local/bin/claude.exe`。只执行原生 exe，不经 `cmd.exe`；版本低于 [`MinimumVersion`](protocol.md) 不使用。找不到时写方法返回 `runtime_unavailable`，读路径不受影响。
- **子进程环境**：移除继承的 `CLAUDE_CODE_*`、`CLAUDE_AGENT_SDK_*`、`CLAUDECODE` 等宿主变量，设置 `CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL=1`；用户自己的 `ANTHROPIC_*` 配置和 Claude settings 照常生效，daemon 不读取或转发凭据。
- **目录与会话**：会话 ID 必须是索引中的 UUID 文件名或 daemon 创建的 UUID；工作目录只来自转录索引或基于已索引项目创建的会话，Mobile 不能传路径；启动前确认目录存在。
- **电脑端占用**：活跃登记中进程仍存活（Windows 上核对 `procStart` 防 PID 复用）且不是 daemon 自己的子进程时，会话为 `externallyActive`，`turn.start` 返回 `conversation_busy_external`。登记文件遗留但进程已退出时不锁定。daemon 无法阻止用户在 daemon 进程运行期间另开终端 `--resume` 同一会话。
- **并发**：同一会话已有活跃 Turn 或待处理请求时返回 `conversation_busy`；请求解析先移除再回复，保证 first-valid-wins。写操作的 `expectedRevision` 不匹配返回 `revision_conflict`。
- **进程失败**：进程退出时，活跃 Turn 以失败（或中断）结束，待处理请求全部失效；下一次发送重新启动进程。
- **权限**：`fullAccess` 映射到 Claude Code 的 bypass 模式并在 catalog 中标记 `dangerous`；`readOnly` 不可选，避免把非只读模式伪装为只读。
- 读取错误返回 `provider_error`，不影响 Codex 路径；大内容遵循与 Codex 相同的 contentRef 和响应上限。

## Invariants and non-goals

- 后端失败或被禁用不影响 Codex 实例与 Bridge。
- `externallyActive` 会话只读；daemon 从不接管电脑端正在使用的会话。
- 实时条目 ID 与持久化条目 ID 一致；`turnId` 等于用户消息记录 UUID。
- 尚未支持：运行中排队或 steer、hook/MCP elicitation 等 host 回调、子 agent 时间线展开、会话设置持久化、命令输出实时流（命令结果在完成后随快照出现）、Claude 桌面应用 UI 状态镜像。

## Code ownership

- DTO、事件与后端接口：`internal/provider/provider.go`
- Claude Code 后端：`internal/provider/claudecode/`
  - `backend.go`：索引、活跃判定、会话状态、读方法、空闲回收
  - `interactive.go`：新建会话、发送、中断、action、计划、设置与 catalog
  - `session.go`：CLI 进程、stream-json 解析、控制请求与回复
  - `runtime.go`：CLI 解析、版本门控、环境净化、启动参数、模型与权限目录
  - `transcript.go`：转录解析与投影
- daemon 路由、版本、事件与附件：`internal/daemon/backends.go`，共享附件存储 `beginAttachment`（`internal/daemon/daemon.go`）
- 进程存活检查：`internal/platform/processinfo/alive_*.go`
- 测试数据：`testdata/claude-code/transcripts/`（合成转录）、`testdata/claude-code/<version>/`（headless 协议录制，见 [`protocol.md`](protocol.md)）

## Verification

```powershell
cd desktop
.\.tools\go-sdk\go\bin\go.exe test ./internal/provider/... ./internal/daemon/... ./internal/remote/...
.\.tools\go-sdk\go\bin\go.exe vet ./...
```

- `backend_test.go`：项目/会话聚合、标题、时间线映射、稳定 cursor、增量追加、半行、活跃登记、删除通知。
- `interactive_test.go`：测试二进制在 `WA_FAKE_CLAUDE` 下充当按录制协议工作的假 CLI（`fakecli_test.go`），覆盖流式 delta 与持久化 ID 一致、启动参数与环境净化、initialize 握手、进程复用、审批允许/拒绝与重复解析、提问答案映射、计划确认与修订、中断、进程崩溃、电脑端占用拒绝、自有进程不算外部占用、新建会话、设置到启动参数与重启、空闲回收。
- `internal/daemon/backends_test.go`：agent 合并、分页、快照与 contentRef、事件名与 sequence/revision、写方法路由与 revision 冲突、附件绑定、catalog 转换、队列不支持。
- `internal/remote/delta_backend_test.go`：后端 delta payload 被 Remote 批处理识别。
- 人工：用真实 CLI 新建会话并发送、批准一次 PowerShell 写文件、回答一次 `AskUserQuestion`、修订一次计划、以 `fullAccess` 发送；回到电脑端 `claude --resume <id>` 能看到完整记录。

## Documentation update triggers

增加或改变 Mobile 方法映射、交互映射、转录映射、ID 规则、活跃判定、进程生命周期、CLI 解析或环境处理、配置项、轮询/缓存边界时，必须更新本文档、[`protocol.md`](protocol.md)、[本机控制设计](../local-control/design.md) 和 [Mobile 协议同步设计](../../../mobile/docs/protocol-and-sync/design.md)。
