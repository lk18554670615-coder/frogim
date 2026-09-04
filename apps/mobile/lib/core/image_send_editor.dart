import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:pro_image_editor/pro_image_editor.dart';

import 'app_theme.dart';

const _editorBackground = Color(0xFF080B0A);
const _editorBarBackground = LinliColors.brandInk;
const _editorAccent = LinliColors.brandYellow;
const _editorToolbarText = TextStyle(
  color: Colors.white,
  fontSize: 15,
  fontWeight: FontWeight.w600,
  fontFamily: 'NotoSansSC',
  fontFamilyFallback: [
    'PingFang SC',
    'Microsoft YaHei',
    'Noto Sans CJK SC',
    'system-ui',
  ],
);
const _editorProgressIndicator = ProgressIndicatorConfigs(
  widgets: ProgressIndicatorWidgets(
    circularProgressIndicator: CircularProgressIndicator(
      color: _editorAccent,
      strokeWidth: 2.5,
    ),
  ),
);

ThemeData _imageEditorTheme(ThemeData source) => source.copyWith(
  brightness: Brightness.dark,
  scaffoldBackgroundColor: _editorBackground,
  appBarTheme: const AppBarTheme(
    backgroundColor: _editorBarBackground,
    foregroundColor: Colors.white,
    surfaceTintColor: Colors.transparent,
    elevation: 0,
    scrolledUnderElevation: 0,
    iconTheme: IconThemeData(color: Colors.white, size: 24),
    actionsIconTheme: IconThemeData(color: Colors.white, size: 24),
  ),
  iconButtonTheme: IconButtonThemeData(
    style: IconButton.styleFrom(
      minimumSize: const Size.square(48),
      foregroundColor: Colors.white,
      disabledForegroundColor: Colors.white54,
    ),
  ),
);

const _mainEditorStyle = MainEditorStyle(
  background: _editorBackground,
  appBarBackground: _editorBarBackground,
  appBarColor: Colors.white,
  bottomBarBackground: _editorBarBackground,
  bottomBarColor: Colors.white,
);

const _mainEditorWidgets = MainEditorWidgets(appBar: _buildMainEditorAppBar);

ReactiveAppbar _buildMainEditorAppBar(
  ProImageEditorState editor,
  Stream<void> rebuildStream,
) => ReactiveAppbar(
  stream: rebuildStream,
  builder: (_) => AppBar(
    key: const Key('image-editor-top-bar'),
    automaticallyImplyLeading: false,
    titleSpacing: 8,
    title: Row(
      children: [
        TextButton(
          key: const Key('image-editor-cancel'),
          onPressed: editor.closeEditor,
          style: TextButton.styleFrom(
            minimumSize: const Size(56, 48),
            foregroundColor: Colors.white,
            textStyle: _editorToolbarText,
          ),
          child: const Text('取消'),
        ),
        const Spacer(),
        IconButton(
          key: const Key('image-editor-undo'),
          tooltip: '撤销',
          onPressed: editor.canUndo ? editor.undoAction : null,
          style: IconButton.styleFrom(
            minimumSize: const Size.square(48),
            foregroundColor: Colors.white,
            disabledForegroundColor: Colors.white54,
          ),
          icon: const Icon(Icons.undo_rounded, size: 23),
        ),
        IconButton(
          key: const Key('image-editor-redo'),
          tooltip: '重做',
          onPressed: editor.canRedo ? editor.redoAction : null,
          style: IconButton.styleFrom(
            minimumSize: const Size.square(48),
            foregroundColor: Colors.white,
            disabledForegroundColor: Colors.white54,
          ),
          icon: const Icon(Icons.redo_rounded, size: 23),
        ),
        const SizedBox(width: 4),
        FilledButton(
          key: const Key('image-editor-done'),
          onPressed: editor.doneEditing,
          style: FilledButton.styleFrom(
            minimumSize: const Size(64, 44),
            padding: const EdgeInsets.symmetric(horizontal: 16),
            backgroundColor: _editorAccent,
            foregroundColor: LinliColors.brandInk,
            textStyle: _editorToolbarText.copyWith(color: LinliColors.brandInk),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(10),
            ),
          ),
          child: const Text('完成'),
        ),
      ],
    ),
  ),
);

const _cropEditorStyle = CropRotateEditorStyle(
  background: _editorBackground,
  appBarBackground: _editorBarBackground,
  appBarColor: Colors.white,
  bottomBarBackground: _editorBarBackground,
  bottomBarColor: Colors.white,
  cropCornerColor: _editorAccent,
  helperLineColor: Colors.white70,
);

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
          theme: _imageEditorTheme(theme),
          progressIndicatorConfigs: _editorProgressIndicator,
          mainEditor: const MainEditorConfigs(
            style: _mainEditorStyle,
            widgets: _mainEditorWidgets,
            tools: [
              SubEditorMode.cropRotate,
              SubEditorMode.paint,
              SubEditorMode.text,
              SubEditorMode.emoji,
              SubEditorMode.sticker,
              SubEditorMode.blur,
            ],
          ),
          cropRotateEditor: const CropRotateEditorConfigs(
            style: _cropEditorStyle,
          ),
          paintEditor: const PaintEditorConfigs(
            initialPaintMode: PaintMode.pixelate,
            tools: [
              PaintMode.pixelate,
              PaintMode.blur,
              PaintMode.freeStyle,
              PaintMode.arrow,
              PaintMode.line,
              PaintMode.rect,
              PaintMode.circle,
              PaintMode.eraser,
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
              bottomNavigationBarText: '打码',
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

/// Opens the profile-photo editor with a focused 1:1 crop workflow. Avatar
/// editing deliberately excludes drawing, stickers and text so the result
/// stays clean and predictable across the app's circular and square slots.
Future<Uint8List?> editAvatarImage(
  BuildContext context,
  Uint8List sourceBytes,
) {
  final theme = Theme.of(context);
  return Navigator.of(context).push<Uint8List>(
    MaterialPageRoute(
      fullscreenDialog: true,
      builder: (editorContext) => ProImageEditor.memory(
        sourceBytes,
        key: const Key('profile-avatar-editor'),
        configs: ProImageEditorConfigs(
          theme: _imageEditorTheme(theme),
          progressIndicatorConfigs: _editorProgressIndicator,
          mainEditor: const MainEditorConfigs(
            style: _mainEditorStyle,
            widgets: _mainEditorWidgets,
            tools: [SubEditorMode.cropRotate],
          ),
          cropRotateEditor: const CropRotateEditorConfigs(
            initAspectRatio: 1,
            enableKeepAspectRatioOnRotate: true,
            tools: [
              CropRotateTool.rotate,
              CropRotateTool.flip,
              CropRotateTool.reset,
            ],
            aspectRatios: [AspectRatioItem(text: '1:1', value: 1)],
            style: _cropEditorStyle,
          ),
          i18n: const I18n(
            cancel: '取消',
            undo: '撤销',
            redo: '重做',
            done: '完成',
            remove: '删除',
            doneLoadingMsg: '正在生成头像…',
            various: I18nVarious(
              loadingDialogMsg: '请稍候…',
              closeEditorWarningTitle: '放弃调整？',
              closeEditorWarningMessage: '当前头像调整尚未保存，确定退出吗？',
              closeEditorWarningConfirmBtn: '放弃',
              closeEditorWarningCancelBtn: '继续调整',
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
