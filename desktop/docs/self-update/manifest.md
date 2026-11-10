# Update manifest protocol

本文是 Work Anywhere 更新清单的唯一规范。Desktop 负责维护本文和生成工具；[Desktop 自更新](design.md) 与 [Mobile 应用内更新](../../../mobile/docs/app-update/design.md) 是消费方，只链接本文，不复制字段定义。

## Goal and scope

为每个正式 GitHub Release 发布一份签名清单，让客户端只发一次请求就能拿到最新版本号、各平台安装产物的下载地址、大小和 SHA-256，并在信任这些字段前验证发布者签名。

清单只描述 `stable` 通道，也就是非 prerelease 的 GitHub Release。Relay 镜像不在清单内。

## Location

- 默认地址：`https://github.com/vual/work-anywhere/releases/latest/download/update-manifest.json`
- GitHub 的 `releases/latest` 只指向最新的非 prerelease、非 draft Release，因此 prerelease 不会被推送给客户端。
- 每个 Release 都附带自己的 `update-manifest.json`。旧 Release 的清单仍然有效，但客户端不会接受版本不高于当前版本的清单（见下文“Client verification”）。

### Mirror

客户端可以配置一个镜像前缀 `mirrorBase`（必须是 `https://`）。启用后：

- 清单地址变为 `<mirrorBase>/latest/download/update-manifest.json`。
- 产物 `url` 中的前缀 `https://github.com/vual/work-anywhere/releases/` 被替换为 `<mirrorBase>/`，路径其余部分保持不变。

镜像不被信任：签名和 SHA-256 校验规则完全不变。镜像只能让更新不可用或回退到旧清单（会被拒绝），不能让客户端安装未签名的内容。

## File format

`update-manifest.json` 是一个外层 envelope，签名覆盖内层 payload 的原始字节，避免 JSON canonicalization：

```json
{
  "format": "waw-update-manifest",
  "formatVersion": 1,
  "keyId": "waw-release-2026",
  "payload": "<base64url(无 padding) 编码的 payload UTF-8 JSON 字节>",
  "signature": "<base64url(无 padding) 编码的 64 字节 Ed25519 签名>"
}
```

签名输入为 `"waw-update-manifest-v1\n"` 的 ASCII 字节后接 payload 原始字节（domain separation，防止该密钥对其他用途的数据签名被挪用）。

payload 解码后的结构：

```json
{
  "channel": "stable",
  "version": "0.3.0",
  "tag": "v0.3.0",
  "publishedAt": "2026-11-10T08:00:00Z",
  "notesUrl": "https://github.com/vual/work-anywhere/releases/tag/v0.3.0",
  "assets": {
    "windows-amd64-setup": {
      "name": "work-anywhere-setup-0.3.0.exe",
      "url": "https://github.com/vual/work-anywhere/releases/download/v0.3.0/work-anywhere-setup-0.3.0.exe",
      "size": 9123456,
      "sha256": "<64 位小写十六进制>"
    },
    "windows-amd64-portable": {
      "name": "work-anywhere-0.3.0-windows-amd64.exe",
      "url": "https://github.com/vual/work-anywhere/releases/download/v0.3.0/work-anywhere-0.3.0-windows-amd64.exe",
      "size": 21234567,
      "sha256": "<...>"
    },
    "android-apk": {
      "name": "work-anywhere-0.3.0-android.apk",
      "url": "https://github.com/vual/work-anywhere/releases/download/v0.3.0/work-anywhere-0.3.0-android.apk",
      "size": 31234567,
      "sha256": "<...>",
      "packageName": "io.workanywhere.work_anywhere",
      "versionCode": 87,
      "signingCertSha256": "<APK 签名证书 DER 的 SHA-256，64 位小写十六进制>"
    }
  }
}
```

字段规则：

| 字段 | 规则 |
| --- | --- |
| `channel` | 当前只能为 `stable`；客户端拒绝其他值。 |
| `version` | SemVer 2.0，不带 `v` 前缀，不含 prerelease 段，与 `tag` 去掉 `v` 后一致。 |
| `publishedAt` | RFC 3339 UTC；只用于展示，不参与是否更新的决策。 |
| `notesUrl` | 必须是 `https://`；客户端只用系统浏览器打开，不在应用内渲染。 |
| `assets` | 键是平台标识；客户端只读取自己的键，忽略未知键。缺少自己的键表示本次 Release 没有该平台产物。 |
| `size` | 正整数，上限 512 MiB；下载超出或不等都视为失败。 |
| `sha256` | 产物完整文件的 SHA-256。 |
| `android-apk.versionCode` | 与 APK 内的 `versionCode` 一致，由 `github.run_number` 生成。 |
| `android-apk.signingCertSha256` | 与 Release keystore 证书一致；Mobile 安装前用它和已安装应用的签名比较。 |

