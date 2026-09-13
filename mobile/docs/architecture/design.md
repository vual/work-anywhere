# Mobile application architecture

> 状态：Flutter/Pigeon/Go Core 与 Android Keystore host 已实现首个代码竖切；Android artifact/真机与 iOS host 尚未验证，push、完整 lifecycle 和发布加固尚未实现。

## 目标与范围

在一套 iOS/Android 代码基线上实现安全、可恢复的 Desktop 远程控制客户端。架构优先保证协议一致性、移动平台生命周期正确性和可测试性，而不是追求后台常驻或离线写入。

## 技术决策

### UI 与应用层

- 使用 Flutter/Dart 构建 iOS 与 Android UI，采用 feature-first 目录和 MVVM：View 只渲染 immutable UI state，ViewModel 编排 use case，Repository 按 Daemon、Agent、Project/Bot、Conversation 聚合本地状态与远端 service。
- App shell 使用单工作台与系统返回栈，不建立底部导航状态；Daemon/Agent 是工作台上下文 selection，项目和会话是可恢复的下钻 route。
- 路由、依赖注入和状态容器使用锁定版本的 Dart package；具体 package 在 scaffold PoC 后写入 `pubspec.lock`，不得让其类型泄漏到 protocol/repository public interface。
- Flutter 到 host 的命令边界集中在 `app/pigeons/mobile_core.dart` 生成的 Pigeon typed channel，host 到 Flutter 的主动状态/事件通过专用 EventChannel 传递。业务 widget 不直接依赖 channel 字符串，也不接触 key bytes 或 protobuf frame；当前 timeline summary 已在 Desktop 结构化，完整句子由 Flutter 本地化，action/catalog 仍含受控的 provider-shaped payload。
- endpoint core 使用 Go，与 Desktop/Relay 保持同一仓库工具链；一个窄 `mobilecore` facade 通过锁定 commit 的 `gomobile bind` 生成 Android AAR 和 iOS XCFramework。Go 官方工具明确支持这两类 library artifact：[gomobile bind](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile)。
- Relay endpoint 的 canonical/protobuf/Noise 代码从 `relay/internal` 重构到可复用但不暴露服务端存储的 `relay/pkg/endpoint`；Mobile core 依赖该 package，Relay schema 仍是唯一所有者。生成脚本比较 schema hash，禁止手工编辑生成物。
- Android Kotlin host 已使用 Android Keystore 中不可导出的 AES key，以 AES-GCM wrapping 保存 pairing credential；导出和恢复发生在 native host 与 Go Core 之间，private key 不经过 Dart heap。Android manifest 禁止备份 credential ciphertext；Activity 启动时显式清除 `FLAG_SECURE`，并保留在最近任务列表中，因此系统截图、录屏和后台任务缩略图保持可用。Swift Keychain/APNs、Android FCM、应用锁和 app-switcher 敏感内容遮罩尚未实现。

