import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/image_send_editor.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:pro_image_editor/pro_image_editor.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('图片预览单击图片区域直接关闭', (tester) async {
    await _openImagePreview(tester);
    final preview = find.byKey(const Key('message-image-preview'));
    final close = find.byKey(const Key('close-message-image-preview'));
    final more = find.byKey(const Key('more-message-image-preview'));
    final edit = find.byKey(const Key('edit-message-image-preview'));

    expect(preview, findsOneWidget);
    expect(close.hitTestable(), findsOneWidget);
    expect(more.hitTestable(), findsOneWidget);
    expect(edit.hitTestable(), findsOneWidget);

    final center = tester.getCenter(
      find.byKey(const Key('message-image-preview-render')),
    );
    await tester.tapAt(center);
    await tester.pump(const Duration(milliseconds: 420));
    await tester.pumpAndSettle();

    expect(preview, findsNothing);
    expect(close, findsNothing);
    expect(more, findsNothing);
    expect(edit, findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('图片预览支持双指缩放且顶部按钮高对比并不小于 44', (tester) async {
    await _openImagePreview(tester);

    expect(
      find.byKey(const Key('message-image-preview-top-scrim')),
      findsOneWidget,
    );
    expect(
      find.byKey(const Key('message-image-preview-bottom-scrim')),
      findsOneWidget,
    );

    final viewerFinder = find.byKey(
      const Key('message-image-interactive-viewer'),
    );
    final viewer = tester.widget<InteractiveViewer>(viewerFinder);
    expect(viewer.minScale, lessThanOrEqualTo(1));
    expect(viewer.maxScale, greaterThanOrEqualTo(4));
    final transformation = viewer.transformationController!;
    expect(transformation.value.getMaxScaleOnAxis(), closeTo(1, .01));

    final center = tester.getCenter(viewerFinder);
    final first = await tester.startGesture(
      center + const Offset(-32, 0),
      pointer: 1,
    );
    final second = await tester.startGesture(
      center + const Offset(32, 0),
      pointer: 2,
    );
    await tester.pump();
    await first.moveTo(center + const Offset(-92, 0));
    await second.moveTo(center + const Offset(92, 0));
    await tester.pump(const Duration(milliseconds: 100));
    await first.up();
    await second.up();
    await tester.pump();
    expect(transformation.value.getMaxScaleOnAxis(), greaterThan(1.2));

    for (final key in const [
      Key('close-message-image-preview'),
      Key('more-message-image-preview'),
    ]) {
      final finder = find.byKey(key);
      final size = tester.getSize(finder);
      expect(size.width, greaterThanOrEqualTo(44));
      expect(size.height, greaterThanOrEqualTo(44));

      final button = tester.widget<IconButton>(finder);
      final foreground = button.style?.foregroundColor?.resolve({});
      final background = button.style?.backgroundColor?.resolve({});
      expect(foreground, isNotNull);
      expect(background, isNotNull);
      expect(background!.a, greaterThanOrEqualTo(.70));
      final side = button.style?.side?.resolve({});
      expect(side, isNotNull);
      expect(side!.color.a, greaterThanOrEqualTo(.15));
      final opaqueBackground = Color.alphaBlend(
        background,
        const Color(0xFF080B0A),
      );
      expect(_contrastRatio(foreground!, opaqueBackground), greaterThan(4.5));
    }
    expect(tester.takeException(), isNull);
  });

  testWidgets('图片编辑器顶部图标为白色且按钮不小于 48', (tester) async {
    final image = base64Decode(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=',
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Builder(
          builder: (context) => FilledButton(
            onPressed: () => editImageBeforeSending(context, image),
            child: const Text('打开图片编辑器'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开图片编辑器'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));

    final editor = tester.widget<ProImageEditor>(find.byType(ProImageEditor));
    final editorTheme = editor.configs.theme!;
    expect(editorTheme.appBarTheme.foregroundColor, Colors.white);
    expect(editorTheme.appBarTheme.iconTheme?.color, Colors.white);
    expect(editorTheme.appBarTheme.actionsIconTheme?.color, Colors.white);
    expect(editor.configs.mainEditor.style.appBarColor, Colors.white);
    expect(editor.configs.cropRotateEditor.style.appBarColor, Colors.white);

    final minimumSize = editorTheme.iconButtonTheme.style?.minimumSize?.resolve(
      {},
    );
    expect(minimumSize, isNotNull);
    expect(minimumSize!.width, greaterThanOrEqualTo(48));
    expect(minimumSize.height, greaterThanOrEqualTo(48));
    expect(tester.takeException(), isNull);
  });
}

Future<void> _openImagePreview(WidgetTester tester) async {
  tester.view.physicalSize = const Size(375, 812);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);

  final message = ChatMessage(
    id: 'image-preview-interaction',
    conversationId: 'image-preview-conversation',
    senderId: 'me',
    senderName: '我',
    text: '[图片]',
    sentAt: DateTime(2026, 8, 16, 19, 30),
    isMine: true,
    kind: MessageContentKind.image,
    mediaUrl: 'assets/brand/qingwaguagua-mark-transparent.png',
  );
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: Scaffold(body: MessageBubble(message: message)),
    ),
  );
  await tester.pumpAndSettle();
  await tester.tap(
    find.byKey(const Key('message-image-image-preview-interaction')),
  );
  await tester.pumpAndSettle();
  expect(find.byKey(const Key('message-image-preview')), findsOneWidget);
}

double _contrastRatio(Color first, Color second) {
  final lighter = first.computeLuminance() > second.computeLuminance()
      ? first
      : second;
  final darker = identical(lighter, first) ? second : first;
  return (lighter.computeLuminance() + .05) / (darker.computeLuminance() + .05);
}
