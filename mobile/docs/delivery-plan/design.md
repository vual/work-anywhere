# Mobile delivery plan

> 状态：Phase 0/1 的代码底座、分页 ConversationSnapshot/timeline/content DTO 和部分 Phase 2/3 UI 已落地，但所有 phase 的完整 exit criteria 均未满足；以下阶段仍是当前依赖顺序，不能据此宣称已公开测试或发布。

## 目标与范围

用可验证的纵向切片交付生产 Mobile，优先消除跨语言协议和状态语义风险，再扩展完整交互。任何阶段都必须保持 Relay 不解密、Desktop 为权威和 Mobile 不离线写入三个边界。

## Phase 0 — contract release gates

跨模块前置工作：

- 在 Desktop control 层定义并测试 `AgentSummary`、`ProjectSummary`、`ConversationSummary/Snapshot`、分页/搜索、typed timeline、`PendingAction`、catalog 和 stable error DTO；Codex instance 映射为 Agent，禁止 Mobile 产品代码解析任意 Codex raw JSON。
- 增加 Codex-first `agent.list`、`project.list` 和带 `agentId/projectId/query/limit/cursor` 的 `conversation.list` 规范化接口；预留 `navigationKind=bots`/`BotSummary`，但不把 Hermes 描述为已适配。
- 冻结 control DTO 版本协商与兼容策略；明确新增字段、unknown kind 和最低支持版本行为。
- 从 Relay/Go reference 导出 auth、pair proof/grant、Noise KK handshake/transport、replay/tamper 的语言无关 golden vectors。
- 把 Relay endpoint canonical/protobuf/Noise reference 重构为窄 `relay/pkg/endpoint`，为 schema 接入 Buf breaking baseline；schema hash 进入 Mobile core 构建检查。
- 完成 `gomobile bind` PoC：Android AAR/iOS XCFramework、Pigeon async callback、Keychain/Keystore callback、suspend/resume、crash symbol 和包体/启动性能均可接受。
- Desktop 管理页把现有 `waw://pair` URI 渲染为真实 QR，同时保留复制链接；二维码页显示与 Mobile 相同的短 fingerprint。
- 对 pairing、Mobile key wrapping、Go core binding、通知 metadata 和日志做 threat-model review。

验收：Desktop/Relay 当前测试继续通过；AAR/XCFramework 真实 core 逐字节通过所有 vectors；DTO contract tests 有真实 App Server fixture；Go core 的体积、启动、lifecycle 和 symbolication 达到记录的基线；阻断级 security finding 清零。Phase 0 未完成时不进入公开测试。

## Phase 1 — secure pairing vertical slice

- 创建 `mobile/core` Go facade 与 `mobile/app` Flutter 工程，固定 Go/`x/mobile`/Flutter/依赖、Android/iOS bundle identity 和 dev/prod flavors。
- 建立 MVVM/repository/service 骨架、Pigeon host/core 平台插件和脱敏日志。
- 实现 Keychain/Keystore protected identity、二维码扫描/粘贴、pair join/grant 校验、多 pairing 存储和 revoke。
- 实现 authenticated WSS、Noise session、`system.status`、连接状态机和前后台销毁/重建。

验收：真实 iOS/Android 与本机及公网测试 Relay 完成“扫码—Desktop 批准—E2EE status—revoke”；重装/升级/key corruption/错误二维码/过期/离线均 fail closed；HTTP/WS 与 HTTPS/WSS Relay transport 均可按配置连接。

## Phase 2 — read-only task experience

- 实现无底部导航的单工作台、上次 Daemon/Agent 恢复、顶栏右侧 Daemon 状态下拉、居中活跃任务提示、左侧 App icon 和下一行横向 Agent selector。
- 实现 Codex 最近项目 10 条与展开、跨项目最近对话 20 条、项目内最近对话 15 条与分页、项目/对话全集搜索和运行中蓝色 loading icon。
- 实现项目/会话详情和 typed timeline renderer；Hermes Bot 仅完成 capability/unsupported 状态，不纳入 Codex-first 发布范围。
- 实现 conversation snapshot/cursor、event reducer、gap/snapshot fallback、弱网退避和 selection generation。
- 完成 Markdown 安全 renderer（GFM 常用格式、选择/复制、链接确认和远程图片拦截）、diff/tool 安全 renderer、滚动跟随、stale/offline/error 状态与应用锁/隐私遮罩。

验收：无 pairing 根页和有 pairing 工作台无底栏；恢复/删除/切换 Daemon 与 Agent 正确；10/20/15 数量、cursor 分页、跨全部记录搜索和运行状态 icon 符合契约；长会话、1000 event 边界、streaming、duplicate/gap/late response、24 小时断网恢复不重复/丢序；widget/accessibility/golden tests 覆盖所有页面状态；正文不落盘。

## Phase 3 — controlled writes

- 实现 action center、approval/question schema、first-valid-wins 和 `already_resolved` 交互。
- 实现 interrupt 的 pending/terminal 状态。
- 实现 composer、catalog 选择、`expectedRevision`、`clientMessageId` 和 ambiguous-write reconciliation。
- 实现相册/相机选择、图片预览、begin/chunk/commit、取消与失败重试。

验收：两台 Mobile + Desktop 的 action race、turn.start response 丢失、interrupt/completion race、revision conflict、48 MiB/1 MiB 分块边界和跨 instance attachment 安全测试通过；抓包、Relay DB/log、Mobile log 不出现明文。

## Phase 4 — push and lifecycle

