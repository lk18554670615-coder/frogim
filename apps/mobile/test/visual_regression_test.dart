import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/image_send_editor.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _surfaceKey = Key('visual-regression-surface');
final _audioPlayerEventChannels = <EventChannel>[];

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    await Future.wait([
      _loadFont('NotoSansSC', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont('.SF Pro Text', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont('.SF Pro Display', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont('CupertinoSystemText', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont(
        'CupertinoSystemDisplay',
        'assets/fonts/NotoSansSC-Regular.otf',
      ),
      _loadFont('Roboto', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont('MaterialIcons', 'fonts/MaterialIcons-Regular.otf'),
      _loadFont(
        'packages/cupertino_icons/CupertinoIcons',
        'packages/cupertino_icons/assets/CupertinoIcons.ttf',
      ),
      _loadFont(
        'packages/pro_image_editor/ProImageEditorIcons',
        'packages/pro_image_editor/assets/fonts/ProImageEditorIcons.ttf',
      ),
    ]);
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('com.llfbandit.record/messages'),
      (_) async => null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      (_) async => null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      (call) async {
        if (call.method == 'create') {
          final arguments = call.arguments! as Map<Object?, Object?>;
          final playerId = arguments['playerId']! as String;
          final channel = EventChannel(
            'xyz.luan/audioplayers/events/$playerId',
          );
          _audioPlayerEventChannels.add(channel);
          messenger.setMockStreamHandler(
            channel,
            MockStreamHandler.inline(onListen: (_, _) {}),
          );
        }
        return null;
      },
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      MockStreamHandler.inline(onListen: (_, _) {}),
    );
  });

  tearDownAll(() {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('com.llfbandit.record/messages'),
      null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      null,
    );
    for (final channel in _audioPlayerEventChannels) {
      messenger.setMockStreamHandler(channel, null);
    }
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      null,
    );
  });

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets('mobile login visual baseline', (tester) async {
    final controller = AppController(_GoldenRepository());
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: LoginScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-login.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile conversations visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: HomeScreen(controller: controller, onToggleTheme: () {}),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-conversations.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile group chat visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: ChatScreen(controller: controller, conversation: conversation),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-group-chat.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('desktop conversation workspace visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(1440, 1000),
      child: HomeScreen(controller: controller, onToggleTheme: () {}),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/desktop-conversations.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('send-time image editor visual baseline', (tester) async {
    final asset = await rootBundle.load('assets/avatars/weekend-coffee.png');
    final source = asset.buffer.asUint8List(
      asset.offsetInBytes,
      asset.lengthInBytes,
    );
    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: Builder(
        builder: (context) => Center(
          child: ElevatedButton(
            onPressed: () => editImageBeforeSending(context, source),
            child: const Text('Open editor'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('Open editor'));
    await _settle(tester);
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 500)),
    );
    await _settle(tester);
    expect(find.byType(CircularProgressIndicator), findsNothing);

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/image-editor.png'),
    );
  }, skip: !Platform.isWindows);
}

Future<void> _loadFont(String family, String assetPath) async {
  final font = await rootBundle.load(assetPath);
  await (FontLoader(family)..addFont(Future.value(font))).load();
}

Future<AppController> _authenticatedController(WidgetTester tester) async {
  final controller = AppController(_GoldenRepository());
  await tester.runAsync(controller.loginAsDemo);
  return controller;
}

class _GoldenRepository extends DemoImRepository {
  _GoldenRepository() : super(latency: Duration.zero);

  @override
  Future<List<ChatMessage>> messages(String conversationId) async {
    final messages = await super.messages(conversationId);
    final firstMessageAt = DateTime(2024, 6, 18, 9, 0);
    return [
      for (var index = 0; index < messages.length; index++)
        ChatMessage.fromJson({
          ...messages[index].toJson(),
          'sentAt': firstMessageAt
              .add(Duration(minutes: index * 5))
              .toIso8601String(),
        }),
    ];
  }
}

Future<void> _pumpSurface(
  WidgetTester tester, {
  required Size size,
  required Widget child,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);

  await tester.pumpWidget(
    MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: buildLinliTheme(Brightness.light, fontFamily: 'NotoSansSC'),
      builder: (context, navigator) => MediaQuery(
        data: MediaQueryData(
          size: size,
          devicePixelRatio: 1,
          disableAnimations: true,
        ),
        child: RepaintBoundary(
          key: _surfaceKey,
          child: navigator ?? const SizedBox.shrink(),
        ),
      ),
      home: child,
    ),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var index = 0; index < 6; index++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}
