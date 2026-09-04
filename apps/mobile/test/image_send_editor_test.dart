import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/image_send_editor.dart';
import 'package:pro_image_editor/pro_image_editor.dart';

void main() {
  testWidgets('send-time editor exposes every required image tool', (
    tester,
  ) async {
    final image = File(
      'assets/brand/qingwaguagua-mark-flat-source.png',
    ).readAsBytesSync();
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => ElevatedButton(
            onPressed: () => editImageBeforeSending(context, image),
            child: const Text('编辑'),
          ),
        ),
      ),
    );

    await tester.tap(find.text('编辑'));
    await tester.pump();
    await tester.pump(const Duration(seconds: 2));

    final editor = tester.widget<ProImageEditor>(find.byType(ProImageEditor));
    expect(
      editor.configs.mainEditor.tools,
      containsAll(<SubEditorMode>[
        SubEditorMode.cropRotate,
        SubEditorMode.paint,
        SubEditorMode.text,
        SubEditorMode.emoji,
        SubEditorMode.sticker,
        SubEditorMode.blur,
      ]),
    );
    expect(editor.configs.stickerEditor.builder, isNotNull);
    expect(editor.configs.i18n.undo, '撤销');
    expect(editor.configs.i18n.redo, '重做');
    expect(editor.configs.i18n.paintEditor.bottomNavigationBarText, '打码');
    expect(editor.configs.i18n.blurEditor.bottomNavigationBarText, '模糊');
    expect(editor.configs.paintEditor.initialPaintMode, PaintMode.pixelate);
    expect(editor.configs.paintEditor.tools.first, PaintMode.pixelate);
    expect(editor.configs.mainEditor.widgets.appBar, isNotNull);
    expect(find.byKey(const Key('image-editor-top-bar')), findsOneWidget);
    expect(find.byKey(const Key('image-editor-cancel')), findsOneWidget);
    expect(find.byKey(const Key('image-editor-undo')), findsOneWidget);
    expect(find.byKey(const Key('image-editor-redo')), findsOneWidget);
    expect(find.byKey(const Key('image-editor-done')), findsOneWidget);
    expect(find.text('取消'), findsOneWidget);
    expect(find.text('完成'), findsOneWidget);
    expect(
      tester.getSize(find.byKey(const Key('image-editor-cancel'))).height,
      greaterThanOrEqualTo(48),
    );
    expect(
      tester.getSize(find.byKey(const Key('image-editor-done'))).height,
      greaterThanOrEqualTo(44),
    );
    for (final key in const [
      Key('image-editor-undo'),
      Key('image-editor-redo'),
    ]) {
      final button = tester.widget<IconButton>(find.byKey(key));
      final disabledColor = button.style?.foregroundColor?.resolve({
        WidgetState.disabled,
      });
      expect(disabledColor, isNotNull);
      expect(disabledColor!.a, greaterThanOrEqualTo(.50));
    }
    final editorTheme = editor.configs.theme!;
    expect(editorTheme.appBarTheme.foregroundColor, Colors.white);
    expect(editorTheme.appBarTheme.backgroundColor, LinliColors.brandInk);
    final progress = editor
        .configs
        .progressIndicatorConfigs
        .widgets
        .circularProgressIndicator;
    expect(progress, isA<CircularProgressIndicator>());
    expect(
      (progress! as CircularProgressIndicator).color,
      LinliColors.brandYellow,
    );
    expect(tester.takeException(), isNull);
  });
}
