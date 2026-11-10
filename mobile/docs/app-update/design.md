# Mobile 应用内更新

## 目标与范围

通过 GitHub Release 分发的 Android APK 能发现新的正式版本。用户在提示中点击“立即更新”后，App 下载 APK、完成校验，然后拉起系统安装确认界面完成覆盖安装；升级后保留配对 catalog 和本地偏好。

范围与边界：

- 只支持 Android release 构建（`minSdk = 29`），平台键为 `android-apk`。
- 更新来源、签名、字段和校验顺序以 Desktop 所有的 [更新清单协议](../../../desktop/docs/self-update/manifest.md) 为准，本文不复制字段定义。
- Android 规定侧载安装必须由用户在系统界面中确认，因此“自动更新”的含义是：App 自动完成下载、校验和发起安装，用户最后还要在系统对话框中点一次“安装”。

## 当前架构与数据流

```text
App 启动或回到前台（距上次检查 ≥ 24 h），或用户在关于页点“检查更新”
   │
   ▼
Flutter UpdateController ── Pigeon checkUpdate(mirrorBase) ──► Kotlin（versionCode、packageName）──► Go Core update
   │                                               │ HTTPS 拉清单 → 验签 → versionCode 比较
   ◄────────────── {state, latest} / 稳定错误码 ──────┘
   │ available → 自动检查时弹对话框；关于页内显示更新卡片
   │ 用户点“立即更新”
   ▼
Pigeon downloadUpdate ──► Go Core 下载到 cache/updates/<versionCode>/app.apk.part
   │                          size + SHA-256 校验 → 重命名为 app.apk
   │ EventChannel type=update 推送进度
   ▼
Pigeon installUpdate ──► Kotlin host
   ├─ Flutter 先调 canInstallPackages()：false → 关于页权限说明 → ACTION_MANAGE_UNKNOWN_APP_SOURCES → 回到前台后自动重试安装
   ├─ getPackageArchiveInfo：packageName、versionCode、签名证书 SHA-256 三项都必须匹配
   └─ PackageInstaller session 直接写入 APK 字节 → commit（不经 FileProvider）
          └─ UpdateInstallReceiver：STATUS_PENDING_USER_ACTION → 启动系统确认界面；结果以 type=update 事件回传
   ▼
系统完成覆盖安装 → 进程被替换 → 新版本冷启动 → host 删除 versionCode ≤ 已安装版本的 cache/updates 子目录
```

分层与 [Mobile 架构](../architecture/design.md) 保持一致：

- **Go Core**：负责网络、验签、版本比较、下载和 SHA-256 校验，与 Desktop 使用同一份清单测试向量。Dart 和 Kotlin 都不重新实现签名校验。
- **Kotlin host**：负责 Android 专有的安装权限检查、APK 签名比对和 `PackageInstaller`。
- **Flutter**：只负责状态展示和用户意图，不接触下载字节或文件路径以外的数据。

### 版本比较

- 当前 `versionCode`、`versionName` 和 `packageName` 由 Kotlin host 读取 `PackageInfo`；`checkUpdate` 时 host 把 `versionCode` 与 `packageName` 传给 Go Core，Go Core 拒绝 `packageName` 不一致的清单。
- 只有清单中 `android-apk.versionCode` 严格大于已安装版本时才视为有更新；`versionName` 只用于展示。原因是 `versionCode` 来自 `github.run_number`，即使通过 `workflow_dispatch` 重建同一个 tag 也会单调递增。
- 可调试构建（`ApplicationInfo.FLAG_DEBUGGABLE`）不检查也不安装更新，`appVersion().updatable=false`，`checkUpdate` 返回 `update_unsupported_build`：其签名与正式 APK 不同，本来就无法覆盖安装。非可调试但签名不同的构建由安装前的签名比对拒绝（`update_signer_mismatch`）。

## 接口

### Pigeon（`app/pigeons/mobile_core.dart`）

