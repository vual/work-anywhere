# Work Anywhere Mobile

> 状态：Android-first 实现已建立 Flutter 应用、Pigeon host、Go endpoint core、多电脑配对管理、工作台/项目/会话与受控写入代码路径；电脑管理支持锚定下拉切换、拖动排序、启用/禁用、名称与 Relay 地址编辑，并使用 Android Keystore 加密 catalog。会话详情使用稳定 timeline/catalog/settings/queue DTO、opaque cursor 分页和大内容按需读取；`request_user_input` 已使用稳定问题 DTO、多问题弹窗和待处理提醒，命令审批继续兼容允许/拒绝。尚未完成 Android 全量真机 E2E、正式签名、完整 approval DTO、iOS host、push、应用锁与发布验收，因此不能作为生产客户端发布。`relay/` 中的浏览器 `mobile-simulator` 仍只用于协议联调。

Mobile 是 Work Anywhere 的 iOS/Android 客户端。它不连接 Codex 云端，也不保存云端账号；每次通过已授权的 Desktop daemon 读取任务状态、继续已有会话、处理审批/问题、发送图片和中断运行中的 Turn。

## V1 product scope

- 一台手机配对多台 Desktop daemon，一台 Desktop 也可授权多台手机。
- 查看 Daemon/Agent 在线状态、Codex 项目、会话列表、完整时间线和实时增量；V1 优先完成 Codex，其他 Agent 先保留能力模型。
- 处理 approval/question、发送文字与图片、选择模型/思考等级/权限配置并中断 Turn；运行中的新输入进入 App Server 权威队列，可编辑、立即引导或取消；在项目内新建 Codex 对话。
- 接收只含通用提示的 APNs/FCM 通知；点击后重新建立 E2EE 会话并从 Desktop 同步权威状态。
- 在前台维持 WSS；退到后台后不依赖长连接或静默推送保证状态同步。
- 私钥保存在 iOS Keychain 或 Android Keystore 保护的本机密钥包中；V1 不把会话正文写入磁盘。

V1 不提供 Work Anywhere 账号、云端历史、离线命令、后台常驻连接、通知内直接审批、团队共享或 Codex 之外 Provider 的完整适配。Mobile 可在已同步的 Codex 项目内新建 thread；Hermes 的 `Bot`、Claude Code、OpenCode、Pi 等只保留 discovery/capability 边界。

## Architecture

```text
Flutter views / view models
            │
            ▼
repositories (daemons, agents, projects, conversations)
            │
            ▼
Pigeon typed bridge (commands + redacted state/events)
            │
            ├── Go endpoint core ── WSS / protobuf / Noise / control JSON
            ├── Swift host ──────── iOS Keychain / APNs / privacy overlay
            └── Kotlin host ─────── Android Keystore / FCM / lifecycle privacy handling

Mobile ◄════ Noise_KK E2EE over outbound WSS ════► Relay ◄════► Desktop daemon
```

UI 与应用状态使用 Flutter/Dart；endpoint core 使用 Go，并通过锁定版本的 `gomobile bind` 生成 Android AAR 与 iOS XCFramework。Core 复用 Relay protobuf、canonical transcript 和 Noise reference package，Flutter 只接收规范化 DTO 与连接状态，不接触 private key、protobuf frame 或 control raw JSON。Keychain、Keystore、push token、应用切后台遮罩和系统权限由 Swift/Kotlin host 封装。

完整技术边界见 [`docs/architecture/design.md`](docs/architecture/design.md)，连接和同步语义见 [`docs/protocol-and-sync/design.md`](docs/protocol-and-sync/design.md)，页面与交互见 [`docs/product-interaction/design.md`](docs/product-interaction/design.md)，实施顺序见 [`docs/delivery-plan/design.md`](docs/delivery-plan/design.md)。

## Workspace navigation

App 不使用底部导航栏。没有 pairing 时根页面直接展示添加配对；已有 pairing 时进入单一工作台，并按以下层级下钻：

```text
当前 Daemon（顶栏右侧下拉，恢复上次选择）
  └─ Agent（顶栏下一行横向切换，恢复该 Daemon 上次选择）
      ├─ Codex：最近项目 10 条 → 项目详情 → 最近对话 15 条
      ├─ Hermes：Bot 列表（后续适配）
      └─ 最近对话 30 条（跨项目、不分组）
          └─ 对话详情与交互

工作台内容底部：搜索项目与对话
```

Daemon 下拉项必须显示连接状态：连接中为 loading icon，在线为绿色点，离线为灰色点；状态同时提供可访问性文本，不能只靠颜色。顶栏中间优先显示待处理或运行中任务提示并可点击进入对应对话；无活跃任务时左侧仅显示 App icon。Agent 在顶栏下一行横向排列，支持左右滑动和直接切换。运行中的对话在列表右侧显示蓝色 loading icon。添加/管理配对、通知和隐私设置从 Daemon 下拉底部或页面 overflow 进入，不占用常驻导航。

