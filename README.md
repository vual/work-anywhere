<div align="center">
  <br />
  <img src="waw-app-icon.svg" alt="Work Anywhere icon" width="80" />
  <h2>Work Anywhere(WAW)</h2>
  <p><strong>简体中文</strong> | <a href="README.en.md">English</a></p>
</div>

Work Anywhere(WAW) 的目标是让你在手机上也能实时与电脑端 ChatGPT Codex Desktop 进行交互，让你离开电脑时，能够在手机上随时掌控任务运行状态，或者发出新的指令。

适合谁：
- 喜欢使用官方ChatGPT Codex桌面端，但是没有ChatGPT账号，或者你有账号，但是苦于网络问题，官方Remote功能时常连不上。
- 需要随时在手机上掌控电脑端ChatGPT Codex任务运行状态，并且需要能够派发新的任务。

项目由三个相互独立的模块组成：

- `desktop`：连接官方 ChatGPT Codex Desktop，托管官方 Codex App Server，并提供本机控制能力。
- `relay`：在 Desktop daemon 与移动端之间中继加密连接、状态和通知。
- `mobile`：查看任务进度、处理审批/问题、接收通知并继续输入。

目前只有 **Desktop V1** 已实现，Relay 和 Mobile 尚未实现，本文的安装部署步骤因此仅适用于 Windows Desktop。

## 当前能力

Desktop V1 提供：

- 使用 `CODEX_CLI_PATH` 接入官方 ChatGPT Codex Desktop，不替换官方 UI。
- 自动发现 Desktop 当前使用的官方 `codex.exe`，升级后按安全边界切换新 runtime。
- 一个 Windows 用户运行一个 daemon，每个 Codex 逻辑实例托管一个官方 App Server。
- 支持多个 ChatGPT Desktop 实例，按 Windows SID、`CODEX_HOME`、Electron user-data 和可选实例名隔离。
- 观察实例、会话、Turn 状态和增量事件。
- 从本机控制面发起文字/图片 Turn、指定模型、思考等级、审批和 sandbox 策略。
- 处理中途审批、用户问题和 Turn 中断。
- Desktop Bridge 断开后 daemon 继续持有 App Server；Desktop 重新连接时恢复初始化状态。
- SQLite 会话投影、附件存储、结构化日志和显式开启的完整协议流量诊断。
- 系统托盘、本机管理页和命令行控制工具。
- daemon 无法在请求前建立连接时直通官方 Codex，优先保证 Desktop 可用。

## 总体架构

```text
ChatGPT Desktop A ─► Bridge A ─┐
                               │  per-user protected Named Pipe
ChatGPT Desktop B ─► Bridge B ─┼──────────────────────────────┐
                               │                              ▼
Local CLI / Admin page ────────┘                       Desktop daemon
                                                             │
                                      ┌──────────────────────┼─────────────────────┐
                                      ▼                      ▼                     ▼
                              Codex App Server A      Codex App Server B      Encrypted SQLite
                                      │                      │
                                      └──── official Codex sessions ────┘

Future Mobile ◄──── encrypted connection / notifications ────► Future Relay ◄────► daemon
```

`work-anywhere.exe` 是一个多模式单文件程序：

```text
work-anywhere.exe app-server ...   ChatGPT Desktop 调用的透明 Bridge
work-anywhere.exe daemon run       常驻 daemon
work-anywhere.exe <command>        本机控制 CLI
其他 Codex 参数                    透传给真实 codex.exe
```

正式链路使用官方 App Server 的 stdio JSONL。Bridge 与 daemon 之间使用带版本和长度前缀的 WAI1 Named Pipe 协议；daemon 负责 JSON-RPC ID 隔离、初始化虚拟化、审批仲裁、App Server 生命周期和本机控制 API。

## 仓库结构

```text
work-anywhere/
├─ AGENTS.md                    项目开发与文档维护规则
├─ go.work                      Go workspace
└─ desktop/
   ├─ cmd/work-anywhere/        单 EXE 入口
   ├─ internal/
   │  ├─ bridge/                Desktop stdio Bridge
   │  ├─ daemon/                App Server supervisor 和控制面
   │  ├─ codex/                 Codex runtime、实例和参数处理
   │  ├─ jsonrpc/               JSON-RPC envelope 与 ID
   │  ├─ provider/              可扩展 Provider 接口
   │  ├─ store/                 SQLite 加密投影
   │  └─ platform/              Windows/macOS/Linux 平台适配边界
   ├─ packaging/                各平台安装、签名和发行配置
   ├─ docs/                     当前状态设计文档
   ├─ scripts/                  构建脚本
   └─ testdata/                 官方 App Server schema fixture
```

