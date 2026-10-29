# Claude Code headless protocol (recorded)

本文件记录对 Claude Code CLI headless 模式的实测结果，是 [Claude Code provider](design.md) 写路径的协议依据。当前 Desktop 代码**尚未**启动 Claude Code 进程；这里描述的是已验证的 CLI 行为和录制的 fixture，不是已实现能力。stdio 控制协议不是公开稳定接口，实现写路径时必须以版本门控和 fixture 测试守护。

## Recorded version and fixtures

- CLI：`2.1.306`（Windows，npm 包内的原生 `bin/claude.exe`）。
- fixture：[`testdata/claude-code/2.1.306/`](../../testdata/claude-code/2.1.306/)，每个场景一对 `<scenario>.stdin.jsonl`（发送内容）与 `<scenario>.stdout.jsonl`（CLI 输出）：`text-turn`（同进程两轮）、`resume-turn`、`approval-turn`（允许一次、拒绝一次）、`question-turn`（`AskUserQuestion`）、`plan-turn`（`ExitPlanMode` 拒绝）、`interrupt-turn`。
- fixture 已脱敏：`system/init` 只保留协议字段，用户路径替换为 `C:\work`、`C:\Users\user`，模型名替换为 `claude-model`，`system/thinking_tokens` 已删除。

## Process launch

```text
claude.exe -p --input-format stream-json --output-format stream-json --verbose
           --include-partial-messages --permission-prompts host --permission-prompt-tool stdio
           (--session-id <uuid> | --resume <sessionId>) [--permission-mode <mode>] [--model <model>]
```

- stdin 第一行必须是 `{"type":"control_request","request_id":"…","request":{"subtype":"initialize"}}`。缺少 initialize 或 `--permission-prompt-tool stdio` 时，CLI 认为没有审批面：需要审批的工具被自动拒绝（出现在 `result.permission_denials`），且不向模型提供 `AskUserQuestion` 与 `ExitPlanMode`。
- 子进程环境必须移除继承的宿主变量（`CLAUDE_CODE_*`、`CLAUDE_AGENT_SDK_*`、`CLAUDECODE`、`CLAUDE_EFFORT`、`CLAUDE_PID` 等），否则 CLI 走宿主鉴权并返回 401 “Not logged in”；同时设置 `CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL=1`。用户 settings 中的 `env`（如 `ANTHROPIC_BASE_URL`、`ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`）由 CLI 自行加载，模型别名可能被映射为非 Anthropic 名称。
- npm 安装的 `claude.cmd` 只是调用 `node_modules/@anthropic-ai/claude-code/bin/claude.exe` 的包装；应直接执行 exe，不经 `cmd.exe`。
- 用户 `permissions.allow` 中的工具不会触发审批请求。

## Messages

| 方向 | 消息 | 说明 |
| --- | --- | --- |
| → | `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"…"}]}}` | 一轮输入；同一进程可连续发送多轮 |
| ← | `system/init` | 每轮开头输出，含 `session_id`、`model`、`permissionMode`、`claude_code_version` |
| ← | `system/status`、`system/thinking_tokens` | 状态提示，可忽略 |
| ← | `stream_event`（`message_start`、`content_block_start/delta/stop`、`message_delta`、`message_stop`） | 文本与 thinking 增量 |
| ← | `assistant` | 完整的 assistant content block（text、thinking、tool_use） |
| ← | `user` | `tool_result`，附带结构化 `tool_use_result` |
| ← | `result` | 一轮结束：`subtype`、`is_error`、`result`、`stop_reason`、`permission_denials` |

### Permission requests

```json
← {"type":"control_request","request_id":"<id>","request":{"subtype":"can_use_tool","tool_name":"PowerShell","display_name":"PowerShell","input":{…},"description":"…","permission_suggestions":[…],"blocked_path":"…","decision_reason_type":"…","tool_use_id":"…"}}
→ {"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{"behavior":"allow","updatedInput":{…}}}}
→ {"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{"behavior":"deny","message":"…"}}}
```

- `AskUserQuestion`：`can_use_tool` 且 `requires_user_interaction:true`，`input.questions[]` 含 `question`、`header`、`options[{label,description}]`、`multiSelect`。允许时在 `updatedInput` 中追加 `answers:{"<question>":"<label>"}`，模型收到答案。
- `ExitPlanMode`：`can_use_tool` 且 `requires_user_interaction:true`，`input` 含 `plan`（Markdown）与 `planFilePath`。`allow` 表示批准计划，`deny` 的 `message` 作为修订意见返回模型。

### Interrupt

`→ {"type":"control_request","request_id":"<id>","request":{"subtype":"interrupt"}}` 得到 `control_response success {"still_queued":[]}`，随后输出 `user "[Request interrupted by user]"` 和 `result{subtype:"error_during_execution"}`。

## Update triggers

升级支持的 CLI 版本、重新录制 fixture、或写路径依赖新的消息/字段时，更新本文件与 fixture 目录，并在 [design.md](design.md) 中同步能力边界。
