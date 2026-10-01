import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/web_emoji_font.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets(
    'emoji download starts after the first frame and never blocks it',
    (tester) async {
      final gate = Completer<void>();
      var calls = 0;
      final font = WebEmojiFont(
        isWeb: true,
        loadFont: () {
          calls++;
          return gate.future;
        },
      );
      font.loadAfterFirstFrame();
      font.loadAfterFirstFrame();
      expect(calls, 0);
      await tester.pumpWidget(
        const Directionality(
          textDirection: TextDirection.ltr,
          child: Text('中文首屏'),
        ),
      );
      await tester.pump(const Duration(milliseconds: 1));
      expect(find.text('中文首屏'), findsOneWidget);
      expect(calls, 1);
      final pending = font.load();
      expect(calls, 1);
      gate.complete();
      await pending;
      await font.load();
      expect(calls, 1);
    },
  );

  test(
    'a failed background load can retry, including synchronous failures',
    () async {
      var calls = 0;
      final font = WebEmojiFont(
        isWeb: true,
        loadFont: () {
          calls++;
          if (calls == 1) {
            throw StateError('offline');
          }
          if (calls == 2) {
            return Future<void>.error(StateError('still offline'));
          }
          return Future<void>.value();
        },
      );
      await font.load();
      await font.load();
      await font.load();
      await font.load();
      expect(calls, 3);
    },
  );

  testWidgets('native startup does not download or schedule another font', (
    tester,
  ) async {
    var calls = 0;
    final font = WebEmojiFont(
      isWeb: false,
      loadFont: () async {
        calls++;
      },
    );
    font.loadAfterFirstFrame();
    await font.load();
    await tester.pump();
    expect(calls, 0);
  });
}
