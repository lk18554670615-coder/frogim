# 常用小表情扩充验证

日期：2026-09-02

## 交付内容

- 本地独立目录包含 320 个不同 Emoji；现有 80 个表情及默认最近项全部保留。
- 8 个内容分类：表情、手势、符号、动物与自然、食物与饮品、运动与活动、旅行与交通、物品；前面保留“最近”。
- 分类横向滚动，切换时网格回顶，网格按面板宽度和字体缩放调整列数，每项点击区域至少 44 × 44。
- 点击仍在光标位置插入普通文本；选中替换、复合字符退格和确认发送行为保留。
- 沿用 `chat_recent_emojis` 和 24 项上限；不按新目录过滤旧记录中的其他有效 Emoji。
- 表情面板退格按钮明确设置背景/前景色，解决深浅主题下图标与背景同色的问题。
- 没有恢复表情商店入口，没有修改图片贴纸、消息回应、协议或服务端，没有增加字体、图片资源和依赖。

## 验证结果

环境：Windows、PowerShell 7.6.4、FVM Flutter 3.44.8。

- `flutter analyze --no-pub`：无问题。
- 相关 Flutter 回归共 **161 项通过**，包含本次新增的 18 项目录与面板测试。
- 目录校验：320 个不同项、分类有序且非空、全部为完整字符、原有项不丢失。
- 读取现有 NotoColorEmoji 字体 cmap，确认目录内所有基础码点已有字形；无需增加字体文件。
- 组件校验：全部分类可访问、分类切换回顶、插入不直接发送、光标插入/替换、复合 Emoji/肤色/家庭/旗帜完整退格、空文本禁止发送。
- 最近记录校验：旧存储读取、不覆盖目录外有效记录、去重、重复使用置顶、24 项上限、关闭重开顺序不变。
- 布局校验：320 × 568 和 1280 × 900，深浅主题，100% 与 200% 字体，混合中文/英文/Emoji 文本。
- 新增并复核 4 张 Windows Golden 基线：手机大字体与桌面正常字体的深浅主题。仅新增本次基线，未更新其他功能截图。

在 `apps/mobile` 下复现：

```powershell
& ./.fvm/flutter_sdk/bin/flutter.bat analyze --no-pub
& ./.fvm/flutter_sdk/bin/flutter.bat test --no-pub `
  test/emoji_catalog_test.dart test/emoji_panel_test.dart `
  test/chat_composer_test.dart test/forward_batch_test.dart `
  test/forward_conversation_sheet_test.dart test/message_forward_picker_test.dart `
  test/structured_messages_repository_test.dart test/structured_message_widget_test.dart `
  test/message_collaboration_repository_test.dart test/message_collaboration_widget_test.dart `
  test/ios_chat_navigation_test.dart test/voice_composer_controller_test.dart `
  test/voice_composer_widgets_test.dart test/voice_queue_test.dart test/widget_test.dart `
  --reporter expanded
```

## 边界

本轮为本地自动化及截图验证，没有进行四端真机发送验收；各平台原生字形仍受系统字体影响。没有提交、推送、打包、部署或调整更新策略。工作区已有的转发、语音、导航等修改均保留。
