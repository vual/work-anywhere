# Relay wire protocol

> 状态：wire v1 protobuf schema 与 Relay/Desktop Go 生成代码已实现。

## 目标与所有权

Wire protocol 定义 daemon/Mobile 与 Relay 的 binary WebSocket 契约；密文内业务语义由 Desktop control protocol 负责。唯一 schema 所有者是 [`relay.proto`](../../proto/waw/relay/v1/relay.proto)，生成代码位于 Relay `gen/` 和 Desktop `internal/remote/gen/`。

## Encoding and frames

每个 WebSocket binary message 承载一个 `ClientFrame` 或 `ServerFrame`。顶层包含 `wire_major`、`wire_minor`、16-byte `message_id` 和 `oneof body`。

当前 families：

```text
server_challenge / client_auth / auth_ok
pair_start / pair_started / pair_join / pair_hello
pair_decision / pair_result / pair_cancel / pair_revoke / pair_revoked
session_open / session_handshake / session_data / session_close
route_ack / delivery_ack / protocol_error
push_register / push_unregister / notify
ping / pong
```

实现拒绝 text frame、错误 major 和 malformed ID。未知 protobuf fields 由标准 decoder 忽略；minor capability negotiation 和 Buf 基线 breaking check 尚未完整实现，因此 wire v1 仍应视为预发布协议。

## Routed envelope and E2EE

`session_open/handshake/data/close` 与 `delivery_ack` 都绑定 recipient、pairing/session 和 sender；Relay 以认证 socket 覆盖 sender。data 额外包含单调 sequence、traffic class、notification hint、created/expiry 和 ciphertext。

`EncryptedPayload` 在 endpoint 侧序列化后由 Noise 加密，绑定 sequence、content type、时间、pairing/session 和双端 device ID。Desktop 当前收发 `desktop-control-json-v1`；`attachment-chunk-v1` 在 reference crypto 中保留，但 Desktop remote adapter 当前通过 JSON attachment methods 处理分块，不接受独立 attachment content type。

Remote allowlist：

- `system.status`, `instance.list/get`
- `conversation.list/read/subscribe`
- `turn.start/interrupt`
- `action.list/resolve`
- `catalog.models/permissionProfiles`
- `attachment.begin/chunk/commit`

`system.shutdown`、traffic、UI、安装和未知方法由 Desktop 拒绝。

## Limits and delivery

- connect WebSocket frame：2 MiB；pairing frame：64 KiB。
- routed ciphertext：1 MiB。
- outbound socket queue：256 frames 或 4 MiB。
- socket ping：25 秒，write/ping timeout 10 秒。
- auth challenge：30 秒；pairing TTL：10 秒到 5 分钟；notify expiry：最多 1 小时。

route 是 at-most-once。`route_ack.accepted` 只表示 frame 进入目标内存队列；`delivery_ack` 可表示 endpoint 接收；业务完成只能由 E2EE response/event 表达。offline、queue full、expired 和 pairing failure 不持久化。

当前 server 对 auth/pairing/push 提供细分 error code；一般 route failure 除 queue full 外会折叠为 `temporarily_unavailable`。更完整稳定错误分类属于发布加固项。

## 兼容与安全不变量

field number 只能追加，不得重解释；删除字段必须 reserved。Relay schema 与 Desktop JSON 是独立版本域。协议不提供 HTTP fallback、SSE、离线 mailbox 或自定义 topic，也不承诺业务 exactly-once。

## 代码归属

- schema：`proto/waw/relay/v1/relay.proto`
- canonical transcript/constants：`internal/protocol/`
- generated Relay：`gen/waw/relay/v1/`
- generated Desktop：`desktop/internal/remote/gen/waw/relay/v1/`
- generation：`buf*.yaml`, `scripts/generate.ps1`

## 验证方式

运行 Buf lint/generate 并确认生成代码无 diff；单元/集成测试覆盖 protobuf、canonical signing、tamper、route/ACK、expiry 和 unknown/malformed frame。仍需跨语言 golden vectors、主分支 breaking baseline 和更全面 fuzz。

## 文档更新触发

修改 endpoint、frame/field、版本协商、content type、allowlist、限制、ACK/error 语义或生成策略时，必须更新本文档及所有消费模块文档和 vectors。
