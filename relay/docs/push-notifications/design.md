# Mobile push notifications

> 状态：APNs/FCM adapters、加密 endpoint 存储和 River worker 已实现；真实 provider 凭据联调与正式 Mobile 注册尚未完成。

## 目标与范围

当 Mobile 不在线时，daemon 可请求 Relay 发送通用的“需要操作”或“任务完成”通知。Push 只用于提示，不承载会话内容或命令，也不改变 Desktop 的权威状态。

## 当前数据流

1. 已认证 daemon 发送 `notify`，指定已配对 Mobile、随机 notification ID、`action_required`/`turn_completed` hint、collapse key 和 expiry。
2. Relay 校验 sender role、active pairing、目标和限流。
3. Relay 查询目标 Mobile 的 active push endpoints，并为每个 endpoint 插入唯一 River job。
4. worker 解密 token，调用 APNs token-auth HTTP/2 或 FCM HTTP v1。
5. provider 判定 token invalid/unregistered 时 endpoint 被标为 invalid；瞬时错误由 River 重试，过期任务直接丢弃。

通知正文由 Relay 固定生成通用中文提示。job args 包含 endpoint ID 和 metadata，不包含 Prompt、conversation ID、审批内容或结果。provider 接受请求不等于系统实际展示。

## Endpoint registration and storage

已认证 Mobile socket 可发送 `push_register`/`push_unregister`。Relay 只允许 Mobile 管理自己的 endpoint。token 使用 `RELAY_DATA_KEY` 的 AES-256-GCM 加密，AAD 绑定 Mobile、platform、app ID 和 environment；旧 generation 不覆盖新记录。

APNs 配置为 `APNS_TEAM_ID`、`APNS_KEY_ID`、`APNS_KEY_FILE`；FCM 使用 `FCM_CREDENTIALS_FILE` 中的 service account JSON。凭据从 read-only 文件读取，不写数据库或日志。云 KMS envelope key 尚未实现。

## 安全和失败处理

- 日志 scrubber 不输出 token、credential、签名、payload 或数据库 URL。
- notify 按 daemon 做 token-bucket 限流，River unique key 防止同 endpoint/notification 重复入队。
- endpoint 查询返回 `pgx.ErrNoRows` 或 endpoint 已停用时结束任务；连接中断、超时等数据库查询错误返回稳定的可重试错误，由 River 重试。错误不带 token、正文或数据库细节，原有有效期、去重与最大次数不变。
- 未配置 provider 时对应任务失败且可观测，不回退到包含业务正文的其他通道。
- Mobile 点击通知后必须重新连接、Noise handshake 并向 daemon 读取状态。

## 不变量与非目标

Push 不是可靠业务队列、状态数据库或 command transport。V1 不支持短信、邮件、Web Push、富媒体和厂商私有 Android push。隐私模式、按 pairing 的精细通知策略以及正式 Mobile 后台行为尚未实现。

## 代码归属

- providers：`internal/push/`
- River worker/args：`internal/jobs/`
- endpoint persistence：`internal/store/`, `migrations/00001_initial.sql`
- wire frames：`proto/waw/relay/v1/relay.proto`

## 验证方式

单元测试覆盖 APNs JWT、FCM request、provider error 分类、worker expiry/dedup、endpoint 缺失/停用、临时数据库查询错误重试和 invalid token 清理；数据库集成测试覆盖 encrypted endpoint 与 job enqueue。Apple sandbox、FCM test project、前后台/被杀 Mobile 场景需要真实凭据和正式 Mobile 后补充。

## 文档更新触发

修改 notification class/payload、provider API、token storage、凭据、retry/TTL/dedup、隐私策略或新增平台时，必须更新本文档、wire、存储、部署和 Mobile 通知文档。
