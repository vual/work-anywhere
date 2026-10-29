# 缺陷修复验收

本文件记录当前修复的验证范围和待完成验收，不代表生产发布通过。功能设计见 [交付设计](design.md)、[协议同步](../protocol-and-sync/design.md)、[Desktop 控制面](../../../desktop/docs/local-control/design.md) 和 [Relay 配对](../../../relay/docs/identity-and-pairing/design.md)。

## 已通过的本地检查

| 模块 | 检查 | 结果 |
| --- | --- | --- |
| Desktop | bundled Go `test ./...`、`vet ./...` | 通过；含独立输出测试子进程、权限与默认批准参数回归 |
| Desktop | `scripts/build.ps1`、产物 `self-test`、`self-test --protocol` | 通过；目标 ChatGPT 26.1023.6621.0 / CLI 0.165.0-alpha.1.2，实际 EXE 一次性启动检查及隔离 initialize 握手通过 |
| Relay | `go fmt ./...`、`go test ./...`、`go vet ./...` | 通过；含实际 Desktop 默认 scope 的签名握手、旧授权子集和推送查询错误分类 |
| Mobile Go Core | `go test ./...` | 通过；含全禁用后启用、完整排序校验/恢复、请求代次和活动探测复用 |
| Flutter | `scripts/test.ps1`（analyze + tests） | 通过，98 项 Flutter 测试；含 Core/UI 偏好冲突、迟到工作台响应、保存失败、超限草稿可编辑，以及 agent capabilities 缺省、`externallyActive` 只读说明和审批 `detail` 展示 |
| Android | `scripts/generate.ps1`、`scripts/build-android.ps1 -Mode debug` | 通过；JDK 21、实际 AAR 的 `reorderPairings(java.lang.String)` 检查、Pigeon 无内容 diff；APK v2 签名验证通过，使用 Android Debug 证书 |
| 生成产物 | Pigeon 三端生成文件、AAR 跟踪状态 | Pigeon 无内容 diff；AAR 被 Git 忽略，不入库 |
| Desktop Claude Code | `go test ./internal/provider/... ./internal/daemon/... ./internal/remote/...`（连续 3 次） | 通过；假 CLI 子进程覆盖流式 delta 与持久化 ID 一致、环境净化、审批允许/拒绝与重复解析、提问答案、计划确认与修订、中断、崩溃、电脑端占用、新建会话、设置重启与空闲回收 |
| Desktop Claude Code | 真实 CLI 2.1.306 一次性冒烟（未入库的临时测试，经用户自定义网关） | 通过：新建会话流式回复、PowerShell 写文件审批后文件生成、`AskUserQuestion` 答案送达、计划修订、`fullAccess` 启动参数被接受，时间线 ID 与实时 ID 一致 |
| Desktop Codex | `TestCodexMobileLifecycleIntegration`，隔离 CODEX_HOME 与测试会话 | CLI 0.165.0-alpha.1.2 通过：创建项目和会话、空历史快照、两轮真实回复、流式 delta、控制端重连、App Server 重启后读取历史 |
| Flutter l10n | `flutter gen-l10n` | 已重新生成并提交 |

权限 fixture 回归检查仓库两套协议共有的 sandbox/审批字段，不表示两套 App Server 的所有功能接口均兼容。`custom` 和旧记录缺失完整策略时保留实时线程权限；明确切换 workspace 模式才需要从有效项目配置构造策略。

## 未完成项与原因

- Relay `go test -race ./...` 已尝试：默认 CGO 关闭；显式启用后因 Windows 环境缺少 `gcc` 而无法编译。Docker 引擎未运行，也没有启动它来替代验收。需要在配置 C 编译器的隔离 runner 补跑。
- PostgreSQL 集成测试需要 `RELAY_TEST_DATABASE_URL`；当前未配置，数据库测试跳过。推送临时故障目前验证 worker 返回可重试错误，尚未验证真实数据库恢复后的 River 重试执行。
- 真实 Codex 回归使用 `WORK_ANYWHERE_TEST_CODEX` 和 `WORK_ANYWHERE_TEST_AUTH`，仅对隔离 CODEX_HOME 与测试会话执行。上表已覆盖真实模型生命周期；权限交互、完整手机链路仍需独立验收。普通 `self-test` 只确认基础能力，`self-test --protocol` 只增加启动检查和初始化验证。
- 未进行 Android 真机配对、排序重启恢复、弱网快速切换或真实 KeyStore 写入故障注入。APK/AAR 构建通过不替代这些验收。
- iOS、正式 APNs/FCM 与生产部署均不在本次实现范围。
- `claudecode` 包的 `-race` 测试同样因无 cgo/`gcc` 未运行。真实 CLI 冒烟是一次性临时测试，仓库内没有可重复的 opt-in 真实 Claude Code 测试入口。
- 未进行 Claude Code 的物理手机端到端验收（手机 → Relay → daemon → `claude.exe`）。

## 人工验收与产物

Desktop 产物：`desktop/dist/windows-amd64/work-anywhere.exe`。Android 产物：`mobile/app/build/app/outputs/flutter-apk/app-debug.apk`。

当前构建来自本次 Codex 兼容修复源码，包含已提交的 Claude Code 接入。Android 包名为 `io.workanywhere.work_anywhere`，版本为 `1.0.0+1`；debug APK 为 249744097 字节，SHA-256 为 `5803f96be05efb72d8211a9aae4370659871edb7b1bd2935ffb5cb01f5da53f2`。Windows EXE 的 SHA-256 为 `067bcee1b91a5b5660609d1a691e88b90d1e3a38afc0bb5aaa53d315c524680d`。APK、AAR 与 EXE 是忽略的构建产物，不提交仓库。

使用隔离 Relay/PostgreSQL、测试授权材料和 Android 设备，按 Relay → Desktop → Mobile 顺序测试默认/旧授权配对、完整权限保留、计划确认、超限图片、快速切换和排序重启恢复。未执行 APK 真机安装与完整端到端验收；产物供人工验证。

Claude Code 人工验收：确认 PATH 中 `claude -p hi` 可正常回复；用新产物重启 daemon；在手机选择 Claude Code agent，核对项目与会话列表；新建会话并发送文字和一张图片；让它运行一条非 allowlist 的命令并在手机批准；回答一次 `AskUserQuestion`；在计划模式下确认与修订一次计划；中断一次长回复；在电脑端打开同一会话，确认手机显示电脑端使用中且不能发送；最后在电脑端 `claude --resume <id>` 检查完整记录。
