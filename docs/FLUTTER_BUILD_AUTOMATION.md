# Flutter 四端构建入口

Flutter 固定为 FVM `3.44.8`。构建入口不读取或输出服务端密钥。

| 平台 | 构建机 | 固定入口 |
|---|---|---|
| Android APK/AAB | Windows + Android Studio SDK | `infra/scripts/build-android-release.ps1` |
| Web | Docker/Linux | `apps/mobile/Dockerfile.web` |
| iOS | macOS + Xcode | `infra/scripts/build-apple-release.sh ios` |
| macOS | macOS + Xcode | `infra/scripts/build-apple-release.sh macos` |

Android 示例：

```powershell
powershell -ExecutionPolicy Bypass -File infra/scripts/build-android-release.ps1 `
  -ServerOrigin https://example.com -Format all
```

Apple 示例：

```bash
SERVER_ORIGIN=https://example.com infra/scripts/build-apple-release.sh all
```

Apple 入口会拒绝非 macOS 主机、错误 Flutter 版本、非 HTTPS 服务地址以及不可访问的健康/协议/隐私页面。iOS 使用 `--no-codesign` 生成可供 Xcode 签名归档的 Release 构建；正式上架仍必须由用户的 Apple Developer Team 完成签名、公证和真机验证。