| 方法 | 说明 |
| --- | --- |
| `appVersion()` | 返回 `CoreReply`，result 为 `{versionName, versionCode, packageName, updatable}`；由 host 直接读取 `PackageInfo` |
| `checkUpdate(String mirrorBase)` | 返回 `CoreReply`，result 为 `{state: "upToDate" \| "available", latest?: {versionName, versionCode, size, notesUrl}}` 或稳定错误码；`mirrorBase` 为空字符串时用默认地址 |
| `downloadUpdate(int versionCode)` | 在 `Dispatchers.IO` 上阻塞到下载完成，返回 `{path}` 或错误；必须先对同一 `versionCode` 成功 `checkUpdate`。进度通过 `type=update` 事件返回 |
| `cancelUpdateDownload()` | 取消下载；被取消的调用返回 `update_cancelled` 并删除 `.part` 文件 |
| `canInstallPackages()` | 返回 `PackageManager.canRequestPackageInstalls()` 的结果 |
| `openInstallPermissionSettings()` | 打开本应用的“安装未知应用”设置页 |
| `installUpdate(int versionCode)` | 校验 APK 后提交 `PackageInstaller` session；成功只表示已提交，最终结果走事件 |

Dart 侧 [`CoreGateway`](../../app/lib/core/core_gateway.dart) 解包 `CoreReply.result`，失败抛出带稳定错误码的 `CoreFault`。

`type=update` 事件与 daemon 事件共用同一个 EventChannel sink，但不附加 `daemonId`/selection generation，也不经过电脑过滤：

| `state` | 来源 | 字段 |
| --- | --- | --- |
| `downloading` | Go Core，最多每 500 ms 一次 | `received`、`total` |
| `downloaded` | Go Core | `received`、`total` |
| `error` | Go Core（下载）或 Kotlin（安装） | `error: {code, message}` |
| `installing` | Kotlin，session 已提交 | — |
| `installed` | Kotlin，`STATUS_SUCCESS` | — |

`CoreGateway.events` 缓存单个 broadcast stream。原因是每次 `receiveBroadcastStream()` 都会替换 native 侧的 handler 和 sink，多次订阅会让先订阅者收不到事件；`updateEvents` 在其上过滤 `type == "update"`。

### Android manifest 与 host

- 声明 `android.permission.REQUEST_INSTALL_PACKAGES`。
- 不使用 `FileProvider`：[`UpdateInstaller.kt`](../../app/android/app/src/main/kotlin/io/workanywhere/work_anywhere/UpdateInstaller.kt) 把 APK 字节写入 `PackageInstaller` session，APK 不离开应用私有缓存，也不需要 content URI。
- 不导出的 `UpdateInstallReceiver` 接收 session 提交结果：`STATUS_PENDING_USER_ACTION` 时启动系统确认 Intent；`STATUS_SUCCESS` 发 `installed`；`STATUS_FAILURE_ABORTED` 映射为 `update_install_cancelled`；`STATUS_FAILURE_INCOMPATIBLE` 等签名冲突映射为 `update_signer_mismatch`；其余映射为 `update_install_failed`。
- `MainActivity` 启动时删除 `cache/updates/` 下 `versionCode` 不大于已安装版本的子目录。

### 交互