- iOS 直接 APNs token、Android FCM token 的注册/rotation/unregister；权限请求在用户理解价值后触发。
- 实现 generic/action/completion 通知、foreground suppression、cold/background/terminated click-to-sync。
- 完成 notification collapse、badge/stale、provider invalid token 和关闭权限的降级体验。

验收：Apple sandbox/production 配置与 FCM test project 真机验证；push payload/log/database 无 daemon/thread/action/正文标识；推送丢失或延迟不影响下一次前台同步；用户拒绝通知仍可完整使用在线功能。

## Phase 5 — release hardening

- 建立 Android/iOS CI：Go core test、analyze、unit/widget/integration、protobuf/core regenerate-no-diff、Android release 和 iOS no-sign build。
- 锁定依赖，生成 SBOM，执行 secret/dependency/license scan、崩溃脱敏检查和可复现构建记录。
- 完成弱网/电量/内存/大时间线性能、24h soak、VoiceOver/TalkBack、本地化和 upgrade migration。
- 准备隐私说明、加密出口合规信息、商店截图/元数据、签名与 staged rollout/rollback runbook。

验收：P0/P1 crash-free 与连接成功指标只含非敏感计数；无阻断 security/accessibility finding；内部、封闭、公开测试逐级通过，rollback 不删除用户 pairing key。

## Dependency graph

```text
normalized Desktop DTO ─┐
crypto golden vectors ──┼─► secure pairing slice ─► read-only sync ─► controlled writes ─► release
QR renderer ────────────┘              │                                  │
push provider credentials ─────────────┴──────────────────────────────────► push validation
```

Phase 2 不依赖真实 push；Phase 3 不得绕过 DTO/vector gate；Phase 4 可以与 Phase 3 后半并行开发，但正式验收必须使用可同步的 read-only client。

## 构建与验证入口

当前提供：

```text
mobile/scripts/bootstrap.ps1        安装/校验锁定 Flutter 与 generators
mobile/scripts/generate.ps1         生成 Android Go core AAR 与 Pigeon bindings
mobile/scripts/test.ps1             Go Core + Flutter analyze/unit/widget tests
mobile/scripts/build-android.ps1    Android tester APK
```

脚本使用明确的仓库相对输入，AAR 是忽略的构建产物，Pigeon 生成物提交并在 CI 做 no-diff。Android 生成/构建要求调用者已配置 `ANDROID_HOME` 并自行接受 SDK 许可；macOS iOS no-sign 脚本尚未建立。

## 风险与退出条件

| 风险 | 控制措施 | 阻断发布条件 |
| --- | --- | --- |
| Go core artifact 与 Relay/Desktop 不一致 | canonical vectors + artifact-level E2E + review | 任一 vector/tamper 用例失败 |
| gomobile 运行时/绑定不满足发布要求 | Phase 0 AAR/XCFramework 体积、lifecycle、callback、symbolication PoC | 任一平台无法稳定构建或恢复 |
| Mobile 绑定 provider raw JSON 或从 20 条对话猜项目 | Desktop Agent/Project/Conversation normalized DTO 与 server-side pagination/search | 产品路径仍解析 raw payload 或 client-side 推断 project identity |
| 大会话超过 Relay frame | metadata-only read、opaque cursor timeline、512 KiB control budget、可按 turn/item/block 重新签发的临时 contentRef | 10 KiB/900 KiB/1.5 MiB/13 MiB 真机会话任一无法进入或完整内容无法按需读取 |
| OS 后台限制导致假在线 | foreground socket + push hint + resume sync | UI 在旧 session 上允许写入 |
| at-most-once route 造成重复写 | no auto retry + clientMessageId + snapshot reconciliation | 弱网测试出现重复 Turn/action |
| key/正文泄露 | platform key wrapping、no body disk、scrubbed logs | artifact/log/db 扫描发现 fixture 明文 |
| 二维码被截取 | 短 TTL、Desktop 人工批准、fingerprint | 未批准即可建立 pairing |

## 不变量与非目标

交付压力不能降低 E2EE、scope、明确 Desktop 批准、写操作不重放和无正文 push 的要求。V1 不以 Web/PWA、browser simulator 或单平台 beta 代替 iOS/Android 生产验收；也不在本计划加入账号、离线 mailbox、多节点 Relay 或 Hermes/Claude Code/OpenCode/Pi 的完整适配。新建 thread 仅限已同步 Codex 项目，并遵循结果未知不自动重放。

## 代码归属

- Mobile：`mobile/core/`、`mobile/app/`、`mobile/scripts/` 与 `mobile/docs/`
- Desktop DTO/QR：`desktop/internal/daemon/`, `desktop/internal/provider/`, `desktop/internal/daemon/admin.go`
- Relay schema/vectors/push：`relay/proto/`, `relay/internal/protocol/`, `relay/internal/e2ee/`, `relay/internal/push/`
- CI/release：`.github/workflows/` 与未来 `mobile/app/tool/`

## 验证方式

每个 phase 的 exit criteria 是进入下一阶段的必要条件。跨模块改动分别运行 Desktop 和 Relay 的仓库规定验证；Mobile scaffold 后运行 analyze/test/build、真机 E2E 和生成物 no-diff。文档链接与命令在每个阶段结束时一并检查。

## 文档更新触发

阶段依赖、范围、Daemon/Agent/Project/Bot 模型、列表数量、分页搜索、Provider 优先级、发布门槛、技术栈、目标平台、CI、DTO/vector 策略或验收方式变化时直接重写本文档，并同步 Mobile README、相关功能设计与 Relay remaining plan。
