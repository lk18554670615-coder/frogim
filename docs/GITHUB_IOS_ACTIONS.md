# GitHub Actions iOS 构建

仓库工作流位于 `.github/workflows/ios-build.yml`，使用 GitHub 托管的
`macos-15` Runner 和 `apps/mobile/.fvmrc` 固定的 Flutter 版本。

## 自动编译检查

以下文件在 `main` 分支发生变化时，工作流会自动运行：

- Flutter 客户端；
- 个推 Flutter 插件；
- 固定的 WuKongIM 与 WebRTC SDK；
- iOS 工作流自身。

自动任务会执行依赖解析、`flutter analyze`、全部 Flutter 测试，以及：

```bash
flutter build ios --release --no-codesign
```

成功后，Actions 运行详情页会提供保留 7 天的
`ios-unsigned-<run number>` 构建产物。该产物只用于编译验证，不能安装到普通
iPhone，也不能提交 App Store。

## 签名 IPA 所需 Secrets

进入 GitHub 仓库的 `Settings → Secrets and variables → Actions`，新增：

| Secret | 内容 |
| --- | --- |
| `IOS_CERTIFICATE_P12_BASE64` | Apple Distribution `.p12` 的 Base64 文本 |
| `IOS_CERTIFICATE_PASSWORD` | `.p12` 密码 |
| `IOS_PROVISIONING_PROFILE_BASE64` | 匹配 `com.qingwaguagua.imapp` 的 `.mobileprovision` Base64 文本 |
| `IOS_EXPORT_OPTIONS_PLIST_BASE64` | 对应发布方式的 `ExportOptions.plist` Base64 文本 |
| `IOS_KEYCHAIN_PASSWORD` | 临时 CI Keychain 使用的随机高强度密码 |
| `GETUI_APP_ID` | 个推客户端 App ID |
| `GETUI_APP_KEY` | 个推客户端 App Key |
| `GETUI_APP_SECRET` | 个推客户端 App Secret；不是服务端 MasterSecret |

Apple 证书、描述文件和私钥材料不得提交到 Git。Windows PowerShell 可使用：

```powershell
[Convert]::ToBase64String(
  [IO.File]::ReadAllBytes('C:\secure\distribution.p12')
) | Set-Clipboard
```

对 `.mobileprovision` 和 `ExportOptions.plist` 使用同样命令生成 Base64 文本。

## 手动构建签名 IPA

1. 打开 GitHub 仓库的 `Actions` 页面。
2. 选择 `iOS Build`。
3. 点击 `Run workflow`。
4. 勾选“使用仓库 Secrets 构建签名 IPA”。
5. 构建成功后下载 `ios-signed-<run number>`。

签名产物保留 14 天。工作流会在任务结束时删除 Runner 上的临时证书、描述文件和
Keychain。目前工作流只生成 IPA，不自动上传 TestFlight。