会话详情页由“初始 snapshot + sequence 增量”驱动。任何 gap、过期 cursor、重连或不确定的写操作都会先重新读取 snapshot，不在本地猜测任务是否已执行。

## Repository layout

```text
mobile/
├─ README.md
├─ docs/
│  ├─ README.md
│  ├─ architecture/design.md
│  ├─ protocol-and-sync/design.md
│  ├─ product-interaction/design.md
│  └─ delivery-plan/design.md
├─ core/                        # gomobile-bindable Go endpoint facade
│  ├─ go.mod
│  └─ mobilecore/               # gomobile-bindable narrow API
└─ app/                         # Phase 1 创建 Flutter 工程
   ├─ lib/app/                  # bootstrap、routing、theme
   ├─ lib/core/                 # Pigeon bridge 与 normalized DTO
   ├─ lib/features/             # pairing、workspace、project、conversation
   ├─ pigeons/                  # typed host API source
   ├─ test/
   ├─ android/
   └─ ios/
```

Flutter 固定为 `3.47.2`，Go 为仓库 `1.26.5`，`x/mobile` 固定到 `v0.0.0-20260908204917-8b95e45f8d3e`；Dart 和 Gradle 依赖由提交的 lock/wrapper 固定。Android API 29 是最低版本。Android host 使用 Keystore AES-GCM wrapping 保存 Go Core 导出的 pairing credential，credential 不进入 Flutter/Dart；正文、action 和附件只保留在进程内存。

Flutter 文案已提供 `zh-CN` 与 `en-US` locale（并保留对应基础语言 fallback）；Android/iOS launcher icon 使用白底、黑色 WAW 连线和绿色中心点，应用内图标仍使用黑底、白色 WAW 连线和绿色中心点。启动图标的矢量源见 [`app/branding/launcher-icon.svg`](app/branding/launcher-icon.svg)。iOS 当前只有生成的 Pigeon 接口与权限声明，尚未接入 Go XCFramework/Keychain host，因此不属于可运行目标。

## Build and verification

```powershell
cd mobile
.\scripts\bootstrap.ps1
.\scripts\test.ps1
```

配置 `ANDROID_HOME`、接受 Android SDK 许可并安装对应 platform/build-tools 后，可生成 Go AAR、Pigeon binding 和 tester APK：

```powershell
.\scripts\generate.ps1
.\scripts\build-android.ps1 -Mode debug
```

CI 的 `Mobile` workflow 执行 endpoint/Core tests、Pigeon no-diff、Flutter analyze/test，并构建 debug-signed tester APK。正式 release signing 尚未配置。

### 真机扫码排查

二维码会原样携带 Desktop 当前配置的 Relay origin。`127.0.0.1` 和 `localhost` 只指向运行 App 的手机自身，不能用于真机配对；所有构建类型均可使用手机可访问的 HTTP/HTTPS IP 或域名，正式部署由运维选择 HTTPS。HTTP 是明文连接，使用时需确保网络环境可信。配对传输失败会留在 pairing 页面；grant 已批准但首次工作台同步失败时会保留 pairing，并在工作台显示重试提示。

## Cross-module contracts

- Relay wire、pairing、E2EE envelope 和 push frame 的唯一所有者是 [`relay/proto`](../relay/proto/waw/relay/v1/) 及 [Relay wire design](../relay/docs/wire-protocol/design.md)。Mobile 只提交生成代码，不复制 schema。
- 密文内的业务方法由 [Desktop local control](../desktop/docs/local-control/design.md) 所有，Mobile 通过 pairing scopes 限制展示和操作。
- Relay 不保存业务 ciphertext；Desktop/Codex session 是权威状态。Mobile 的内存 projection 可随时丢弃并重建。
- Desktop 已提供 `mobile-control-v1`、`agent.list`、`project.list`、`conversation.create`、project/conversation summary、分页 timeline、大内容引用、稳定 model/permission catalog、thread settings 更新和 `request_user_input` 问题 DTO；Desktop 管理页可生成真实 QR，Relay endpoint crypto 已成为公开复用包。完整 approval DTO、server-side project search 和跨产物 golden vectors 仍是发布门槛。

## Delivery summary

1. 固化 Mobile DTO、error/event contract、crypto vectors 和 QR 展示。
2. 建立 Flutter 工程、平台安全存储和“扫码—桌面批准—`system.status`”竖切。
3. 完成单工作台、多 Daemon/Agent 切换、Codex 项目/对话列表、搜索、会话详情和 cursor/snapshot 恢复。
4. 完成 approval/question、interrupt、文字/图片 Turn 与 catalog 选择。
5. 完成 APNs/FCM、前后台生命周期、隐私与真实设备发布验证。

每阶段的验收和阻断条件见 [delivery plan](docs/delivery-plan/design.md)。
