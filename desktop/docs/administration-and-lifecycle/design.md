# Administration UI, tray, and process lifecycle

## Goal and scope

Desktop daemon 常驻运行，通过系统托盘、本机管理页和 CLI 提供 Agent 实例展示、启动设置、Relay 配置/配对、诊断及安全退出。daemon 可独立运行，不依赖 ChatGPT Desktop；可选随 Windows 登录启动；不提供聊天显示层。

## Current architecture and data flow

daemon 有三种启动入口：登录自启动项（HKCU `Software\Microsoft\Windows\CurrentVersion\Run` 的 `WorkAnywhere` 值，内容为 `"<exe>" daemon start --background`）、开始菜单 “Work Anywhere”（`work-anywhere.exe ui`，daemon 未运行时自动启动）、以及 Bridge 首次需要 daemon 时隐藏启动 `work-anywhere.exe daemon run`。`--background` 不输出、已运行时静默成功。EXE manifest 声明 `consoleAllocationPolicy=detached`，Windows 11 24H2+ 从资源管理器/自启动项启动时不分配控制台；旧系统忽略该元素，会短暂闪现控制台窗口。daemon 使用当前 SID 命名的互斥体确保单实例，并启动：

- 当前 SID ACL 的管理 Named Pipe。
- 随机 `127.0.0.1` 端口上的嵌入式管理 HTTP/WebSocket 服务。
- 原生 `Shell_NotifyIcon` 托盘图标；图标来自仓库根目录 `waw-app-icon.svg`，构建时优先用 Chrome 原生 SVG 透明背景渲染，缺少 Chrome 时使用透明背景的 `packaging/windows/icon.png` fallback，再编译为 Windows PE icon resource。托盘通过 `LoadImageW` 显式加载 48×48 资源以保证缩放清晰度；最终显示尺寸仍由 Windows 托盘和 DPI 设置决定。
- 用于托管官方 App Server、Claude 会话和诊断子树的 Windows Job 集合，每个受管根进程拥有独立 Job。

托盘菜单包括打开管理页、查看实例状态、暂停/恢复本机控制、重启错误实例和退出。管理页展示同一 `waw-app-icon.svg`、daemon、Agent 实例（Claude Code 与 Codex 分组）、runtime、日志和连接状态，以及“启动设置”（登录自启动开关、ChatGPT Codex Desktop 集成状态），并提供 Relay 配置、enrollment、配对 URI 复制、配对审批/取消/撤销；不展示聊天 Timeline 或 Prompt 输入。页面同时将该 SVG 作为浏览器页签 favicon。页头标题 “Work Anywhere” 右侧显示内联 SVG 的 GitHub 图标，链接到仓库 `https://github.com/vual/work-anywhere`，以新页签打开（`rel="noopener noreferrer"`），不加载外部资源，也不放宽管理页 CSP。

## Interfaces

管理页 Relay 区只有一个“启用远程访问”主按钮：保存地址后自动注册和连接；Relay 为 `invite` 模式时显示邀请码输入框，已填写时同一按钮在保存地址后用邀请码调用 `remote.register`，未填写则提示先填写。旧版注册入口仅在 `relay_upgrade_required` 时显示。状态展示注册阶段与重试时间，仅连接成功时允许生成二维码。操作结果与错误以页面顶部弹出提示显示：错误提示保留到用户关闭，成功提示数秒后自动关闭，相同内容不重复堆叠；Relay 状态错误同时在 Relay 区内联显示，且仅在错误内容变化时弹出，周期刷新不会重复弹出。切换地址和重置远程身份需要本机确认，清除配对 URI/QR 和旧身份后重新注册。管理权限与 CSRF/Origin 校验沿用下述本机边界。

