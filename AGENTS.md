# Work Anywhere repository instructions

本文件适用于整个仓库。子目录若存在更具体的 `AGENTS.md`，应在不违反本文件约束的前提下补充模块规则。

## Repository layout

- `desktop/`：Windows Desktop Bridge、daemon、本机控制 API、CLI 和管理入口。
- `relay/`：Relay 服务模块；当前处于方案阶段，运行代码尚未实现。
- `mobile/`：iOS/Android 客户端；当前已建立产品、架构、协议同步、交互和交付设计，应用代码尚未实现。

各模块必须保持独立的构建入口、依赖定义、测试和设计文档。跨模块共享协议时，应明确协议的所有者，并在所有消费模块的文档中链接同一份规范，不能复制出互相漂移的版本。

## Required documentation entrypoints

开始修改代码前必须阅读与改动范围相关的入口文档：

- [`README.md`](README.md)：仓库级模块地图和构建入口。
- [`README.en.md`](README.en.md)：与根目录简体中文 README 内容对等的英文入口；修改项目介绍、架构、安装或使用方式时必须同步更新两份语言版本。
- [`desktop/README.md`](desktop/README.md)：Desktop 模块使用、构建和命令入口。
- [`desktop/docs/README.md`](desktop/docs/README.md)：Desktop 功能设计文档索引、文档归属和维护规则。
- [`relay/README.md`](relay/README.md)：Relay 模块目标、边界、技术栈和实施入口。
- [`relay/docs/README.md`](relay/docs/README.md)：Relay 功能设计文档索引、协议归属和实施顺序。
- [`mobile/README.md`](mobile/README.md)：Mobile 产品范围、技术方案、交互入口和实施阶段。
- [`mobile/docs/README.md`](mobile/docs/README.md)：Mobile 架构、协议同步、交互和交付设计索引。

新增顶层模块时必须创建并在本节明确列出：

1. `<module>/README.md`：模块用途、运行方式和最短上手路径。
2. `<module>/docs/README.md`：该模块全部功能设计文档的主索引。
3. `<module>/docs/<feature>/design.md`：每个功能一个目录，保存该功能的当前设计；协议、示例、图或 schema 等相关文件放在同一目录。

不能只创建文档文件而不把它加入所属模块的 `docs/README.md`；不能新增模块 README 而不把入口加入本文件。

## Documentation contract

每次新增、删除或修改功能点，文档是同一项工作的必要组成部分：

1. 从模块 `docs/README.md` 的功能索引确定主设计文档和受影响的关联文档。
2. 修改代码的同时更新所有受影响文档，包括跨功能的数据流、协议、安全边界、配置、失败处理和验收方式。
3. 新功能必须先建立 `docs/<feature>/` 目录和 `design.md`，再完成实现；若功能跨模块，每个模块都要有面向自身职责的设计说明。
4. 删除或重命名功能时，同步删除或移动失效文档，并修复所有索引和链接。
5. 提交前检查相对链接可达、命令可执行、名称与代码一致，并运行模块文档指定的验证命令。

设计文档只描述当前最新状态：

- 不写按日期排列的变更记录、版本流水账、`此前/现在` 对照或已失效方案。
- 不把计划中的能力描述为已实现；未实现但影响边界的内容应放在“非目标/尚未支持”中。
- 重要设计原因可以记录为当前约束和权衡，但不能演变为历史日志。
- 优先链接代码入口和权威协议文件，不大段复制易漂移的源码或生成 schema。
- 文档中的安全保证必须与实际实现一致；不能用目标状态代替当前实现。

## Feature document shape

功能主文档固定命名为 `design.md`，至少覆盖：

- 目标与范围
- 当前架构或数据流
- 外部/内部接口
- 状态、数据与持久化（适用时）
- 安全和失败处理（适用时）
- 不变量与非目标
- 代码归属
- 验证方式
- 哪些改动必须同步更新本文档

相关的 `protocol.md`、`schema.json`、示例、图和测试 fixture 可以与 `design.md` 同目录存放，并必须由 `design.md` 或模块 `docs/README.md` 链接。

## Desktop validation

Desktop 代码或设计变更至少执行：

```powershell
cd desktop
.\.tools\go-sdk\go\bin\go.exe test ./...
.\.tools\go-sdk\go\bin\go.exe vet ./...
```

涉及发布、资源、安装或进程入口时还必须执行：

```powershell
cd desktop
.\scripts\build.ps1
.\dist\windows-amd64\work-anywhere.exe self-test
```

## Relay validation

Relay 运行代码建立后，代码或设计变更至少执行：

```bash
cd relay
go fmt ./...
go test ./...
go vet ./...
```

`go fmt ./...` 后不能遗留未说明的格式改动。涉及连接注册表、并发路由或网络协议时还必须执行 `go test -race ./...`；涉及数据库、协议 schema 或发布镜像时，还必须执行对应设计文档定义的 migration、Buf compatibility、跨语言协议向量和容器 smoke test。

## Mobile validation

Mobile Go Core 或 Flutter 代码变更至少执行：

```powershell
cd mobile
.\scripts\test.ps1
```

Pigeon 接口或 Android Go Core 变更还必须在已配置 `ANDROID_HOME` 且接受本机 Android SDK 许可的环境执行：

```powershell
cd mobile
.\scripts\generate.ps1
.\scripts\build-android.ps1 -Mode debug
```

生成的 Pigeon 文件必须无 diff；`mobile/app/android/app/libs/mobilecore.aar` 是构建产物，不提交仓库。iOS XCFramework 与 no-sign build 需在 macOS runner 增加后执行，Windows 不将其标记为已验证。
