# Flutter 四端构建入口

本项目使用同一套 Flutter 业务代码生成 Web、Android、iOS 与 macOS 客户端。正式构建必须关闭演示模式，并使用真实 HTTPS 服务、用户协议和隐私政策地址。

## 构建前门禁

```bash
cd apps/mobile
flutter pub get
flutter analyze
flutter test
```

上述命令全部成功后才能继续构建。真实推送参数保存在已忽略的本地配置或 CI 密钥中，不得写入仓库。

## Web

```bash
flutter build web --release \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://SERVER_IP_OR_DOMAIN \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://SERVER_IP_OR_DOMAIN/legal/terms \
  --dart-define=PRIVACY_URL=https://SERVER_IP_OR_DOMAIN/legal/privacy
```

产物位于 `apps/mobile/build/web/`。服务器部署由生产 Compose 中的 `web` 容器统一构建，避免手工复制遗漏缓存头、反向代理或运行配置。

## Android

正式 APK/AAB 必须通过 `infra/scripts/build-android-release.ps1`，脚本会检查真实服务、法律页面、签名和 SHA-256 发布清单。调试包可以使用：

```bash
flutter build apk --debug
```

## iOS

```bash
flutter build ios --release --no-codesign \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://SERVER_IP_OR_DOMAIN \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://SERVER_IP_OR_DOMAIN/legal/terms \
  --dart-define=PRIVACY_URL=https://SERVER_IP_OR_DOMAIN/legal/privacy
```

最终签名、能力文件、推送证书和上架归档必须在 Xcode 或受控 CI 中完成。

## macOS

```bash
flutter build macos --release \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://SERVER_IP_OR_DOMAIN \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://SERVER_IP_OR_DOMAIN/legal/terms \
  --dart-define=PRIVACY_URL=https://SERVER_IP_OR_DOMAIN/legal/privacy
```

正式分发还需要 Developer ID 签名、公证和安装包验证。

## 验收证据

每次发布至少记录 Git 提交、目标服务地址、构建命令、测试结果、产物 SHA-256 与部署时间。仅有“构建成功”不能证明登录、消息、离线同步、文件、音视频和推送业务链路可用。
