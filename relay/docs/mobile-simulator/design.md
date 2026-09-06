# Browser Mobile simulator

> 状态：开发工具已实现，不属于生产 Mobile 应用。

## 目标与范围

`mobile-simulator` 为 Relay/Desktop 集成提供可人工操作的 Go reference client。它在浏览器中接受 Desktop 生成的 `waw://pair` URI，由本地 Go 进程完成真实 pairing、Ed25519 grant 校验、Relay 设备认证、Noise KK 握手和 Desktop control 请求。

## 当前架构与接口

`cmd/mobile-simulator` 启动仅绑定 loopback 的 HTTP 服务并提供嵌入式 HTML/CSS/JS。浏览器调用本地 simulator API；Relay 协议、设备私钥和 Noise session 都由 `internal/devmobile` 的 Go 代码处理，不在页面 JavaScript 中实现。

支持的联调路径：

- 粘贴并解析二维码 URI；
- 加入 pairing 并等待 Desktop 明确批准；
- 校验 daemon identity、pairing grant、scope 和过期时间；
- 连接 `/v1/connect` 并建立 Noise 会话；
- 发送 `desktop-control-json-v1` 请求、显示响应和事件；
- 接收 pairing revoke 并清理当前连接状态。

## 状态与安全

身份和 pairing 仅保存在 simulator 进程内，进程退出即丢失。页面只绑定 loopback，但该工具没有生产客户端需要的 Keychain/Keystore、设备证明、系统通知、后台连接、应用沙箱和发布签名，不能部署为真实 Mobile 客户端或公网服务。

## 失败处理与非目标

非 binary frame、签名/HMAC/Noise/sequence/expiry 错误会终止当前流程。daemon 离线或 pairing 被撤销时不缓存写命令。模拟器不测试 APNs/FCM 系统交付，不承诺移动浏览器后台存活，也不提供正式 UX。

## 代码归属

- 入口和 Web 资源：`relay/cmd/mobile-simulator/`
- reference client：`relay/internal/devmobile/`
- wire schema：`relay/proto/waw/relay/v1/relay.proto`

## 验证方式

启动 Relay、Desktop daemon 和 simulator，完成 pairing；通过 simulator 调用 `system.status` 并收到 Noise 加密响应，再验证未授权的 `system.shutdown` 被拒绝、revoke 后连接失效。

## 文档更新触发

修改 simulator API、状态持久化、pairing/Noise 行为、支持的 control 方法、安全边界或运行方式时，必须更新本文档、Relay README 和相关 wire/E2EE/Desktop connector 文档。
