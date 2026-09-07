# Telegram 风格蓝白主题验证记录

日期：2026-09-07。环境：Windows、PowerShell 7.6.5、Flutter 3.44.8。

## 交付范围

- Flutter 使用 `LinliPalette` / `context.linli` 区分按钮、导航、选中态、链接、气泡和状态色；亮色为蓝白冷灰，暗色为深蓝灰。
- 登录注册、会话列表、聊天顶部与内容、群资料、邀请/移除、设置、录音、回应、转发、二维码、更新及媒体编辑沿用共享主题。
- Cupertino 开关显式继承主题，避免框架默认绿色；文字头像使用蓝色。绿色青蛙 Logo、上传头像以及在线/成功/接听语义保留。
- 管理后台仍仅提供亮色：白色侧栏/顶部、冷灰工作区、浅蓝选中、蓝底白字按钮；清理旧黄黑 CSS 叠加规则。
- 同步 Web manifest、加载背景、Android 启动背景与系统栏、`DESIGN.md`。保留已有主题偏好与聊天背景存储值（旧 `softMint` 仅更改显示名称和颜色）。
- 未修改服务端 API、数据库、协议、版本号或依赖锁文件；没有提交、推送、发布或生成 APK/IPA。

## 自动化验证

| 项目 | 结果 | 本地日志 |
| --- | --- | --- |
| Flutter 全量测试，含视觉比较 | 791 项通过 | `artifacts/telegram-blue-flutter-all.jsonl` |
| Flutter 静态检查 | No issues found | `artifacts/telegram-blue-analyze.log` |
| Flutter 正式入口 Web Release 构建 | 成功 | `artifacts/telegram-blue-web-build.log` |
| Flutter 隔离预览入口 Web 构建 | 成功 | `artifacts/telegram-blue-preview-build.log` |
| 管理后台 Vitest | 7 个文件、134 项通过 | `artifacts/telegram-blue-admin-tests.log` |
| 管理后台 TypeScript + Vite 生产构建 | 成功 | `artifacts/telegram-blue-admin-build.log` |
| Git 空白错误检查 | 通过 | `git diff --check` |

主题断言验证主按钮正常/悬停/按压配色，以及正文、次级文字、气泡链接的实际前景/背景组合达到 4.5:1；焦点与控件轮廓达到 3:1。另验证导航色、Cupertino 主题继承、禁用优先级和后台旧黄黑色值未残留。

首轮修正了旧的白色启动背景断言。高并发全量运行曾出现既有群成员异步测试的偶发时序失败，单独复测通过；最终全量使用并发 2 通过，未因此改动群成员业务代码。后台构建仍有既有大 chunk 提示，不影响构建成功。

## 视觉检查

新增或更新 50 张 Windows 组件截图，位于 `apps/mobile/test/goldens/windows/`，逐张检查后保留新基线：

- 登录、注册、找回密码、桌面扫码登录；浅色和深色登录。
- 四个一级页面、设置、聊天信息、单聊和群聊、群头像角标、群管理与邀请、完整成员列表。
- 表情回应的选中/未选中、录音/取消/试听三态，均覆盖浅深色。
- 手机及桌面 Emoji 面板、图片编辑、个人二维码、朋友圈和各类空/错误状态。
- 1280px 深色桌面、窄聊天列、大字体、长群名和 IPv6 顶部资料。

检查时修正了深色消息时间/回执偏暗、未选中登录方式偏暗、搜索提示与分组标题对比不足，以及 Cupertino 开关未继承蓝色的问题。录音取消继续保持红色，媒体观看背景继续保持深色。

Windows 预览程序可能对 PNG 建立内存映射，因此截图测试改为临时文件原子替换基线，避免直接截断被占用图片；没有放宽现有比较精度。

## 本地浏览器实操

通过回环地址与测试夹具检查，没有登录生产账号或向真实聊天发送消息：

- 客户端：390px 会话列表与导航、1280px 默认空会话、打开群聊、浅深色切换、展开/收起信息栏、窄聊天列、进入群管理和蓝色设置开关。
- 后台：登录、运行概览/图表、窄屏抽屉、1280px 白色侧栏及用户表格、新增用户表单、危险操作弹窗与取消。未提交管理写操作。
- 1280px 页面通过固定宽度 iframe 查看；外层缩放仅用于在本地浏览器面板容纳预览，应用内媒体查询使用 1280px。

### 复现预览

在 `apps/mobile` 构建隔离的测试入口（不作为发布产物）：

```powershell
flutter build web --release --no-pub --base-href /mobile/ -t tool/theme_preview.dart --output build/theme-preview
```

在 `apps/admin` 执行 `npm run build` 后，回到仓库根目录运行：

```powershell
node tools/theme-preview-server.mjs
```

- 后台：`http://127.0.0.1:4186/`，本地测试登录使用 `admin / preview-password`，并非生产凭据。
- 客户端：`http://127.0.0.1:4186/mobile/?width=390`。
- 深色登录：`http://127.0.0.1:4186/mobile/?width=390&dark=1&page=login`。
- 桌面后台：`http://127.0.0.1:4186/preview-desktop`。
- 桌面客户端：`http://127.0.0.1:4186/preview-desktop?app=mobile`。

预览服务器只监听 `127.0.0.1`，管理数据来自现有测试夹具，除模拟登录外的写请求一律拒绝。正式入口仍为 `lib/main.dart`，不导入预览代码。

## 边界

本次完成共享 Flutter 页面、Windows 组件截图和浏览器检查；未进行 Android/iOS/macOS 真机或模拟器视觉验收，不将共享代码测试等同于各端真机通过。正式入口 Web 已构建但未部署；移动端配色需要后续重新构建客户端才能上线。
