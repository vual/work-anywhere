# Codex Desktop compatibility baseline

## Target and evidence

目标为 Windows ChatGPT `26.1023.6621.0`、官方 CLI `0.165.0-alpha.1.2`。来源为该安装版本的启动代码、进程参数、启动日志、官方 CLI 的 help/协议导出及隔离握手探测；不是对未来版本或其他发布通道的保证。

权威资料为 [官方 App Server 文档](https://learn.chatgpt.com/docs/app-server) 和 [本版本导出 manifest](../../testdata/app-server-schema/0.165.0-alpha.1.2/manifest.json)。旧版 fixture 只用于验证接受旧结构；稳定与实验导出分别比较。

## Startup contract

1. 客户端解析 local/WSL/custom CLI 与 runtime。`CODEX_CLI_PATH` 指向 WAW 时属于 override。
2. 运行一次性 `app-server --print-startup-config --startup-client-info <JSON>`；恢复登录路径可能附带 `--startup-sign-out`。stdin 关闭，读取完整 stdout 和进程退出码。
3. 校验 `appServerVersion`、`configRequirements.requirements`，加载 `application.network`。必要时重新启动客户端以应用 Node 网络权限；协议错误不会当作普通网络瞬断重试。
4. 启动长驻 App Server。Windows 当前正式路径仍是 stdio；`CODEX_APP_SERVER_USE_LOCAL_DAEMON` 的客户端分支仅用于非 Windows。
5. `initialize` → 成功 response → `initialized`，随后使用业务接口。

典型长驻参数为 `-c features.code_mode_host=true app-server --analytics-default-enabled -c features.prefer_mxc=true`，还可能附带插件配置。WAW 不硬编码这些配置的值，保留选项顺序、重复 `-c`、环境与 cwd。

一次性启动检查和 `--listen` 互斥；不能把启动检查送入 WAW JSONL Bridge，也不能通过返回空组织策略消除错误。

## Wire compatibility

| Surface | Current contract | WAW behavior |
| --- | --- | --- |
| Transport | stdio JSONL；request/response ID 可为字符串或数字，wire 可省略 jsonrpc | 只改写 ID，保留未知字段与 trace |
| Initialize | experimentalApi、explicitGatewayOauth、extensions、MCP form、attestation、notification opt-out | 比较完整 capabilities；只有成功才可复用缓存 |
| Client infrastructure | currentTime/read、attestation/generate、account/chatgptAuthTokens/refresh、item/tool/call | 时间本地回答；其他由实际接收的 Desktop 回答，Mobile 不处理 |
| Assistant message | commentary、partial_answer、final_answer 及未知阶段 | 文字展示与 Turn 完成判断分离 |
| imageGeneration | inputImages、savedPath、status；不要求旧 result 字段 | 接受两代结构，只保留现有摘要 |
| subAgentActivity | 可携带 model、reasoningEffort | 不丢弃 Desktop 原始字段；Mobile 保持摘要 |
| Model provider capabilities | 不要求 namespaceTools | 不作为必要解码字段 |
| Notifications | prediction、read-state、OAuth/auth recovery 等 | 未专门投影的通知仍透明转发 |
| Thread metadata | thread/read 的 projectId 可为 null，cwd/environments 仍提供工作目录 | 会话详情按 provider 项目主目录恢复归属；idle 等 tagged status 保持非运行状态 |
| Other methods | feedback、attachment owner、gateway OAuth、sampling、verification 等 | 不基于白名单拦截，也不自动增加 Mobile 功能 |

`currentTime/read` 已存在于旧实验 fixture；不能根据稳定导出缺少该方法判断它是新功能。schema 方法存在不代表当前会话支持该能力；历史、队列等仍按真实调用结果判断。

## Non-goals and verification limits

不接管官方本地 daemon/proxy 或非 stdio 监听；不实现官方远控、语音、用户验证等新 UI；不改变 Relay、Pigeon 或 mobile-control-v1。

runtime 发现顺序保持现有设计。Windows App Package/Core/缓存副本的完整选择逻辑由官方客户端负责；WAW 不重建或伪造包身份，不保证所有未来 Core 布局。`--version` 可执行只证明基础可运行，不能替代协议检查。

自检与回归命令、真实模型 opt-in 测试及发布验收要求由 [主设计](design.md#verification) 维护。`self-test --protocol` 不等于 GUI 或手机真机 E2E，也不验证模型调用、登录退出或所有组织策略。
