import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:pro_image_editor/pro_image_editor.dart';

/// Opens the send-time editor and returns JPEG bytes, or `null` when the user
/// cancels. Keeping this helper independent from chat/moments makes the editing
/// behavior identical everywhere media can be published.
Future<Uint8List?> editImageBeforeSending(
  BuildContext context,
  Uint8List sourceBytes,
) {
  final theme = Theme.of(context);
  return Navigator.of(context).push<Uint8List>(
    MaterialPageRoute(
      fullscreenDialog: true,
      builder: (editorContext) => ProImageEditor.memory(
        sourceBytes,
        configs: ProImageEditorConfigs(
          theme: theme,
          mainEditor: const MainEditorConfigs(
            tools: [
              SubEditorMode.cropRotate,
              SubEditorMode.paint,
              SubEditorMode.text,
              SubEditorMode.emoji,
              SubEditorMode.sticker,
              SubEditorMode.blur,
            ],
          ),
          stickerEditor: StickerEditorConfigs(builder: _buildStickerPicker),
          i18n: const I18n(
            cancel: '取消',
            undo: '撤销',
            redo: '重做',
            done: '完成',
            remove: '删除',
            doneLoadingMsg: '正在生成图片…',
            various: I18nVarious(
              loadingDialogMsg: '请稍候…',
              closeEditorWarningTitle: '放弃编辑？',
              closeEditorWarningMessage: '当前修改尚未保存，确定退出吗？',
              closeEditorWarningConfirmBtn: '放弃',
              closeEditorWarningCancelBtn: '继续编辑',
            ),
            cropRotateEditor: I18nCropRotateEditor(
              bottomNavigationBarText: '裁剪',
              rotate: '旋转',
              flip: '翻转',
              ratio: '比例',
              reset: '重置',
              back: '返回',
              cancel: '取消',
              done: '完成',
              undo: '撤销',
              redo: '重做',
              smallScreenMoreTooltip: '更多',
            ),
            paintEditor: I18nPaintEditor(
              bottomNavigationBarText: '涂鸦',
              moveAndZoom: '移动缩放',
              freestyle: '画笔',
              arrow: '箭头',
              line: '直线',
              rectangle: '矩形',
              circle: '圆形',
              blur: '模糊笔',
              pixelate: '马赛克',
              eraser: '橡皮擦',
              lineWidth: '线宽',
              toggleFill: '填充',
              changeOpacity: '透明度',
              opacity: '透明度',
              color: '颜色',
              strokeWidth: '线宽',
              fill: '填充',
              cancel: '取消',
              undo: '撤销',
              redo: '重做',
              done: '完成',
              back: '返回',
              smallScreenMoreTooltip: '更多',
            ),
            textEditor: I18nTextEditor(
              inputHintText: '输入文字',
              bottomNavigationBarText: '文字',
              back: '返回',
              done: '完成',
              textAlign: '对齐',
              fontScale: '字号',
              backgroundMode: '背景',
              smallScreenMoreTooltip: '更多',
            ),
            emojiEditor: I18nEmojiEditor(
              bottomNavigationBarText: '表情',
              search: '搜索表情',
              categoryRecent: '最近',
              categorySmileys: '人物',
              categoryAnimals: '动物',
              categoryFood: '食物',
              categoryActivities: '活动',
              categoryTravel: '旅行',
              categoryObjects: '物品',
              categorySymbols: '符号',
              categoryFlags: '旗帜',
              locale: Locale('zh'),
            ),
            stickerEditor: I18nStickerEditor(bottomNavigationBarText: '贴纸'),
            blurEditor: I18nBlurEditor(
              bottomNavigationBarText: '模糊',
              back: '返回',
              done: '完成',
            ),
          ),
        ),
        callbacks: ProImageEditorCallbacks(
          onImageEditingComplete: (bytes) async {
            if (editorContext.mounted) {
              Navigator.of(editorContext).pop(bytes);
            }
          },
        ),
      ),
    ),
  );
}

const _stickers = <String>[
  '👍',
  '❤️',
  '🎉',
  '✨',
  '🔥',
  '💯',
  '✅',
  '⭐',
  '📌',
  '💡',
  '😂',
  '🥳',
  '😎',
  '🤝',
  '👏',
  '🚀',
  '🌈',
  '🎁',
  '☕',
  '🌸',
];

Widget _buildStickerPicker(
  void Function(WidgetLayer) setLayer,
  ScrollController scrollController,
) => GridView.builder(
  controller: scrollController,
  padding: const EdgeInsets.fromLTRB(16, 16, 16, 32),
  gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
    crossAxisCount: 5,
    mainAxisSpacing: 10,
    crossAxisSpacing: 10,
  ),
  itemCount: _stickers.length,
  itemBuilder: (context, index) {
    final sticker = _stickers[index];
    return Material(
      color: Theme.of(context).colorScheme.surfaceContainerHigh,
      borderRadius: BorderRadius.circular(14),
      child: InkWell(
        key: Key('image-editor-sticker-$index'),
        borderRadius: BorderRadius.circular(14),
        onTap: () => setLayer(
          WidgetLayer(
            widget: Text(sticker, style: const TextStyle(fontSize: 64)),
          ),
        ),
        child: Center(
          child: Text(sticker, style: const TextStyle(fontSize: 30)),
        ),
      ),
    );
  },
);
