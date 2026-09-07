# 蓝底白蛙 Logo 更新与验证（2026-09-07）

## 实现

- 保留原青蛙轮廓、五官位置和气泡尾巴，改为 `#1976B9` 蓝底、白色青蛙、同款蓝色五官。深浅模式共用，不改为纸飞机。
- 图片编辑工具的首版结果存在不透明棋盘格和色值偏差，未采用、未接入应用。经用户明确同意后，改用项目现有 Python/Pillow 脚本进行确定性换色。
- 对原 PNG 的绿色身体和深色五官进行分类换色，保留外缘 alpha；仅把原图接近不透明的导出噪声归为完全不透明。对比原图和新图的可见轮廓（alpha > 8），差异为空。母版可见色只有 `#FFFFFF` 和 `#1976B9`。
- 统一生成 **69 个 PNG 和 1 个 SVG favicon**：Android 各密度普通、自适应和单色图标，iOS/macOS 全部图标目录，Web/PWA 图标、favicon，应用内徽标、圆形默认群头像及后台 Logo。
- 页面改用圆角蓝底徽标，保持原展示尺寸。单色图标使用五官镂空 alpha，不再把彩色前景直接用作单色层。
- 原 Android 自适应层占比 62% 在圆形裁切时尾巴超出安全区；改为 48%，完整青蛙位于 108dp 图层的 66dp 安全圆内。普通应用图标仍为 84%，PWA maskable 为 62%；圆形默认群头像单独为 76%，避免直接裁切方形图标损失尾巴。
- 未启用任何原生启动 Logo：Android 仍为空背景/透明启动图层，iOS 启动 storyboard 不变。Web 页面背景 `#F2F5F8`、导航主题色 `#FFFFFF` 不变。
- 登录、启动、关于、二维码分享卡、默认群头像及后台品牌区域同步；上传头像不变。在线/成功/接听继续绿色，后台状态圆点改用独立成功色，避免跟随品牌色变蓝。

## 资源与再生成

母版：`apps/mobile/assets/brand/qingwaguagua-mark-flat-source.png`。

生成器：`apps/mobile/tool/generate_brand_assets.py`，沿用项目已有 Pillow，无新增运行时依赖。所有尺寸只从这一母版生成，不需要再执行其他图标生成器。平台的 Contents.json、manifest 和启动配置不会被脚本覆盖。

在仓库根目录使用已安装 Pillow 的 Python 运行：

```powershell
python apps/mobile/tool/generate_brand_assets.py apps/mobile/assets/brand/qingwaguagua-mark-flat-source.png apps/mobile
python apps/mobile/tool/generate_brand_assets.py apps/mobile/assets/brand/qingwaguagua-mark-flat-source.png apps/mobile --check
python -m unittest discover -s apps/mobile/tool -p test_brand_assets.py
```

`--check` 只核对文件，不写入。`--recolor-green` 仅用于旧绿色母版的一次迁移，不应再用于当前蓝白母版。当前工作机使用已确认的 PowerShell 7.6.5，以及 Codex 捆绑 Python/Pillow（Windows Store 的 `python` 别名并非真实解释器）。

主要可预览产物：

- 页面徽标：`apps/mobile/assets/brand/qingwaguagua-badge.png`。
- 普通图标：`apps/mobile/assets/brand/qingwaguagua-icon.png`。
- 图标尺寸和遮罩检查图：`apps/mobile/artifacts/brand-blue/icon-review.png`。

## 验证

| 项目 | 结果 | 日志 |
| --- | --- | --- |
| Python 资源测试 | 10 项通过，包含精确颜色、透明边缘、单色五官、Android/PWA/圆形头像裁切、Apple 尺寸及不透明要求、资源可重复生成 | `artifacts/brand-blue-assets-tests.log` |
| Flutter 完整测试 | 798 项通过，包含新增品牌资源契约、启动/关于视觉基线 | `artifacts/brand-blue-flutter-all.jsonl` |
| Flutter 静态检查 | No issues found | `artifacts/brand-blue-analyze.log` |
| 正式入口 Web Release 构建 | 成功 | `artifacts/brand-blue-web-build.log` |
| 本地隔离预览 Web 构建 | 成功 | `artifacts/brand-blue-preview-build.log` |
| 后台 Vitest | 7 个文件、135 项通过 | `artifacts/brand-blue-admin-tests.log` |
| 后台 TypeScript + 生产构建 | 成功，保留既有大 chunk 提示 | `artifacts/brand-blue-admin-build.log` |

更新受影响 Windows 截图，并新增亮/暗启动、亮/暗关于页 4 张基线；检查登录、桌面扫码登录、注册/找回密码、群聊默认头像和品牌资源。原生单启动层契约继续通过，未出现重复 Logo。

本地浏览器使用 `127.0.0.1:4186` 的隔离模拟数据预览，核对 390px 登录页的深浅主题、后台登录页及 1280px 后台导航。未登录生产账号、未修改服务器数据。

## 交付边界

保留此前未提交的蓝白主题修改。未修改服务端、数据库、API、消息协议、包名、版本或依赖锁文件；未提交、推送、发布或生成 APK/IPA。Android/iOS/macOS 只验证资源与共享代码，未进行真机安装验收；手机桌面图标要安装后续新版本才会更新。浏览器/PWA 的图标还可能受已有图标缓存影响。
