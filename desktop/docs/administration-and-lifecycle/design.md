# Administration UI, tray, and process lifecycle

## Goal and scope

Desktop daemon 以按需常驻方式运行，通过系统托盘、本机管理页和 CLI 提供 Relay 配置/配对、诊断及安全退出。它不随 Windows 登录自动启动，也不提供聊天显示层。

## Current architecture and data flow

Bridge 首次需要 daemon 时会隐藏启动 `work-anywhere.exe daemon run`；用户也可用 CLI 显式启动。daemon 使用当前 SID 命名的互斥体确保单实例，并启动：

- 当前 SID ACL 的管理 Named Pipe。
- 随机 `127.0.0.1` 端口上的嵌入式管理 HTTP/WebSocket 服务。
- 原生 `Shell_NotifyIcon` 托盘图标；图标来自仓库根目录 `waw-app-icon.svg`，构建时优先用 Chrome 原生 SVG 透明背景渲染，缺少 Chrome 时使用透明背景的 `packaging/windows/icon.png` fallback，再编译为 Windows PE icon resource。托盘通过 `LoadImageW` 显式加载 48×48 资源以保证缩放清晰度；最终显示尺寸仍由 Windows 托盘和 DPI 设置决定。
- 用于托管官方 App Server 的 Windows Job Object。

托盘菜单包括打开管理页、查看实例状态、暂停/恢复本机控制、重启错误实例和退出。管理页展示同一 `waw-app-icon.svg`、daemon、Codex 实例、runtime、日志和连接状态，并提供 Relay 配置、enrollment、配对 URI 复制、配对审批/取消/撤销；不展示聊天 Timeline 或 Prompt 输入。页面同时将该 SVG 作为浏览器页签 favicon。

## Interfaces

- `work-anywhere.exe daemon start|status|stop|force-stop`
- `work-anywhere.exe ui`
- 控制方法 `system.status`, `system.ui_url`, `system.control.pause/resume`, `system.shutdown`
- 管理页通过同一受保护 WebSocket 调用 `remote.configure`, `remote.enroll`, `remote.reconnect`, `remote.pair.*`；配对决定提交后页面显示等待状态，Relay 拒绝时恢复可重试状态。
- 托盘命令通过 daemon 内部调用执行，与 CLI 使用相同状态约束。

管理页 URL 包含一次性 fragment token。页面通过 exchange 获取 HttpOnly、SameSite session 后清除 fragment；后续状态请求和 WebSocket 连接校验 session、Origin 和 CSRF。

## State and persistence

daemon PID、启动时间、管理端口、一次性 token、session、托盘状态和控制暂停状态主要保存在运行期。实例/runtime 诊断投影持久化到数据库。V1 不创建登录自启动项或 Windows Service。

## Security and failure handling

- HTTP 仅绑定 loopback，不能作为 Relay 的公网监听端口。
- 一次性 token 被消费后不可重复换取 session；token 不放在 URL query 中。
- 正常退出时若无活跃 Turn，关闭 App Server并释放 Job Object。
- 有活跃 Turn 时托盘显示原生确认框；确认后发送 interrupt，最多等待十秒，再结束 Job Object；取消则 daemon 继续运行。
- `force-stop` 是异常恢复手段，可能中断正在执行的 Turn。

## Invariants and non-goals

- 每个 Windows 用户最多一个 daemon 和一个托盘入口。
- Desktop 关闭不等于 daemon 退出；daemon 只在用户/CLI明确退出或进程异常时结束。
- Job Object 关闭后不得遗留本 daemon 创建的 App Server。
- V1 不实现自动登录启动、Windows Service、自动空闲退出和桌面聊天 UI。

## Code ownership

- `internal/daemon/admin.go`
- `internal/platform/tray/`
- `internal/daemon/web/`
- `internal/tools/iconrender/`
- `internal/platform/process/`
- `internal/daemon/daemon.go`
- `internal/cli/process_windows.go`

## Verification

- daemon 单例、start/status/stop/force-stop 测试。
- 管理页 token exchange、Origin/CSRF 和过期 session 测试。
- Bridge 断开后 daemon/App Server 保持；正常退出后 Job 子进程清理。
- 有/无活跃 Turn 的托盘退出路径人工 E2E。

## Documentation update triggers

修改 daemon 启动方式、单例策略、管理页内容/认证、托盘菜单、控制暂停、退出确认、interrupt 等待或平台子进程清理时，必须更新本文档及平台支持、控制面和 App Server 监督文档。