- **自动提示**：App 首帧渲染后和每次回到前台时（`UpdatePrompter` 的 `AppLifecycleListener`），若距离上次检查已超过 24 小时，就静默检查一次；失败不弹窗。发现新版本且该版本未被跳过时，弹出对话框，包含版本号、下载大小、“查看更新说明”（只用系统浏览器打开 https 的 `notesUrl`），以及“立即更新”“稍后”“跳过此版本”三个按钮。“立即更新”打开关于页并开始下载，让用户能看到进度。
- **手动入口**：点击工作台左上角 App icon 打开左侧侧边栏，里面的“检查更新”和“关于”都进入关于页 `/about`。区别是“检查更新”带 `check=1` 进入，页面打开后自动执行一次检查。侧边栏和关于页的布局归属 [产品交互](../product-interaction/design.md#侧边栏与关于页)。电脑下拉菜单里不放更新相关入口。
- **关于页中的更新状态**：检查中显示进度；已是最新时显示“已是最新版本”；有新版本时直接在页面内显示更新卡片（版本号、大小、“查看更新说明”“立即更新”“跳过此版本”），不再额外弹对话框；失败时显示错误文案和“重试”。“更新镜像地址”放在页面底部可折叠的“高级”区域。
- **新版本标记**：自动或手动检查得到未跳过的新版本后，工作台左上角 App icon 显示小红点，侧边栏“检查更新”项显示“有新版本 x.y.z”。升级完成或跳过该版本后清除。
- **下载中**：显示进度和“取消”；正在发送消息、等待 action 等会话状态不受影响。
- **权限引导**：下载完成后若未授权安装未知应用，关于页显示说明和“去设置”；用户从设置返回前台时自动重试安装，仍未授权则停留在说明页并保留已下载的 APK。
- **结果**：用户在系统界面取消，回到“可安装”状态，可以再次点击安装；校验失败时显示可理解的文案，并提供“重新下载”。

## 状态、数据与持久化

- 非敏感偏好保存在 SharedPreferences：`update.lastCheckedAt`、`update.skippedVersionCode`、`update.mirrorBase`。
- APK 只存放在应用私有的 `cache/updates/<versionCode>/` 下，同一时间只保留一个目标版本。下次启动时，若已安装版本 ≥ 目标版本，或者 APK 校验失败，就删除该目录。
- 升级使用同一 `applicationId` 和同一签名证书，Android Keystore 中的 wrapping key 和 app-private SharedPreferences 会被保留，因此配对 catalog 不需要迁移。
- 清单内容和下载进度只保存在内存中。

## 安全与失败处理

- 清单的验签、防回滚和下载 SHA-256 校验规则见 [清单协议](../../../desktop/docs/self-update/manifest.md#client-verification)，镜像不被信任。
- 安装前，Kotlin host 用 `getPackageArchiveInfo(..., GET_SIGNING_CERTIFICATES)` 再做一次独立校验：
  - `packageName` 与当前应用一致；
  - `versionCode` 与清单一致，且大于已安装版本；
  - 签名证书 SHA-256 与已安装应用的证书、以及清单的 `signingCertSha256` 一致。

  任一不符合时返回 `update_signer_mismatch`，不提交安装。系统本身也会拒绝签名不同的覆盖安装，这一步是为了在用户看到系统对话框之前就给出明确错误。
- 下载在进程存活期间持续进行，App 切到后台不会主动暂停；进程被系统回收或网络失败时保留 `.part`，下次对同一版本下载时用 HTTP Range 续传（已有部分超过清单 `size` 时丢弃重下）。只有用户取消才删除 `.part`。不使用 `DownloadManager`，避免 APK 写入共享存储。
- 已下载 APK 与清单字段的对应关系只保存在 host 内存中；进程重启后需要先检查、再调用 `downloadUpdate`，已完整且哈希匹配的文件会被直接复用，不重新下载。
- 下载、校验或安装失败时，不会自动重试安装；只读的检查请求可以在下次前台或手动操作时重试。
- 日志只记录状态、错误码、字节数和耗时，不记录完整 URL 或文件路径。

Mobile 的稳定错误码：`update_permission_required`、`update_signer_mismatch`、`update_install_cancelled`、`update_install_failed`、`update_cancelled`（用户取消下载）、`update_unsupported_build`（可调试构建）、`update_in_progress`（已有下载在进行）、`update_download_failed`；清单相关错误码沿用 [清单协议](../../../desktop/docs/self-update/manifest.md#client-verification)。

## 不变量与非目标

- 没有用户点击“立即更新”，不会下载 APK；没有用户在系统界面确认，不会安装。
- 只会升级到 `versionCode` 更高、且签名证书相同的 APK。
- 升级不清除配对 catalog，也不撤销 Desktop 侧授权。
- 非目标：
  - **iOS**：只能通过 App Store/TestFlight 更新，未来最多提供跳转链接。
  - **Google Play 渠道**：`REQUEST_INSTALL_PACKAGES` 是 Play 的受限权限；如果以后上架 Play，需要单独的 flavor，去掉本功能和该权限，改用 Play In-App Updates。
  - 后台或静默安装（需要设备所有者权限）。
  - 增量更新、prerelease 通道、强制更新。
  - 提示 Desktop 升级、或检查 Desktop/Mobile 版本兼容性。

## 前置条件

- [Release workflow](../../../.github/workflows/release.yml) 的 `android` job 在缺少 release keystore secret 时直接失败，不再发布 debug 签名 APK；它通过 job outputs 把 `versionCode` 和签名证书 SHA-256 传给 `update-manifest` job，见 [清单协议的 Generation](../../../desktop/docs/self-update/manifest.md#generation)。
- 客户端内置的可信公钥与 Release 签名密钥必须一致，否则任何清单都会以 `update_signature_invalid` 被拒绝；见 [清单协议的 Signing keys](../../../desktop/docs/self-update/manifest.md#signing-keys)。
- 已经用 debug 签名分发出去的 APK 无法通过本功能升级到正式签名版本，用户需要卸载后手动安装一次。

## 代码归属

- Go Core：[`core/update/`](../../core/update/)（清单、验签、Android 选择、下载与续传、缓存清理），[`core/mobilecore/update.go`](../../core/mobilecore/update.go)（facade 方法、独立下载锁、`type=update` 事件）
- Pigeon 定义：[`app/pigeons/mobile_core.dart`](../../app/pigeons/mobile_core.dart)，生成物 `app/lib/core/mobile_core.g.dart`、`MobileCore.g.kt`、`MobileCore.g.swift`
- Kotlin host：`MainActivity.kt`（`ReflectiveMobileCore` 的更新方法）与 [`UpdateInstaller.kt`](../../app/android/app/src/main/kotlin/io/workanywhere/work_anywhere/UpdateInstaller.kt)（版本、权限、签名比对、`PackageInstaller`、结果 receiver）
- Android 配置：[`AndroidManifest.xml`](../../app/android/app/src/main/AndroidManifest.xml)
- Flutter：[`features/update/`](../../app/lib/features/update/)（`UpdateController` 状态机、关于页更新区块与对话框、前台自动检查 `UpdatePrompter`），[`features/about/`](../../app/lib/features/about/)，侧边栏 [`features/workspace/app_drawer.dart`](../../app/lib/features/workspace/app_drawer.dart)，路由 `/about` 在 [`app/router.dart`](../../app/lib/app/router.dart)
- Release：`.github/workflows/release.yml`

## 验证方式

- **Go Core 单元测试**：读取 Desktop 的清单 `vectors.json`，覆盖验签、篡改、未知 `keyId`、`versionCode` 比较、size/hash 不匹配、Range 续传和取消。
- **Dart 单元/Widget 测试**：24 小时节流、跳过版本、对话框三种选择、下载进度与取消、权限引导、各错误码的文案；`/about?check=1` 只自动检查一次（页面重建、旋转不重复触发），检查或下载进行中不重复触发；App icon 小红点和侧边栏“有新版本”标记的出现与清除。
- **仓库规定的命令**：

```powershell
cd mobile
.\scripts\test.ps1
.\scripts\generate.ps1
.\scripts\build-android.ps1 -Mode debug
```

- **Android 真机 E2E**（release 签名的两个版本，配合测试密钥签名的本地清单）：
  1. 首次更新时引导授予“安装未知应用”权限，之后完成升级；升级后配对的电脑仍然在线，无需重新配对。
  2. 在系统确认界面取消，回到 App 后可以再次安装。
  3. 篡改 APK 或清单，分别得到 `update_hash_mismatch` 和 `update_signature_invalid`。
  4. 用 debug 签名 APK 冒充新版本，得到 `update_signer_mismatch`。
  5. 下载过程中切后台，下载继续；在下载中途杀掉进程后重新检查并更新，下载从已有字节续传。

真机未验证前，不得在 [验收记录](../delivery-plan/verification.md) 中标为通过。

## 文档更新触发

修改检查节流、版本比较、Pigeon 方法或事件、错误码、权限与 receiver 配置、APK 校验项、缓存目录、交互入口或支持渠道时，必须更新本文档、[Mobile 架构](../architecture/design.md)、[产品交互](../product-interaction/design.md)、[交付计划](../delivery-plan/design.md)、Desktop 的 [清单协议](../../../desktop/docs/self-update/manifest.md) 和 Mobile README。
