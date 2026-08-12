import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/image_send_editor.dart';
import 'package:pro_image_editor/pro_image_editor.dart';

void main() {
  testWidgets('send-time editor exposes every required image tool', (
    tester,
  ) async {
    final image = base64Decode(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=',
    );
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
    await tester.pump(const Duration(milliseconds: 500));

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
  });
}
