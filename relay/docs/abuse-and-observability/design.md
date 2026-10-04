# Abuse prevention and observability

> 状态：基础限流、硬上限、结构化日志 scrubber 和 Prometheus 文本指标已实现；生产压测、dashboard 和 trace 尚未完成。

## 目标与范围

Relay 必须限制 enrollment/pairing 猜测、连接和 frame 洪泛、通知轰炸与慢消费者，同时保证日志和指标不形成业务内容旁路。

## 当前保护

- enrollment 按 socket 源 IP 限流，pair/connect 在认证前按源 IP 限流。
- 认证后 frame 按 device ID 限流，notify 再按 daemon ID 限流。
- pairing session 使用随机 ID、五分钟以内 expiry、数据库原子计数且最多五次尝试。
- 每 device 一个 primary connection；新 epoch 替换并关闭旧连接。
- WebSocket 只接受 binary protobuf，读取大小、ciphertext 大小和过期时间有硬限制。
- 每连接发送队列最多 256 frames/4 MiB；队列满时关闭慢目标。
- token-bucket registry 本身有 entry 上限，避免攻击者制造无界 limiter state。

反向代理仍需提供 TLS、认证前连接数和源 IP 粗限流。`RELAY_MAX_CONNECTIONS` 限制认证后的 registry，跨分片新增使用原子容量预留。同设备连接替换不占新额度。`TRUSTED_PROXY_CIDRS` 中的直连代理才允许使用 X-Forwarded-For，解析整条链并从右向左跳过可信代理；非法链或未配置可信代理时使用直接 socket IP。所有注册、配对与连接入口共用该策略。

签名注册 challenge/完成分别按 IP 0.2/s burst 5，全局分别 10/s burst 50/20；challenge TTL、内存上限和设备总登记额度见 [注册设计](../daemon-enrollment/design.md)。限流返回 429 和 Retry-After。登记额度包含 revoked tombstone，已有 active 身份恢复不占新增额度。匿名攻击者可以更换密钥，这些限制只控制资源消耗，不证明用户身份。

## Logging and metrics

注册指标分别计数新建、恢复、限流及固定错误类别；标签不包含设备 ID、IP、公钥或用户输入。认证数据库读取暂时失败返回 temporarily_unavailable，客户端退避恢复；身份不存在、绑定错误或 revoked 才返回 auth_failed。

JSON `slog` 通过递归 scrubber 过滤 token、secret、key、signature、challenge、ciphertext、payload 和数据库 URL。允许记录 event 名、稳定错误类别、延迟、frame 长度和短标识；禁止记录 Prompt、conversation/project/title 和附件。

`GET /metrics` 输出低基数计数：连接、认证、配对、route、push 和 revoke 结果。endpoint 与 health 共用 listener，部署者必须在反向代理阻止公网访问 `/metrics`。当前没有 OpenTelemetry trace、dashboard、alert 或自动 SLO 计算。

## 失败与安全响应

revoke 会使数据库 pairing 失效、清除 ACL cache、通知在线双方并使后续 route fail closed。PostgreSQL cache miss 时授权失败；短 TTL positive cache 只维持已验证在线 route。应用不会记录被拒 frame 正文。

## 不变量与非目标

可观测性不能采集 E2EE plaintext/ciphertext。V1 不做 Prompt 审核、用户画像或商业 analytics。当前阈值是保守开发默认值，不是完成容量验证的生产 SLO。

## 代码归属

- limiter：`internal/limits/`
- logging/metrics：`internal/observability/`
- endpoint/frame enforcement：`internal/server/`, `internal/routing/`

## 验证方式

单元与集成测试覆盖 limiter burst/eviction、认证、pairing、queue full 和日志 canary scrub；网络并发改动运行 `go test -race ./...`。生产前仍需 slowloris、连接洪泛、10k socket、soak、dashboard/alert 与凭据泄漏扫描。

## 文档更新触发

修改公网 endpoint、限流键/阈值、frame/queue 上限、日志字段、metrics、proxy 信任、revoke 或安全响应时，必须更新本文档及相关 wire/部署文档。