- `work-anywhere.exe daemon start|status|stop|force-stop`
- `work-anywhere.exe ui`
- `work-anywhere.exe daemon start --background`、`work-anywhere.exe autostart enable|disable|status`
- 控制方法 `system.status`, `system.ui_url`, `system.control.pause/resume`, `system.shutdown`, `system.autostart.set`（参数 `enabled`，返回启动设置快照；拒绝 Mobile 调用，不在 Relay scope 白名单中）
- 管理 HTTP `GET /api/status` 在 `system.status` 结果上附加 `agents`（每个 backend 的状态、活跃 Turn、待处理审批，Claude Code 的配置目录/版本/诊断，以及未启用原因 `disabled`/`not_found`/`start_failed`）和 `startup`（自启动状态、`CODEX_CLI_PATH` 集成状态）。二者包含本机路径，只在 loopback 管理页返回，不进入 `system.status` 或 Mobile 投影。
- 管理页通过同一受保护 WebSocket 调用 `remote.configure`, `remote.enroll`, `remote.reconnect`, `remote.pair.*`；配对决定提交后页面显示等待状态，Relay 拒绝时恢复可重试状态。
- 托盘命令通过 daemon 内部调用执行，与 CLI 使用相同状态约束。

管理页 URL 包含一次性 fragment token。页面通过 exchange 获取 HttpOnly、SameSite session 后清除 fragment；后续状态请求和 WebSocket 连接校验 session、Origin 和 CSRF。

## State and persistence

daemon PID、启动时间、管理端口、一次性 token、session、托盘状态、控制暂停状态和实例/runtime 诊断投影都只保存在运行期。Relay 与 thread 配置保存在用户目录的 DPAPI 保护 JSON 中。登录自启动只持久化为 HKCU Run 键值；状态读取时比较其指向的 EXE 与当前程序（`current`）。不创建 Windows Service。

## Security and failure handling

- HTTP 仅绑定 loopback，不能作为 Relay 的公网监听端口。
- 一次性 token 被消费后不可重复换取 session；token 不放在 URL query 中。
- 正常退出时若无活跃 Turn，关闭 App Server并释放 Job Object。
- 有活跃 Turn（包括 [Claude Code provider](../claude-code-provider/design.md) 的 Turn；托盘实例列表也列出该 agent）时托盘显示原生确认框；确认后发送 interrupt，最多等待十秒，再结束 Job Object；取消则 daemon 继续运行。
- `force-stop` 是异常恢复手段，可能中断正在执行的 Turn。

## Invariants and non-goals

- 每个 Windows 用户最多一个 daemon 和一个托盘入口。
- Desktop 关闭不等于 daemon 退出；daemon 只在用户/CLI明确退出或进程异常时结束。
- Group 关闭后不得遗留已成功挂接的受管根进程及其后代。根进程退出清理自己的 Job；其余成员继续服务。Close 幂等并与 Add/退出清理互斥。
- 自启动只写当前用户 HKCU，不需要管理员权限；卸载必须删除该值。
- 不实现 Windows Service、自动空闲退出和桌面聊天 UI。
- 管理页顶部更新横幅（版本、更新说明、立即更新/跳过、下载进度与取消）、“启动设置”中的自动检查开关与镜像地址、托盘“检查更新”菜单和每版本一次的气泡通知，规则见 [Desktop 自更新](../self-update/design.md)。用户确认更新后，daemon 启动独立的安装器进程，再走与托盘退出相同的 graceful shutdown（有活跃 Turn 时须先确认中断）。

## Code ownership

- `internal/daemon/admin.go`、`internal/daemon/admin_agents.go`
- `internal/platform/autostart/`
- `internal/cli/autostart.go`
- `packaging/windows/work-anywhere.manifest`
- `internal/platform/tray/`
- `internal/daemon/web/`
- `internal/tools/iconrender/`
- `internal/platform/process/`
- `internal/daemon/daemon.go`
- `internal/cli/process_windows.go`

## Verification

- daemon 单例、start/status/stop/force-stop 测试。
- 管理页 token exchange、Origin/CSRF 和过期 session 测试。
- autostart 注册表读写（测试专用值名）、`daemon start --background` 参数解析、`agentSnapshots` 与 Claude Code 未启用原因测试。
- 人工：登录后任务管理器只出现 `work-anywhere.exe`；管理页切换自启动后 Run 键同步增删。
- Bridge 断开后 daemon/App Server 保持；正常退出后 Job 子进程清理。
- 有/无活跃 Turn 的托盘退出路径人工 E2E。

## Documentation update triggers

修改 daemon 启动方式、自启动键值/参数、单例策略、管理页内容/认证、托盘菜单、控制暂停、退出确认、interrupt 等待或平台子进程清理时，必须更新本文档及平台支持、控制面和 App Server 监督文档。
