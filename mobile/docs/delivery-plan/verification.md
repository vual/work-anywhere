# 缺陷修复验收

本文件记录当前修复的验证范围和待完成验收，不代表生产发布通过。功能设计见 [交付设计](design.md)、[协议同步](../protocol-and-sync/design.md)、[Desktop 控制面](../../../desktop/docs/local-control/design.md) 和 [Relay 配对](../../../relay/docs/identity-and-pairing/design.md)。

## 已通过的本地检查

| 模块 | 检查 | 结果 |
| --- | --- | --- |
| Desktop | bundled Go `test ./...`、`vet ./...` | 通过；含独立输出测试子进程、权限与默认批准参数回归 |
| Desktop | `scripts/build.ps1`、产物 `self-test` | 通过；只生成产物并执行自检 |
| Relay | `go fmt ./...`、`go test ./...`、`go vet ./...` | 通过；含实际 Desktop 默认 scope 的签名握手、旧授权子集和推送查询错误分类 |
| Mobile Go Core | `go test ./...` | 通过；含全禁用后启用、完整排序校验/恢复、请求代次和活动探测复用 |
| Flutter | `scripts/test.ps1`（analyze + tests） | 通过，93 项 Flutter 测试；含 Core/UI 偏好冲突、迟到工作台响应、保存失败、超限草稿可编辑 |
| Android | `scripts/generate.ps1`、`scripts/build-android.ps1 -Mode debug` | 通过；构建脚本检查实际 AAR 的 `reorderPairings(java.lang.String)` |
| 生成产物 | Pigeon 三端生成文件、AAR 跟踪状态 | Pigeon 无内容 diff；AAR 被 Git 忽略，不入库 |

权限 fixture 回归检查仓库两套协议共有的 sandbox/审批字段，不表示两套 App Server 的所有功能接口均兼容。`custom` 和旧记录缺失完整策略时保留实时线程权限；明确切换 workspace 模式才需要从有效项目配置构造策略。

## 未完成项与原因

- Relay `go test -race ./...` 已尝试：默认 CGO 关闭；显式启用后因 Windows 环境缺少 `gcc` 而无法编译。Docker 引擎未运行，也没有启动它来替代验收。需要在配置 C 编译器的隔离 runner 补跑。
- PostgreSQL 集成测试需要 `RELAY_TEST_DATABASE_URL`；当前未配置，数据库测试跳过。推送临时故障目前验证 worker 返回可重试错误，尚未验证真实数据库恢复后的 River 重试执行。
- 真实 Codex 回归需要隔离的 `WORK_ANYWHERE_TEST_CODEX` 和 `WORK_ANYWHERE_TEST_AUTH`。当前未配置，真实模型/权限会话测试跳过；`self-test` 仅确认 runtime 发现和本机基础能力，不替代真实会话验收。
- 未进行 Android 真机配对、排序重启恢复、弱网快速切换或真实 KeyStore 写入故障注入。APK/AAR 构建通过不替代这些验收。
- iOS、正式 APNs/FCM 与生产部署均不在本次实现范围。

## 人工验收与产物

Desktop 产物：`desktop/dist/windows-amd64/work-anywhere.exe`。Android 产物：`mobile/app/build/app/outputs/flutter-apk/app-debug.apk`。

使用隔离 Relay/PostgreSQL、测试授权材料和 Android 设备，按 Relay → Desktop → Mobile 顺序测试默认/旧授权配对、完整权限保留、计划确认、超限图片、快速切换和排序重启恢复。未安装、部署或重启正在使用的 daemon；产物供人工验证。