共享 Bridge/daemon 业务逻辑不按操作系统复制。系统差异集中在 `desktop/internal/platform/`，发行配置集中在 `desktop/packaging/<platform>/`。当前只有 Windows x64 可运行；macOS/Linux 仅有可交叉编译的适配骨架，详情见[平台支持设计](desktop/docs/platform-support/design.md)。

## 安装部署

### 环境要求

- Windows x64。
- 已安装并可正常运行官方 ChatGPT Codex Desktop。
- 使用仓库内现有 EXE 时不需要安装 Go；从源码构建需要 Go 1.26.5 Windows x64 SDK。
- 当前用户能够写入 `%LOCALAPPDATA%\WorkAnywhere`。

### 1. 获取或构建 EXE

仓库已有构建产物时使用：

```text
desktop\dist\windows-amd64\work-anywhere.exe
```

从源码重新构建：

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\scripts\build.ps1
```

构建脚本使用 `desktop\.tools\go-sdk\go` 中固定的 Go 1.26.5，运行测试后生成 Windows x64 单文件 EXE。

### 2. 安装前检查

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\dist\windows-amd64\work-anywhere.exe version
.\dist\windows-amd64\work-anywhere.exe self-test
```

`self-test` 应显示 `"ok": true`，并输出实际找到的官方 Codex 路径、版本、SHA256、`CODEX_HOME` 和 user-data。若找不到官方 Codex，可在当前环境显式指定：

```powershell
$env:WORK_ANYWHERE_CODEX_EXE = 'C:\path\to\ChatGPT\resources\codex.exe'
.\dist\windows-amd64\work-anywhere.exe self-test
```

### 3. 接入 ChatGPT Desktop

```powershell
.\dist\windows-amd64\work-anywhere.exe install
```

安装命令会：

1. 备份当前用户原有的 `CODEX_CLI_PATH`。
2. 将当前用户的 `CODEX_CLI_PATH` 指向本 EXE。
3. 广播 Windows 环境变量更新。

安装后必须完全重启每个 ChatGPT Desktop 实例，使其继承新的环境变量。多个 Desktop 实例可以共用同一个 `work-anywhere.exe`；daemon 会根据各自的 `CODEX_HOME` 和 user-data 分配不同实例。

如果使用外部启动脚本为不同 Desktop 配置 profile，可以额外设置：

```powershell
$env:CODEX_HOME = 'C:\Users\me\.codex-work'
$env:WORK_ANYWHERE_USER_DATA_DIR = 'C:\Users\me\AppData\Local\OpenAI\CodexProfiles\work'
$env:WORK_ANYWHERE_INSTANCE_NAME = 'work'
```

### 4. 验证运行状态

打开 ChatGPT Desktop 后执行：

```powershell
$wa = 'D:\CodeSpace\work-anywhere\desktop\dist\windows-amd64\work-anywhere.exe'
& $wa daemon status
& $wa instance list
& $wa ui
```

daemon 默认按需启动，不配置 Windows 登录自启动，也不会因 Desktop 窗口关闭或空闲而自动退出。`ui` 会打开仅监听 `127.0.0.1` 的本机管理页。

## 使用方式

以下示例假设：

```powershell
$wa = 'D:\CodeSpace\work-anywhere\desktop\dist\windows-amd64\work-anywhere.exe'
```

### daemon 和实例

```powershell
& $wa daemon start
& $wa daemon status
& $wa instance list
& $wa instance get <instance-id>
& $wa instance restart <instance-id>
```

### 会话和 Turn

```powershell
& $wa conversation list <instance-id>
& $wa conversation read <instance-id> <thread-id>
& $wa turn start <instance-id> <thread-id> '继续完成当前任务'
& $wa turn interrupt <instance-id> <thread-id> <turn-id>
```

本机控制客户端只能在会话空闲时开始新 Turn。Desktop 自身发出的原始请求仍由官方 App Server 判断是否存在并发冲突。

