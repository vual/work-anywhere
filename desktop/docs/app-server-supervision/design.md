# App Server supervision and runtime upgrades

## Goal and scope

daemon 为每个逻辑 Codex 实例托管一个官方 App Server，使 Desktop Bridge 断开后进程仍可服务本机控制客户端，并在官方 Codex 升级时安全切换 runtime。

## Current architecture and data flow

Bridge 注册实例后，daemon 获取实例 lease，按该 Bridge 的参数、cwd 和环境启动真实 `codex.exe app-server`（默认 stdio，保留显式 stdio 参数）。supervisor 拒绝一次性、未知命令和非 stdio 启动形态。stdin/stdout 由实例 supervisor 独占；所有 Desktop/control 请求经 supervisor 多路复用。

实例状态模型：

```text
stopped → starting → waiting_initialize → ready
       → active → upgrade_pending → restarting
       → error
```

- 首次 Desktop `initialize` 发给真实 App Server并缓存请求和响应。
- App Server 重启时用最新缓存参数自动 initialize。
- 新 runtime 注册且实例空闲、无待处理 action 时立即重启。
- 有活跃 Turn 或待处理 action 时标记 `upgrade_pending`，在安全边界后切换。
- App Server 异常退出时清理 pending request；实例空闲且存在初始化缓存时最多自动重启一次，使用 250 ms 延迟。活跃 Turn、待处理 action、待启动 Turn、未完成写请求、结果未知的写请求或已用过自动恢复机会时保持 `error`，不自动重放工作。
- Desktop Bridge 全部断开不会触发空闲回收。

## Interfaces

supervisor 接收 Bridge 的 WAI1 frames 和 daemon 控制请求，向 App Server 读写 JSONL。进程退出会产生状态和控制事件；显式实例重启、升级切换、异常恢复和 daemon 退出可结束子进程。

## Initialization and exit diagnostics

每代进程分别维护 initialize 成功缓存与 initialized 已发送状态。首次、并发 frontend 及自动恢复路径每代只写一次 initialized；initialize error 原样返回等待者，不进入 ready、不作为重连成功缓存。

stderr 每行和整个内存尾部均有 64 KiB 上限。非零退出的 `lastError` 保留退出错误及有界 stderr 尾部，`lastExitCode` 单独暴露真实退出码；stdout 读取失败诊断优先。新代次清空旧诊断，旧代次输出不能覆盖当前状态。结构化 `appserver.exited` 只记录实例、代次、阶段和退出码，不持久化 stderr、参数或认证内容。

## State and persistence

内存状态包括当前 runtime、候选 runtime、进程句柄、初始化缓存、Desktop 连接、活跃 Turn、待处理 action、请求路由和有界会话 projection。实例状态和 runtime 历史不持久化；daemon 重启后根据当前连接与 App Server 重新建立状态。

## Security and failure handling

- App Server 加入 Windows Job Object，配置 `KILL_ON_JOB_CLOSE`；daemon 退出或崩溃时由操作系统清理子进程。
- 实例 lease 与 fallback lease 互斥，避免同一逻辑实例的重复 writer。
- 启动失败进入 `error`，保留官方 session 文件和现有 JSON 配置，不覆盖原会话。
- Pipe 在请求执行中断开只移除 frontend，不立即终止 App Server。
- stdout JSONL 行上限为 64 MiB。超长或不可恢复读取错误按 generation 标记故障，保留具体错误，并直接终止该代子进程；终止不等待 stdin 写锁。旧 generation 的迟到错误不得影响新进程。
- stderr 每行最多保留 64 KiB 诊断文本，超出部分继续分段排空至换行，并标记截断；读取错误同样结束对应进程。两个 reader 结束后统一回收进程、结束等待请求并通知 Desktop/Mobile，避免管道写满后永久阻塞。
- 自动初始化成功不重置已经使用的自动恢复机会；业务写操作不重放。

## Invariants and non-goals

- daemon 是每个受管 App Server 的唯一真实 stdio 客户端。
- 一个实例不会并行运行旧、新两个受管 runtime。
- 不因 Desktop 窗口关闭或空闲时间自动回收 App Server。
- 不强制接管已经被另一个独立 writer 占用的官方 session。
- V1 不依赖官方 Windows 上不可用的 `app-server daemon` 生命周期模式。
- 所有写入 App Server stdin 的 JSONL 消息经过实例级串行写入锁。

## Code ownership

- `internal/daemon/instance.go`
- `internal/daemon/daemon.go`
- `internal/platform/process/`
- `internal/platform/identity/`
- `internal/codex/invocation.go`

## Verification

- Fake App Server 测试启动、initialize、退出、重启和并发响应；独立测试子进程验证超长 stdout 退出、等待请求完成、超长 stderr 后继续读取、旧 generation 和恢复门禁。
- Bridge 断开后 `instance list` 仍显示 `ready`。
- daemon 正常/异常退出后确认其 Job Object 中的测试 App Server 已结束。
- 升级测试覆盖空闲立即切换和活跃 Turn 后延迟切换。

## Documentation update triggers

修改状态机、进程参数、lease、Job Object/进程组、空闲策略、升级时机、初始化恢复或异常恢复策略时，必须更新本文档及平台支持、runtime、JSON-RPC、管理生命周期文档。
