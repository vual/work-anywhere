# Relay persistence, privacy, and retention

> 状态：PostgreSQL schema、migration、encrypted push token 和最小 Store 已实现；自动 retention/删除任务尚未实现。

## 目标与当前数据

Relay 只持久化认证、授权和通知所需数据：

- `devices`：ID、role、显示名、公钥、capability hash、状态和时间；
- `enrollment_tokens`：随机 token 的 SHA-256 hash、expiry、剩余次数和 revoke；
- `pairing_sessions`：daemon、expiry、attempt count 和状态，不含二维码 secret；
- `pairings`：daemon/Mobile、scope、grant hash、状态和 revoke 时间；
- `push_endpoints`：Mobile/platform/app/environment、AES-GCM token 密文、generation 和状态；
- `audit_events`：预留的最小安全事件表，当前业务路径尚未写入；
- River tables：metadata-only push job。

online registry、socket、Noise session、frame、ACK、Prompt、conversation、Turn、attachment 和业务 ciphertext 不写数据库。

## Encryption and secrets

Push token 使用 `RELAY_DATA_KEY` 的 AES-256-GCM，随机 nonce 和 endpoint binding AAD；key version 随行保存。当前只支持一个 runtime key，不支持在线 rotation 或 KMS。enrollment token 只在创建时输出一次，数据库保存 hash。APNs/FCM/database credential 由 runtime env/read-only file 注入。

## Retention and access

当前 migration 定义外键/cascade 和状态字段，但没有后台 retention cleanup、privacy export/delete API、PITR 或 device 管理 CLI。revoked pairing、expired session、finalized River job 和 audit row 会保留，直至运维数据库策略或后续 cleanup worker处理。生产部署必须显式制定并执行保留、备份和删除策略，不能把文档目标当成已自动执行。

Relay V1 不提供 transcript/export API；任何管理查询都不得返回可用 push token 或私钥。

## Failure handling

- enrollment 消费和 pairing 完成使用数据库 transaction，避免 token 重用或半配对。
- data key 缺失/长度错误时进程拒绝启动，不明文 fallback。
- pairing/revoke 写失败时操作失败关闭。
- PostgreSQL 不可用时 ready 失败；ACL cache miss fail closed。
- migration 失败不删除原表或业务数据。

## 不变量与非目标

数据库不得出现 messages、conversations、turns、attachments 或 ciphertext mailbox。V1 不提供云端历史、全文搜索、账号恢复和跨 Relay 数据迁移。

## 代码归属

- migration：`migrations/00001_initial.sql`
- Store/transactions：`internal/store/`
- push token encryption：`internal/secretbox/`
- River jobs：`internal/jobs/`

## 验证方式

数据库测试覆盖 migration、atomic enrollment、pairing claim/complete/revoke、active pairing lookup、encrypted endpoint upsert/delete 和 invalid token。容器 smoke 覆盖 migrate status。仍需补充 retention cleanup、device deletion、backup restore 和 data-key rotation。

## 文档更新触发

增加表/字段、改变明文 metadata、加密/key management、retention、backup、审计或删除语义时，必须更新本文档、架构、安全和运维说明。