### 审批和问题

```powershell
& $wa action list <instance-id>
& $wa action resolve <instance-id> <request-id> '<json-result>'
```

`json-result` 必须符合该 action 对应的官方 response 结构。Desktop 和控制客户端可以同时收到 action，但只有第一个有效结果会提交给 App Server，后续回答会返回 `already_resolved`。

### 模型、权限和诊断

```powershell
& $wa catalog models <instance-id>
& $wa catalog permissions <instance-id>
& $wa traffic on <instance-id>
& $wa traffic off <instance-id>
& $wa call <control-method> '<json-params>'
```

完整协议流量默认关闭，因为其中可能包含 Prompt、工具输出、文件内容和凭据。只应在短期诊断时显式开启，完成后立即关闭。

## 数据与安全

运行数据位于：

```text
%LOCALAPPDATA%\WorkAnywhere
```

- 实例、会话、Turn、Timeline、pending action、附件和设置投影保存在 SQLite WAL 数据库。
- 消息正文、工具 payload、diff、action 和附件内容使用 AES-256-GCM 加密。
- AES 主密钥由当前 Windows 用户的 DPAPI 保护。
- Bridge 与 daemon 的 Named Pipe 使用当前用户 SID ACL。
- 管理页只监听随机 loopback 端口，并使用一次性 token、SameSite session、Origin 和 CSRF 校验。
- 完整进程环境不写数据库；完整 JSONL 流量默认不记录。
- 官方 Codex session 文件仍是完整会话的权威数据源，Work Anywhere 数据库只是控制面投影，损坏时不应覆盖官方 session。

## 停止与卸载

正常停止 daemon：

```powershell
& $wa daemon stop
```

存在活跃 Turn 时普通 stop 会拒绝退出；确认需要中断任务时使用托盘退出确认或：

```powershell
& $wa daemon force-stop
```

卸载 Bridge：

```powershell
& $wa uninstall
```

卸载会恢复安装时备份的用户级 `CODEX_CLI_PATH`。之后同样需要完全重启 ChatGPT Desktop。

## 故障排查

### 找不到官方 codex.exe

先执行 `self-test`，必要时设置 `WORK_ANYWHERE_CODEX_EXE`。Bridge 的发现顺序和多实例规则见[运行时与实例设计](desktop/docs/runtime-and-instance/design.md)。

### daemon 未运行

```powershell
& $wa daemon start
& $wa daemon status
```

结构化日志位于 `%LOCALAPPDATA%\WorkAnywhere\logs`。

### 会话提示 active writer

同一个官方 session 不能被两个独立 App Server writer 同时接管。只有通过本 Bridge 创建或连接的受管实例可以由 daemon 完整控制；项目不会强制抢占其他 Codex 进程已持有的 session。

### Desktop 升级后异常

重新执行 `self-test` 检查发现到的路径和版本。空闲实例会切换到新 runtime；活跃 Turn 会在安全边界后切换。若官方未来移除或改变 `CODEX_CLI_PATH`，需要更新 Desktop 接入层。

## 开发与验证

```powershell
cd D:\CodeSpace\work-anywhere\desktop
.\.tools\go-sdk\go\bin\go.exe test ./...
.\.tools\go-sdk\go\bin\go.exe vet ./...
.\scripts\build.ps1
.\dist\windows-amd64\work-anywhere.exe self-test
```

项目开发规则见 [`AGENTS.md`](AGENTS.md)。Desktop 当前设计和功能文档索引见 [`desktop/docs/README.md`](desktop/docs/README.md)。功能实现发生变化时，必须同步更新所有相关设计文档；设计文档只保留当前最新状态，不作为变更日志。

## 当前限制

- 运行支持仅限 Windows x64。
- Relay、Mobile、远程配对和移动推送尚未实现。
- 当前只有 Codex Provider；Claude Code、OpenCode、Hermes、Pi 等 driver 尚未实现。
- `CODEX_CLI_PATH` 是当前可用的 Desktop 接入点，但不保证官方永久保持兼容。
- 实验性 WebSocket 仅由 `app-server --standalone-ws` 用于诊断，正式 daemon 链路始终使用 stdio。
- 仓库中的 `desktop/codex-stdio-ws-bridge` 是旧 C# 回滚版本，不参与当前 Go 构建。