Flutter 适合共享 UI 与业务逻辑，同时保留 Swift/Kotlin 平台接入点；Flutter 官方架构也明确支持 UI/data 分层与 platform channels：[app architecture](https://docs.flutter.dev/app-architecture/guide)、[platform channels](https://docs.flutter.dev/platform-integration/platform-channels)。

### Endpoint core、网络与密码

- 每个 active `PairingRuntime` 在 Go core 中持有一条前台 WSS，发送/接收 binary protobuf frame，禁用 application-level compression，并执行 Relay 定义的 frame/time/queue limit；Dart connection coordinator 只决定哪些 runtime 应活跃。
- Core 复用 Relay 的 Ed25519/X25519、canonical transcript、Noise KK 和 envelope 代码，不在 Dart 或 Swift/Kotlin 再实现一次密码协议。仅 `SecretStore`、随机数/时钟可测试接口与 redacted event sink 穿过 binding。
- `mobilecore` exported API 只使用 `gomobile` 可稳定绑定的 string/bytes/int/interface，并采用异步 command ID + callback，避免长网络调用阻塞 Flutter/platform main thread。
- AAR/XCFramework 真实产物仍必须逐项通过 golden vectors、tamper/replay/gap tests、binding fuzz 与独立安全审查；“复用 Go reference”不等同于发布审计。
- session、ephemeral key、明文 response/event 只在 Go core/Flutter 当前进程内存；断线、进入后台、revoke、sequence gap 或 decrypt failure 立即销毁 session。

### 备选方案与权衡

- 双原生 SwiftUI + Compose 能获得最直接的平台生命周期，但会复制 UI、reducers、DTO adapters 和大部分测试，V1 团队成本过高。
- 纯 Dart endpoint 减少一层 binding，却需要重新实现并审查 Noise/canonical protocol，风险高于复用现有 Go reference。
- React Native 仍需要原生 secret/push bridge，并额外引入 JavaScript runtime 与 crypto bridge；当前没有相对 Flutter 的明确收益。
- `gomobile` 增加 Go runtime、artifact size 和 AAR/XCFramework 构建复杂度，因此 Phase 0 必须测量启动时间、包体、callback/lifecycle 和 crash symbolication。若 PoC 不满足门槛，必须先重写本文档再采用纯 Dart/Rust core，不能在实现中暗中分叉。

## 内部 binding 接口

当前 Flutter 通过 Pigeon `MobileCoreApi` 发出 `pair`、`restoreStored`、credential 持久化/删除、通用 versioned control `call`、`status` 和 `close`；业务 method/DTO 位于 `mobile-control-v1` envelope 内。Android host 把 Go Core 的 `EventSink` 接到 `work_anywhere/mobile_events` EventChannel，主动发送 daemon event 和 transport state；iOS 事件宿主尚未实现。

Host 把 command 转给 gomobile facade；Go core 的 event sink 只返回两类 JSON envelope：metadata-only `type=event` 与连接生命周期 `type=transport`。endpoint 不把迟到 response 当事件，未注册 sink 时仅在内存保留最近 1000 条 daemon event。Dart controller 合并 event burst，并用 snapshot sequence 丢弃迟到结果。网络任务永不在 platform main thread 同步等待。

Go core 向 host 反向请求的窄接口只有：`SecretStore.load/save/delete`、应用私有文件读取和脱敏诊断 sink；host-only API 包含 `registerPushToken`，provider token 不经过 Flutter。Key bytes、完整配对 URI、provider token、ciphertext 和 control raw body 不可进入 `CoreStateEvent`。Core/Flutter API 与 Relay wire、Desktop control 是三个独立版本域，任何一层升级都要单独协商和测试。

### 本机存储

V1 持久化范围刻意保持最小：

- Android `PairingCredentialSet`：Mobile/pairing/daemon ID、Ed25519/X25519 private keys、双方 capability、scope 与 Relay origin；由 Keystore AES-GCM wrapping 后进入 app-private SharedPreferences。多 pairing record 与 Android backup exclusion 尚未实现。
- iOS `DeviceIdentityBundle`/`PairingRecord` 的 Keychain `ThisDeviceOnly` 存储尚未实现。
- 非敏感偏好：主题、通知提示状态、隐私遮罩、`lastSelectedDaemonId`、每个 Daemon 的 `lastSelectedAgentId`。

iOS 将密钥包保存在 Keychain 的 `ThisDeviceOnly` 可访问级别；Android 使用 Keystore 中不可导出的 AES key 对密钥包做 AEAD wrapping，硬件支持时使用 TEE/StrongBox。Apple 将 Keychain 定位为小型 secret/key 的安全存储；Android Keystore 可约束用途并阻止 key material 导出：[Apple Keychain](https://developer.apple.com/documentation/security/storing-keys-in-the-keychain)、[Android Keystore](https://developer.android.com/privacy-and-security/keystore)。

V1 不把 Prompt、消息、工具输出、diff、action 内容、附件或完整 snapshot 写入磁盘；退出进程后正文缓存消失。日志只允许连接阶段、稳定 error code、耗时、大小和匿名计数，不记录 URI、token、key、ciphertext 或 control body。

## 当前架构与数据流

```text
View ── intent ──► ViewModel ── use case ──► Repository
 ▲                    │                         │
 └── immutable state ─┘                         ├─ typed CoreBridge
                                               │      │ Pigeon commands / EventChannel events
                                               │      ▼
                                               │  Swift/Kotlin host ── secure store
                                               │      │
                                               │      ▼
                                               │  Go mobilecore ── pair/WSS/protobuf/Noise/control
                                               └─ PushIntentService (system notification entry)
```

一个 Go `PairingRuntime` 对应一条 daemon—Mobile pairing，拥有独立连接状态、Noise session、request ID namespace、cursor map 和 in-flight request map。endpoint 客户端为每个 runtime 维护单一重连门闩；并发请求共享一次重连尝试，失败后使用 500 ms 起、最大 30 s 的指数冷却，避免断线轮询把 Relay 连接限流放大。选中的 Daemon 保持前台连接；其余 pairing 由 coordinator 以有界短连接依次探测并缓存 `connecting/online/offline + checkedAt`，探测完成即关闭。切换 Daemon 时关闭旧常驻 session、递增 selection generation 并激活新 runtime，避免无上限 socket/battery 消耗和迟到事件串台。

Repository 中的权威划分：

- Desktop snapshot 是会话与 action 的远端权威。
- Go `PairingRuntime` 是连接/握手、request map 和 wire sequence 的内存权威；Dart Repository 是规范化 view projection/cursor 的内存 owner。
- Workspace ViewModel 恢复并持有当前 Daemon/Agent、项目展开页数、搜索 query 和滚动位置；切换 context 时只接受当前 selection generation 的结果。
- `ConversationController` 拥有 metadata、正序 timeline、older cursor、revision/sequence、分页与 snapshot/action/旧页局部错误；按 item ID 去重。事件触发最新页合并，既不覆盖已加载旧页，也不允许较低 sequence 覆盖新状态；内容引用过期时通过稳定 locator 重新签发，不刷新整页。

会话正文只存在 Dart/Go 进程内存。Desktop 对大 block 返回临时定位引用，Mobile 按 128 KiB 分块拼接查看；引用过期时使用 turn/item/block locator 重新签发，引用和正文都不持久化。`CoreGateway` 的 100 条内存诊断环只记录 method、耗时、结果、稳定 error code 和 request ID，Debug 构建将相同脱敏元数据写入 Logcat。

## 应用生命周期与通知

- `foreground`：恢复上次 Daemon/Agent，连接当前 pairing，并对其他 Daemon 做有界状态探测；鉴权、Noise、同步完成后才开放写操作。
- `inactive/background`：停止新请求，给已在发送中的 frame 一个短 drain deadline，然后关闭 WSS 和销毁 session。草稿可保留在内存，正文不落盘。
- `resumed`：始终新建 socket/Noise session并同步，不复用旧“在线”标记。
- `terminated`：仅依赖 APNs/FCM 通知提示。系统不保证推送或后台执行，因此 push 不能成为 correctness 前提。

通知 token 经已认证 Mobile socket注册。点击通知只携带本地选择的 notification class；由于服务端 payload 不含 conversation ID，App 打开上次 Daemon/Agent 工作台，完成全量轻量同步后再定位目标。通知页不提供 approve/reject 快捷动作。

## 安全与失败处理

- Mobile 不强制 Relay 使用 HTTPS/WSS；所有构建类型均支持 HTTP/WS 和 HTTPS/WSS，HTTP 的明文风险由部署环境和使用者承担，正式部署建议使用 HTTPS/WSS。
- 扫码内容在 grant 验证前是不可信候选；UI 只展示 Relay host、到期时间和 key fingerprint，不宣称已识别 Desktop。
- biometric/PIN 应用锁只保护 UI 访问，不参与 endpoint key 协议。应用锁和 app-switcher 敏感内容遮罩尚未实现；当前 Android 不设置 `FLAG_SECURE`，因此允许系统截图和录屏。未来若提供隐私增强选项，应只在用户启用或应用锁定时按状态设置窗口保护，不能把默认 Activity 永久标记为 secure。
- 只读请求在断线后可自动重试；写请求在 route/response 不确定时禁止自动重发，先同步并给出“结果待确认”。
- Relay `/v1/connect` 的 HTTP 429 被映射为稳定的 `rate_limited` 错误；重连冷却由 Go endpoint 统一执行，Flutter 的同步轮询不能绕过该冷却窗口。该机制只抑制重复拨号，不重放已路由的写请求。
- 已验证的 pairing credential 在首次 authenticated WSS/Noise 建链前即保留到 endpoint runtime；因此启动恢复或配对批准后遇到 429/离线时，普通重试仍可重连。配对批准后的 credential 即使首次建链失败也由 Flutter 持久化，并把 Daemon 显示为离线，而不是退回未配对状态。
- secure store 损坏或 key 丢失时 fail closed：删除不可解密的本地 pairing，提示重新配对，不向 Relay 请求私钥恢复。

## 不变量与非目标

- Relay 永远不成为 UI 状态或历史权威；route ACK 永远不显示为“操作成功”。
- scope 同时控制服务调用和 UI affordance；无 scope 的入口隐藏或只读，不能仅靠按钮禁用保障授权。
- 工作台层级固定为 Daemon → Agent → Project/Bot → Conversation；Codex V1 使用 Project，Hermes 后续使用 Bot，Mobile 不用同一个假实体混合两种语义。
- V1 不做后台常驻、离线命令、正文磁盘缓存、跨设备密钥同步、账号恢复、Web/Desktop 版 Flutter 或第三方 Provider 专属 UI。
- Mobile 不直接解释任意 Codex JSON。生产 UI 只消费 Desktop 发布并版本化的规范化 DTO；协议未冻结前只允许开发 diagnostics 展示 raw payload。

## 代码归属

当前路径：

- bootstrap/routing/theme：`mobile/app/lib/app/`
- feature UI/ViewModel：`mobile/app/lib/features/`
- DTO/core gateway：`mobile/app/lib/core/`
- gomobile facade：`mobile/core/mobilecore/`
- shared endpoint protocol implementation：`relay/pkg/endpoint/`
- typed core bridge source：`mobile/app/pigeons/mobile_core.dart`
- Android host：`mobile/app/android/app/src/main/kotlin/io/workanywhere/work_anywhere/`
- iOS host：`mobile/app/ios/Runner/`（Pigeon 生成接口已存在，真实 host 尚未实现）
- Mobile Go generated protobuf：通过 `relay/pkg/endpoint` 消费唯一 Relay schema

## 验证方式

- Go core unit tests：canonical/Noise vectors、sequence/replay、state machine、late callback、suspend/resume 和 request retry policy。
- Dart unit tests：DTO reducers、connection projection、selection generation、ViewModel 与错误映射。
- widget/golden tests：无配对根页、无底栏工作台、顶栏右侧 Daemon 状态下拉、左侧 App icon 与居中活跃任务提示、横向 Agent selector、项目/对话/搜索/详情的 loading/empty/offline/error/action states、动态字体、深浅主题和窄屏。
- integration tests：模拟 Relay/Desktop 的 pair/auth/Noise/snapshot/event/gap/revoke；真实 Relay + Desktop 端到端闭环。
- iOS/Android physical-device tests：Keychain/Keystore、重装、升级、锁屏、切后台、弱网、push token rotation、相机权限和通知权限。
- release gate：依赖/SBOM/secret scan、release build 中无 debug entitlement、日志与本机文件扫描无 fixture 明文；HTTP/WS 与 HTTPS/WSS 均为受支持的 Relay transport，部署方负责选择安全传输。

模块最少验证入口是 `mobile/scripts/test.ps1`。Android SDK 可用时运行 `generate.ps1` 与 `build-android.ps1`；macOS no-sign iOS build 尚未加入 CI，不能标记为已验证。

## 文档更新触发

修改 UI 框架、导航 shell、Daemon/Agent selection、Project/Bot capability、分页搜索、Go core/binding、状态分层、protobuf 生成、密码原语/Noise、平台 bridge、密钥存储、正文持久化、socket owner、后台策略、通知处理、日志或支持平台时，必须更新本文档及协议同步、交互、交付计划和相关 Relay/Desktop 设计。
