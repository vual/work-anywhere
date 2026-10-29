# Persistence, encryption, logging, and attachments

## Goal and scope

官方 Codex session 是会话、Turn 和 Timeline 的唯一权威来源。Desktop 不保存消息历史、delta、审批正文、Timeline、附件索引或运行时投影；daemon 重启后重新从 App Server 读取权威状态。

Desktop 只持久化重新连接所需的小型配置，并对运行内存、临时附件和诊断流量设置明确边界。

## Current architecture and data flow

配置固定写入 `%USERPROFILE%\.work-anywhere\config.json`。文件包含版本号和受保护的设置值，目前只有 Relay 地址、启停状态、设备名称、Desktop 身份、配对/capability，以及每个 instance/thread 的 mode、model、reasoning effort 和 permission 设置。

每个设置先序列化为 JSON，再用 Windows current-user DPAPI 保护并以 Base64 写入。写入在进程内互斥，使用同目录临时文件、flush 和 `MoveFileEx(REPLACE_EXISTING | WRITE_THROUGH)` 原子替换。写入失败不提交内存副本；损坏或版本不支持的配置在启动时返回错误，不会被新文件覆盖。thread 配置携带 `updatedAt`，只保留最近 2,000 条。

升级启动时删除旧数据目录中的 `work-anywhere.db`、`work-anywhere.db-wal`、`work-anywhere.db-shm` 和 `master.dpapi`。删除失败只记录告警并在下次启动重试；新版本不读取或迁移旧身份、配对、设置和消息投影。

## State and persistence

运行状态只在内存中存在：

- 每会话事件回放最多 256 条；所有回放 payload 合计最多 32 MiB。达到全局预算时淘汰最旧回放前缀并推进 replay floor，旧 cursor 得到 `snapshot=true`。
- 每个实例最多保留 256 个非活跃 conversation projection，按最近访问时间淘汰。活跃 Turn、待提交设置、待开始 Turn 和待审批 action 对应会话固定在内存中。
- revision、sequence、pending action、请求映射和 content reference 均在 daemon 进程结束时失效。

远程图片分块直接写入项目目录 `.work-anywhere-remote-attachments/<conversationId>/<clientMessageId>/`。附件 ID 到路径的索引只在内存中。App Server 明确接受 `turn.start` 或 `conversation.queue.add` 后删除文件和索引；失败或结果未知可保留以便同一进程重试，最多 24 小时。daemon 每 15 分钟清理内存已知的过期附件；重启后在下一次访问同一项目附件目录时清理遗留文件和 `.part`。

图片转 data URI 使用 daemon 全局单并发信号量。编码完成即释放文件内容和编码缓冲，避免多个 48 MiB 图片同时造成高峰内存。

## Interfaces

`internal/config.Store` 只提供小型设置的 get/put/delete。业务正文不能传入该接口。附件接口为 `attachment.begin/chunk/commit/abort`；路径只由 Desktop 根据可信项目根目录生成，Mobile 不提供本机路径。

结构化日志默认启用并轮转，只记录诊断字段。完整 traffic capture 是唯一允许持久化协议正文的显式诊断例外：默认关闭，daemon 重启后仍关闭，按既有大小限制轮转。`traffic.enable` 明确返回敏感内容警告；捕获文件可能包含 prompt、图片、工具输出、文件内容和凭据。

## Security and failure handling

- DPAPI 绑定当前 Windows 用户；复制 `config.json` 到其他用户不能直接解密设置。
- Base64、DPAPI 或 JSON 解码失败会返回错误，不使用部分配置继续运行。
- Relay 配置写入失败时保留旧配置和已授权配对；旧异步任务仍按 generation 隔离，不能写回新状态。
- 附件目录段拒绝路径分隔符、`.`、`..` 和控制字符；commit 校验声明大小后才原子改名。
- 项目目录临时附件受该项目的 Windows 文件权限保护，不承诺静态加密。
- 完整 traffic capture 由用户显式开启并视为高敏诊断数据。

## Invariants and non-goals

- Desktop 没有运行时 SQLite 依赖，也没有消息历史数据库。
- 配置文件不保存正文、图片、delta、Timeline、审批内容或附件索引。
- App Server 事件处理路径不执行配置写入。
- 不提供旧数据库迁移、跨设备配置同步、云备份、离线消息队列或附件永久缓存。

## Code ownership

- 配置和原子替换：`internal/config/`
- DPAPI：`internal/platform/secrets/`
- 有界运行投影和附件：`internal/daemon/`
- 日志与显式流量捕获：`internal/logging/`, `internal/traffic/`
- 路径：`internal/platform/paths/`

## Verification

- 配置 DPAPI round-trip、并发写入、原子重载、损坏/未知版本保护和 2,000 条 thread 配置淘汰测试。
- 旧数据库文件删除和失败重试行为测试。
- 每会话回放窗口、全局 payload 预算、旧 cursor snapshot 与非活跃 projection LRU 测试。
- 附件路径、大小、分块失败、24 小时清理、成功受理删除和图片编码单并发测试。
- traffic capture 默认关闭，并验证显式启用响应包含敏感数据警告。

## Documentation update triggers

修改配置 schema/路径、DPAPI、原子写入、内存限额、附件生命周期、日志字段、traffic capture 或权威数据来源时，必须更新本文档及控制、平台和 Relay connector 文档。

## 完整权限与附件请求限制

线程设置增加可选 `sandboxPolicyJSON` 字符串，随原有 DPAPI 配置加密保存；旧记录无需迁移，重建 workspace 权限前从项目有效配置补齐策略。字段不保存 developer instructions 或正文。

图片选择上限仍为 48 MiB，base64、文字和 JSON 包装后的完整 App Server JSONL 请求（含换行）不得超过 64 MiB。超限在写入前返回 `too_large`，不受理 Turn、不删除手机草稿；不通过提高协议行上限规避限制。验证使用 48 MiB 图片对应 base64 长度加 JSON 包装的边界请求。
