@TestOn('browser')
library;

import 'dart:async';
import 'dart:js_interop';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/web_drop_paste.dart';
import 'package:web/web.dart' as web;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('browser file drag shows the drop target and reads exact bytes', (
    tester,
  ) async {
    final received = Completer<List<WebPickedFile>>();
    final errors = <String>[];
    await tester.pumpWidget(
      MaterialApp(
        home: WebDropPasteRegion(
          onFiles: (files) async => received.complete(files),
          onError: errors.add,
          child: const ColoredBox(color: Colors.white),
        ),
      ),
    );

    final bytes = Uint8List.fromList(<int>[0x4c, 0x49, 0x4e, 0x4c, 0x49]);
    final transfer = web.DataTransfer();
    final file = web.File(
      <web.BlobPart>[bytes.toJS].toJS,
      'drag-proof.txt',
      web.FilePropertyBag(type: 'text/plain'),
    );
    transfer.items.add(file);
    expect(transfer.files.length, 1);

    final enter = web.DragEvent(
      'dragenter',
      web.DragEventInit(
        bubbles: true,
        cancelable: true,
        dataTransfer: transfer,
      ),
    );
    web.document.body!.dispatchEvent(enter);
    await tester.pump();
    expect(enter.defaultPrevented, isTrue);
    expect(find.text('松开发送到当前会话'), findsOneWidget);

    final drop = web.DragEvent(
      'drop',
      web.DragEventInit(
        bubbles: true,
        cancelable: true,
        dataTransfer: transfer,
      ),
    );
    web.document.body!.dispatchEvent(drop);
    final files = await tester.runAsync(
      () => received.future.timeout(const Duration(seconds: 5)),
    );
    await tester.pumpAndSettle();

    expect(drop.defaultPrevented, isTrue);
    expect(files, isNotNull);
    expect(files!.single.name, 'drag-proof.txt');
    expect(files.single.mimeType, 'text/plain');
    expect(files.single.bytes, bytes);
    expect(errors, isEmpty);
    expect(find.text('松开发送到当前会话'), findsNothing);
  });
}
