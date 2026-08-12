import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test(
    'voice cancel threshold only activates after a deliberate upward drag',
    () {
      expect(voiceRecordingShouldCancel(-63.9), isFalse);
      expect(voiceRecordingShouldCancel(-64), isTrue);
      expect(voiceRecordingShouldCancel(20), isFalse);
    },
  );

  testWidgets('composer switches between keyboard and hold-to-talk', (
    tester,
  ) async {
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(_composer(controller: controller));

    expect(find.byKey(const Key('message-input')), findsOneWidget);
    await tester.tap(find.byKey(const Key('voice-mode-button')));
    await tester.pump();

    expect(find.byKey(const Key('message-input')), findsNothing);
    expect(find.byKey(const Key('hold-to-talk')), findsOneWidget);
    expect(find.text('按住说话'), findsOneWidget);

    await tester.tap(find.byKey(const Key('voice-mode-button')));
    await tester.pump();
    expect(find.byKey(const Key('message-input')), findsOneWidget);
  });

  testWidgets(
    'emoji panel supports categories, insertion and grapheme backspace',
    (tester) async {
      final controller = TextEditingController();
      addTearDown(controller.dispose);
      await tester.pumpWidget(
        _composer(controller: controller, showEmoji: true),
      );
      await tester.pump();

      expect(find.byKey(const Key('emoji-grid')), findsOneWidget);
      expect(find.byKey(const Key('emoji-category-2')), findsOneWidget);
      await tester.tap(find.text('❤️').first);
      await tester.pump();
      expect(controller.text, '❤️');

      await tester.tap(find.byKey(const Key('emoji-backspace')));
      await tester.pump();
      expect(controller.text, isEmpty);
      expect(find.byKey(const Key('emoji-send')), findsOneWidget);
    },
  );

  testWidgets('attachment panel exposes all production actions', (
    tester,
  ) async {
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      _composer(controller: controller, showAttachments: true),
    );

    for (final label in ['相册', '拍摄', '视频', '文件', '位置', '名片']) {
      expect(find.text(label), findsOneWidget);
    }
    expect(find.text('待接入'), findsNothing);
  });

  testWidgets('desktop send action submits the current text', (tester) async {
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    var sends = 0;
    await tester.pumpWidget(
      _composer(controller: controller, onSend: () => sends++),
    );
    await tester.enterText(find.byKey(const Key('message-input')), '键盘发送');
    await tester.pump();
    final input = tester.widget<TextField>(
      find.byKey(const Key('message-input')),
    );
    expect(input.textInputAction, TextInputAction.send);
    input.onSubmitted!.call(controller.text);
    expect(sends, 1);
  });

  testWidgets(
    'composer reports typing only while focused with non-empty text',
    (tester) async {
      final controller = TextEditingController();
      addTearDown(controller.dispose);
      final states = <bool>[];
      await tester.pumpWidget(
        _composer(controller: controller, onTypingChanged: states.add),
      );

      await tester.tap(find.byKey(const Key('message-input')));
      await tester.enterText(find.byKey(const Key('message-input')), '正在输入');
      await tester.pump();
      expect(states, contains(true));

      FocusManager.instance.primaryFocus?.unfocus();
      await tester.pump();
      expect(states.last, isFalse);
    },
  );

  testWidgets('composer panels remain usable on a narrow 200% text layout', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = TextEditingController();
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      _composer(
        controller: controller,
        showAttachments: true,
        textScaler: const TextScaler.linear(2),
      ),
    );
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(
      _composer(
        controller: controller,
        showEmoji: true,
        textScaler: const TextScaler.linear(2),
      ),
    );
    expect(tester.takeException(), isNull);
  });
}

Widget _composer({
  required TextEditingController controller,
  VoidCallback? onSend,
  ValueChanged<bool>? onTypingChanged,
  bool showEmoji = false,
  bool showAttachments = false,
  TextScaler textScaler = TextScaler.noScaling,
}) => MaterialApp(
  home: Scaffold(
    body: MediaQuery(
      data: MediaQueryData(textScaler: textScaler),
      child: Align(
        alignment: Alignment.bottomCenter,
        child: ChatComposer(
          controller: controller,
          showEmoji: showEmoji,
          showAttachments: showAttachments,
          onSend: onSend ?? () {},
          onTypingChanged: onTypingChanged,
          onToggleAttachments: () {},
          onToggleEmoji: () {},
          onAttachment: (_) {},
          onVoiceReady: (MediaUpload _) {},
          onCancelReply: () {},
        ),
      ),
    ),
  ),
);