未知字段一律忽略，以便后续增量扩展；修改已有字段语义或签名输入时必须提升 `formatVersion`。

## Client verification

客户端必须按以下顺序处理，任何一步失败都停止，并返回下面列出的稳定错误码：

1. 只通过 HTTPS 下载，允许跨主机 HTTPS 重定向（GitHub 会重定向到对象存储），禁止降级到 HTTP。清单响应上限 64 KiB。
2. 检查 `format`、`formatVersion`；`keyId` 必须在客户端内置的可信公钥列表中，否则返回 `update_signature_invalid`。
3. 用对应 Ed25519 公钥验证签名，否则返回 `update_signature_invalid`。
4. 签名通过后才解析 payload；字段不合法时返回 `update_manifest_invalid`。
5. 只有 `version` 严格高于当前版本（Android 还要求 `versionCode` 严格大于已安装的 `versionCode`）时才视为有更新。这是唯一的防回滚机制。
6. 下载产物时同时校验 `size` 与 `sha256`，不匹配时删除文件并返回 `update_hash_mismatch`。

清单下载失败（网络错误、非 2xx、超过上限）统一返回 `update_manifest_unavailable`。

## Signing keys

- 签名私钥是 32 字节 Ed25519 seed，base64 编码后保存在 GitHub Actions secret `WAW_UPDATE_SIGNING_KEY`；`keyId` 保存在 `WAW_UPDATE_SIGNING_KEY_ID`。私钥不进入仓库，也不出现在日志或产物中。
- 公钥与 `keyId` 编译进客户端：Desktop 在 [`internal/update/manifest/keys.go`](../../internal/update/manifest/keys.go)，Mobile 在 [`core/update/keys.go`](../../../mobile/core/update/keys.go)。两处必须包含同一组可信密钥。
- 当前可信密钥为 `waw-release-2026`。对应私钥和 `keyId` 必须存入上面两个 secret，否则 Release 的 `update-manifest` job 失败。新增或轮换密钥时，用 `go run ./internal/tools/updatemanifest keygen -key-id <id> -out <file>` 在仓库外生成，并把公钥同时写入两份 `keys.go`。
- 轮换时，先发布同时信任新旧 `keyId` 的客户端版本，后续 Release 再改用新密钥签名。私钥疑似泄露时，必须发布移除旧 `keyId` 的版本，并在 Release 说明中提示用户手动升级。

## Generation

Release workflow 的 `update-manifest` job，`needs: [desktop-windows, android]`：

1. 用 `gh release download` 取回本 Release 的 `.sha256` 文件，并读取产物大小；Android 的 `versionCode` 和 `signingCertSha256` 由 `android` job 通过 job outputs 传入。
2. 调用 Desktop 模块中的生成工具 `desktop/internal/tools/updatemanifest`，生成 payload 并用 secret 签名。
3. 自检：`updatemanifest verify` 用编译进工具的可信公钥重新验证签名和版本，再上传 `update-manifest.json`（使用 `--clobber`，与 workflow 的 re-run 语义一致）。

以下任一情况都会让 job 失败，此时 Release 不带清单，客户端看到的是 `update_manifest_unavailable`，而不是半成品：缺少签名 secret；任一平台产物缺失；缺少 Android release keystore secret（`android` job 拒绝发布 debug 签名的 APK）；签名 keyId 不在可信列表中。

## Test vectors

[`vectors.json`](vectors.json) 与本文放在同一目录，包含一组测试密钥（非发布密钥）签名的样例：有效清单、篡改 payload、错误 `keyId`、错误签名、非法版本号，以及期望的错误码。Desktop `internal/update` 与 Mobile Go Core 的单元测试都必须读取这同一份文件，不能各自维护副本。

## Documentation update triggers

修改清单地址、镜像替换规则、envelope 或 payload 字段、签名输入、可信密钥列表、错误码、平台标识或生成流程时，必须同时更新本文、`vectors.json`、[Desktop 自更新](design.md)、[Mobile 应用内更新](../../../mobile/docs/app-update/design.md) 和 [Windows 安装包](../windows-installer/design.md) 的 Release automation 小节。
