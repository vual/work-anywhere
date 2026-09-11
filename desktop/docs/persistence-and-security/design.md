# Persistence, encryption, logging, and attachments

## Goal and scope

本功能为 daemon 保存可重连的会话投影和诊断信息，同时降低消息、工具输出、diff、审批内容和附件落盘后的泄露风险。它不替代官方 Codex session 存储。

## Current architecture and data flow

数据目录固定为 `%LOCALAPPDATA%\WorkAnywhere`，运行时文件、数据库、日志和可选流量记录分目录保存。SQLite 使用纯 Go driver、WAL、单写连接、`synchronous=NORMAL` 和 foreign key 支持。

数据库表：

- `instances`：稳定实例和当前 runtime 投影。
- `runtime_history`：按实例/SHA256 记录已见 runtime。
- `conversations`：状态、revision 和 sequence。
- `turns`：Turn 状态和加密 input/output 列。
- `timeline_events`：按会话 sequence 保存加密 payload。
- `pending_actions`：action 状态和加密内容。
- `attachments`：`attachment_id`、实例/项目/会话/消息关联、MIME、原始文件名、文件路径、大小和创建时间等元数据；新记录不写入附件正文。
- `settings`：加密设置值。

每个需要进入 SQLite 的敏感值先 JSON 序列化，再使用 AES-256-GCM 和随机 nonce 加密。32 字节主密钥随机生成后由 Windows current-user DPAPI 保护，密钥文件本身不保存明文主密钥。远程附件是例外：正文作为普通文件存放在对应项目目录的 `.work-anywhere-remote-attachments/<conversationId>/<messageId>/` 下，SQLite 只保存元数据和安全解析后的路径。

## Interfaces

Store 提供实例/会话/Turn upsert、Timeline append/read、action save/resolve、attachment metadata save/read 和历史附件 materialize。上层只传结构化对象，不直接拼接 SQL 密文。Timeline read 默认限制结果数量，避免一次载入无限历史。

结构化日志默认启用并轮转，只记录运行诊断和经过脱敏的字段。完整 JSONL 协议流量默认关闭，必须按实例显式启用，并受大小与保留策略约束。

## State and persistence

路径、实例 ID、项目/会话/消息 ID、状态、sequence、大小和必要索引元数据是明文。消息正文、工具 payload、diff、action 内容和设置值使用加密列；附件正文不进入数据库，直接落在项目目录普通文件中。旧数据库中的 `content_encrypted` 附件在首次使用时导出为 `legacy-<attachmentId>` 消息目录，成功后清空正文列。若项目根目录、会话 cwd 和实例 cwd 都不可解析，返回 `project_not_resolved` 且不写文件。完整进程环境不持久化。

## Security and failure handling

- DPAPI 绑定当前 Windows 用户；复制数据库和 key 文件到其他用户不能直接解密。
- AES-GCM 验证失败、nonce 截断或 DPAPI 解密失败时返回错误，不使用损坏明文继续运行。
- 数据库损坏或 migration 失败不能覆盖官方 Codex session 文件。
- 附件路径只允许位于项目根目录下的固定目录；会话 ID、消息 ID 和文件名拒绝目录穿越与路径分隔符。分块使用 `.part` 文件，commit 先校验实际大小再原子重命名；daemon 关闭和重启时清理当前或过期临时文件。
- 项目目录文件的安全边界是 Windows 用户对该项目目录的访问权限；它们不再享有 SQLite/DPAPI 的数据库加密保护。
- 日志脱敏覆盖常见 token、authorization、cookie、环境和敏感参数名称，但日志脱敏不是任意数据防泄露的替代品。
- 显式流量记录可能包含完整敏感内容，启用方必须把它视作高敏诊断数据。

## Invariants and non-goals

- 官方 Codex session 是完整会话权威来源；SQLite 是控制面投影和补发缓存。
- 每个会话 Timeline sequence 唯一且单调。
- 敏感 payload 不以明文列保存；附件正文明确不进入 SQLite，文件系统权限是其主要访问边界。
- V1 不实现跨设备数据库同步、云备份、用户自选密钥或数据迁移 UI。

## Code ownership

- `internal/store/`
- `internal/platform/secrets/`
- `internal/logging/`
- `internal/traffic/`
- `internal/platform/paths/`

## Verification

- AES-GCM/DPAPI round-trip 和错误密文测试。
- SQLite WAL migration、event append/read、action、附件元数据/文件落盘、历史正文迁移和无项目根目录失败测试。
- 日志脱敏单元测试。
- 流量记录默认关闭、显式打开后才增长文件的测试。

## Documentation update triggers

修改 schema、数据目录、加密算法、平台密钥保护、明文元数据范围、日志字段/轮转、流量保留或附件存储时，必须更新本文档和平台支持、控制/JSON-RPC 文档。
