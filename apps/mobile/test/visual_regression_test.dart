import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/image_send_editor.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/im/business_features.dart';
import 'package:linli_im/ui/legal_documents.dart';
import 'package:linli_im/ui/screens/business_channel_screens.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_invite_members_screen.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:linli_im/ui/screens/moments_screen.dart';
import 'package:linli_im/ui/screens/qr_tools_screen.dart';
import 'package:linli_im/ui/screens/settings_preferences.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:linli_im/ui/screens/sticker_store_screen.dart';
import 'package:linli_im/ui/voice_composer_controller.dart';
import 'package:shared_preferences/shared_preferences.dart';

class _GoldenVoiceController extends VoiceComposerController {
  @override
  bool get hasDraft => phase == VoiceComposerPhase.preview;

  void show(VoiceComposerPhase value) {
    phase = value;
    seconds = 12;
    draftSeconds = 12;
    notifyListeners();
  }
}

const _surfaceKey = Key('visual-regression-surface');
final _audioPlayerEventChannels = <EventChannel>[];
Directory? _testStorageDirectory;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late final GoldenFileComparator originalGoldenComparator;

  setUpAll(() async {
    _testStorageDirectory = Directory.systemTemp.createTempSync(
      'linli_visual_regression_',
    );
    originalGoldenComparator = goldenFileComparator;
    if (originalGoldenComparator case final LocalFileComparator comparator) {
      goldenFileComparator = _TolerantLocalFileComparator(
        comparator.basedir.resolve('visual_regression_test.dart'),
      );
    }
    await Future.wait([
      _loadFont('NotoSansSC', 'assets/fonts/NotoSansSC-Regular.otf'),
      _loadFont('NotoColorEmoji', 'assets/fonts/NotoColorEmoji.ttf'),
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
    messenger.setMockMethodCallHandler(
      const MethodChannel('plugins.flutter.io/path_provider'),
      (call) async => switch (call.method) {
        'getTemporaryDirectory' ||
        'getApplicationSupportDirectory' ||
        'getApplicationDocumentsDirectory' ||
        'getApplicationCacheDirectory' ||
        'getExternalStorageDirectory' ||
        'getDownloadsDirectory' => _testStorageDirectory!.path,
        _ => null,
      },
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      MockStreamHandler.inline(onListen: (_, _) {}),
    );
  });

  tearDownAll(() {
    goldenFileComparator = originalGoldenComparator;
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
    messenger.setMockMethodCallHandler(
      const MethodChannel('plugins.flutter.io/path_provider'),
      null,
    );
    for (final channel in _audioPlayerEventChannels) {
      messenger.setMockStreamHandler(channel, null);
    }
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      null,
    );
    final storageDirectory = _testStorageDirectory;
    if (storageDirectory != null && storageDirectory.existsSync()) {
      storageDirectory.deleteSync(recursive: true);
    }
  });

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets('mobile login visual baseline', (tester) async {
    final controller = AppController(_ProductionAuthGoldenRepository());
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

  testWidgets('mobile registration visual baseline', (tester) async {
    final controller = AppController(_ProductionAuthGoldenRepository());
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: RegisterScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-registration-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile reset password visual baseline', (tester) async {
    final controller = AppController(_ProductionAuthGoldenRepository());
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: ResetPasswordScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-reset-password-brand.png'),
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

  testWidgets('mobile contacts visual baseline', (tester) async {
    await _expectMobileHomeTabGolden(
      tester,
      tabIndex: 1,
      goldenName: 'mobile-contacts-brand.png',
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile discover visual baseline', (tester) async {
    await _expectMobileHomeTabGolden(
      tester,
      tabIndex: 2,
      goldenName: 'mobile-discover-brand.png',
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile profile visual baseline', (tester) async {
    await _expectMobileHomeTabGolden(
      tester,
      tabIndex: 3,
      goldenName: 'mobile-profile-brand.png',
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile settings visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: SettingsScreen(controller: controller, onToggleTheme: () {}),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-settings-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile direct chat info visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.id == 'c-linyu',
    );

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: ChatInfoScreen(
        controller: controller,
        conversation: conversation,
        onSearch: () {},
        onClearLocal: () async {},
        onBlock: () async {},
        onScheduledMessages: () {},
      ),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-chat-info-brand.png'),
    );
  }, skip: !Platform.isWindows);

  for (final screen in ['info', 'invite']) {
    testWidgets(
      'mobile group $screen quick invite visual baseline',
      (tester) async {
        final controller = AppController(_GroupMembersGoldenRepository());
        await tester.runAsync(controller.loginAsDemo);
        addTearDown(controller.dispose);
        final group = controller.conversations.firstWhere(
          (c) => c.id == 'c-team',
        );
        await _pumpSurface(
          tester,
          size: const Size(390, 844),
          child: screen == 'info'
              ? ChatInfoScreen(
                  controller: controller,
                  conversation: group,
                  onSearch: () {},
                  onClearLocal: () async {},
                  onBlock: () async {},
                  onScheduledMessages: () {},
                )
              : GroupInviteMembersScreen(
                  controller: controller,
                  conversationId: group.id,
                ),
        );
        await expectLater(
          find.byKey(_surfaceKey),
          matchesGoldenFile(
            'goldens/windows/mobile-group-$screen-quick-invite.png',
          ),
        );
      },
      skip: !Platform.isWindows,
    );
  }

  for (final screen in ['info', 'all']) {
    testWidgets('mobile large group $screen visual baseline', (tester) async {
      final controller = AppController(_LargeGroupGoldenRepository());
      await tester.runAsync(controller.loginAsDemo);
      addTearDown(controller.dispose);
      final group = controller.conversations.firstWhere(
        (c) => c.id == 'c-team',
      );
      await _pumpSurface(
        tester,
        size: const Size(390, 844),
        child: ChatInfoScreen(
          controller: controller,
          conversation: group,
          onSearch: () {},
          onClearLocal: () async {},
          onBlock: () async {},
          onScheduledMessages: () {},
        ),
      );
      if (screen == 'all') {
        await tester.tap(find.byKey(const Key('chat-info-all-members')));
        await tester.pumpAndSettle();
        await tester.runAsync(
          () => Future<void>.delayed(const Duration(milliseconds: 80)),
        );
        await _settle(tester);
      }
      await expectLater(
        find.byKey(_surfaceKey),
        matchesGoldenFile('goldens/windows/mobile-large-group-$screen.png'),
      );
    }, skip: !Platform.isWindows);
  }

  testWidgets('mobile moments visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: MomentsScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-moments-brand.png'),
    );
    // flutter_cache_manager schedules a one-shot cleanup after the first
    // path-provider lookup. Advance the fake clock so the visual test does not
    // leave a framework timer behind.
    await tester.pump(const Duration(seconds: 11));
  }, skip: !Platform.isWindows);

  testWidgets('mobile moments error visual baseline', (tester) async {
    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: Scaffold(body: MomentsErrorState(onRefresh: () async {})),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-moments-error-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile personal QR visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: MyQrCodeScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-personal-qr-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile unavailable legal document visual baseline', (
    tester,
  ) async {
    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: const UnavailableLegalDocumentScreen(
        document: LegalDocument.privacy,
      ),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-legal-unavailable-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile empty sticker store visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: StickerStoreScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-sticker-store-empty.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile empty business channel visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: BusinessChannelHubScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-business-channel-empty.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile empty support center visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: SupportCenterScreen(controller: controller),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-support-center-empty.png'),
    );
  }, skip: !Platform.isWindows);

  for (final phase in [
    VoiceComposerPhase.recording,
    VoiceComposerPhase.canceling,
    VoiceComposerPhase.preview,
  ]) {
    testWidgets('mobile voice ${phase.name} visual baseline', (tester) async {
      final voice = _GoldenVoiceController();
      final text = TextEditingController();
      await _pumpSurface(
        tester,
        size: const Size(390, 844),
        child: Scaffold(
          appBar: AppBar(
            title: const Text('语音消息'),
            leading: const BackButton(),
          ),
          body: Column(
            children: [
              const Expanded(child: Center(child: Text('按住说话，松开后可试听'))),
              ChatComposer(
                controller: text,
                voiceController: voice,
                onSend: () {},
                onToggleAttachments: () {},
                onToggleEmoji: () {},
                onAttachment: (_) {},
                onVoiceReady: (_) {},
                onCancelReply: () {},
              ),
            ],
          ),
        ),
      );
      await tester.tap(find.byKey(const Key('voice-mode-button')));
      await tester.pump();
      voice.show(phase);
      await _settle(tester);
      await expectLater(
        find.byKey(_surfaceKey),
        matchesGoldenFile('goldens/windows/mobile-voice-${phase.name}.png'),
      );
      await tester.pumpWidget(const SizedBox.shrink());
      voice.dispose();
      text.dispose();
    }, skip: !Platform.isWindows);
  }

  testWidgets('mobile group chat visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: ChatScreen(
        controller: controller,
        conversation: conversation,
        chatBackgroundOverride: ChatBackgroundStyle.followSystem,
      ),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-group-chat-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('message reaction chips visual baseline', (tester) async {
    await _pumpSurface(
      tester,
      size: const Size(390, 240),
      child: Scaffold(
        body: Padding(
          padding: const EdgeInsets.all(20),
          child: Align(
            alignment: Alignment.topLeft,
            child: MessageBubble(
              message: ChatMessage(
                id: 'reaction-preview',
                conversationId: 'group-preview',
                senderId: 'friend-1',
                senderName: '呱呱',
                text: '这条消息有多个回应',
                sentAt: DateTime(2026, 9, 4, 11),
                isMine: false,
                reactions: const [
                  MessageReaction(emoji: '❤️', count: 1, reactedByMe: false),
                  MessageReaction(emoji: '👍', count: 3, reactedByMe: true),
                ],
              ),
              onReactionTap: (_) {},
              onAddReaction: () {},
            ),
          ),
        ),
      ),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/message-reaction-chips.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('mobile robot command menu visual baseline', (tester) async {
    final controller = AppController(_RobotGoldenRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      child: ChatScreen(
        controller: controller,
        conversation: _RobotGoldenRepository.conversation,
        chatBackgroundOverride: ChatBackgroundStyle.followSystem,
      ),
    );
    await tester.tap(find.byKey(const Key('robot-menu-toggle')));
    await _settle(tester);

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/mobile-robot-menu-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('group identity dark large text visual baseline', (tester) async {
    final controller = AppController(_GroupIdentityGoldenRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere((c) => c.id == 'c-team');
    await _pumpSurface(
      tester,
      size: const Size(390, 844),
      brightness: Brightness.dark,
      textScale: 2,
      child: ChatScreen(controller: controller, conversation: group),
    );
    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/group-identity-dark-large.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('group identity narrow list visual baseline', (tester) async {
    final controller = AppController(_GroupIdentityGoldenRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere((c) => c.id == 'c-team');
    final direct = controller.conversations.firstWhere(
      (c) => c.id == 'c-linyu',
    );
    await _pumpSurface(
      tester,
      size: const Size(320, 400),
      textScale: 2,
      child: Scaffold(
        body: Column(
          children: [
            ConversationTile(
              conversation: group,
              controller: controller,
              highlighted: true,
            ),
            ConversationTile(
              conversation: direct.copyWith(
                title: group.title,
                avatarUrl: group.avatarUrl,
              ),
              controller: controller,
            ),
          ],
        ),
      ),
    );
    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/group-identity-narrow-list.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('desktop QR login visual baseline', (tester) async {
    final controller = AppController(_ProductionAuthGoldenRepository());
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(1280, 720),
      child: LoginScreen(controller: controller),
    );
    await tester.tap(find.text('扫码登录'));
    await _settle(tester);

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/desktop-qr-login-brand.png'),
    );
  }, skip: !Platform.isWindows);

  testWidgets('desktop conversation workspace visual baseline', (tester) async {
    final controller = await _authenticatedController(tester);
    addTearDown(controller.dispose);

    await _pumpSurface(
      tester,
      size: const Size(1440, 1000),
      child: HomeScreen(
        controller: controller,
        onToggleTheme: () {},
        chatBackgroundOverride: ChatBackgroundStyle.followSystem,
      ),
    );

    await expectLater(
      find.byKey(_surfaceKey),
      matchesGoldenFile('goldens/windows/desktop-conversations-brand.png'),
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

class _TolerantLocalFileComparator extends LocalFileComparator {
  _TolerantLocalFileComparator(super.testFile);

  static const _maxDiffPercent = 0.003;

  @override
  Future<bool> compare(Uint8List imageBytes, Uri golden) async {
    final result = await GoldenFileComparator.compareLists(
      imageBytes,
      await getGoldenBytes(golden),
    );
    if (result.passed || result.diffPercent <= _maxDiffPercent) {
      result.dispose();
      return true;
    }

    final error = await generateFailureOutput(result, golden, basedir);
    result.dispose();
    throw FlutterError(error);
  }
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

Future<void> _expectMobileHomeTabGolden(
  WidgetTester tester, {
  required int tabIndex,
  required String goldenName,
}) async {
  final controller = await _authenticatedController(tester);
  addTearDown(controller.dispose);

  await _pumpSurface(
    tester,
    size: const Size(390, 844),
    child: HomeScreen(controller: controller, onToggleTheme: () {}),
  );
  await tester.tap(find.byKey(Key('home-tab-$tabIndex')));
  await _settle(tester);

  await expectLater(
    find.byKey(_surfaceKey),
    matchesGoldenFile('goldens/windows/$goldenName'),
  );
}

class _GroupIdentityGoldenRepository extends _GoldenRepository {
  @override
  Future<List<Conversation>> conversations() async => [
    for (final c in await super.conversations())
      c.id == 'c-team'
          ? c.copyWith(
              title: '这是一个用于区分单聊和群聊的长名称',
              memberCount: 11,
              unread: 99,
              muted: false,
            )
          : c,
  ];
}

class _GroupMembersGoldenRepository extends _GoldenRepository {
  @override
  Future<List<Conversation>> conversations() async => [
    // Demo previews omit self but groupMembers includes self. The real server's
    // memberCount includes everybody, independent of the preview size.
    for (final c in await super.conversations())
      c.id == 'c-team' ? c.copyWith(memberCount: 5) : c,
  ];
}

class _LargeGroupGoldenRepository extends _GoldenRepository {
  final members = List.generate(
    11,
    (index) => GroupMember(
      user: index == 0
          ? DemoImRepository.demoUser
          : AppUser(
              id: 'large-$index',
              name: '测试群友$index',
              handle: 'user$index',
              presence: '',
            ),
      role: index == 0 ? 'owner' : 'member',
      joinedAt: DateTime(2026),
    ),
  );
  @override
  Future<List<GroupMember>> groupMembers(String id) async => List.of(members);
  @override
  Future<List<Conversation>> conversations() async => [
    for (final c in await super.conversations())
      c.id == 'c-team'
          ? c.copyWith(
              members: members.take(8).map((m) => m.user).toList(),
              memberCount: 11,
            )
          : c,
  ];
}

class _GoldenRepository extends DemoImRepository
    implements BusinessFeatureRepository {
  _GoldenRepository() : super(latency: Duration.zero);

  @override
  Future<MomentPage> moments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  }) async => MomentPage(
    items: [
      MomentSummary(
        id: 'moment-linyu',
        authorId: 'u-linyu',
        authorName: '林屿',
        content: '刚把新版社区空间的交互细节收完，欢迎一起体验。',
        mediaKind: 'none',
        media: const [],
        visibility: 'public',
        visibleUserIds: const [],
        location: const {'name': '滨江创意园'},
        likeCount: 12,
        commentCount: 2,
        likedByMe: false,
        comments: const [],
        status: 'published',
        createdAt: DateTime(2024, 6, 18, 9, 0),
        updatedAt: DateTime(2024, 6, 18, 9, 0),
      ),
      MomentSummary(
        id: 'moment-xuyan',
        authorId: 'u-xuyan',
        authorName: '许言',
        content: '周末沿江散步，天气刚刚好。',
        mediaKind: 'none',
        media: const [],
        visibility: 'public',
        visibleUserIds: const [],
        location: const {},
        likeCount: 8,
        commentCount: 1,
        likedByMe: true,
        comments: const [],
        status: 'published',
        createdAt: DateTime(2024, 6, 17, 17, 30),
        updatedAt: DateTime(2024, 6, 17, 17, 30),
      ),
    ],
    nextCursor: '',
  );

  @override
  Future<List<StickerCategorySummary>> stickerCategories() async => const [];

  @override
  Future<List<StickerPackSummary>> stickerPacks({
    String categoryId = '',
  }) async => const [];

  @override
  Future<List<StickerItemSummary>> recentStickers({int limit = 50}) async =>
      const [];

  @override
  Future<List<StickerItemSummary>> favoriteStickers({int limit = 50}) async =>
      const [];

  @override
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  }) async => const [];

  @override
  Future<List<SupportSkillGroupSummary>> supportSkillGroups() async => const [];

  @override
  Future<List<SupportSessionSummary>> supportSessions({
    String status = '',
    String skillGroupId = '',
  }) async => const [];

  @override
  Future<List<SupportAgentSummary>> supportAgents({
    String skillGroupId = '',
  }) async => const [];

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

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _ProductionAuthGoldenRepository extends _GoldenRepository {
  @override
  bool get isDemo => false;

  @override
  bool get supportsDemo => false;

  @override
  Future<QrLoginTicket> createQrLoginTicket({
    required String clientName,
  }) async => QrLoginTicket(
    id: 'qrlogin-golden',
    qrPayload: 'qingwaguagua://login/ql_visual_regression',
    pollToken: 'qp_visual_regression',
    expiresAt: DateTime(2099, 1, 1),
  );

  @override
  Future<AppUser?> pollQrLoginTicket(QrLoginTicket ticket) async => null;
}

class _RobotGoldenRepository extends _GoldenRepository {
  static final conversation = Conversation(
    id: 'conversation-service-helper',
    title: '服务助手',
    subtitle: '请选择服务',
    updatedAt: DateTime(2026, 8, 16, 15),
    kind: ConversationKind.direct,
    channelId: 'robot-service-helper',
    channelType: 1,
    members: const [
      AppUser(
        id: 'robot-service-helper',
        name: '服务助手',
        handle: 'service_helper',
        presence: '在线',
      ),
    ],
  );

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => const [];

  @override
  Future<List<RobotProfile>> robotProfiles(String conversationId) async =>
      conversationId == conversation.id
      ? const [
          RobotProfile(
            id: 'robot-service-helper',
            name: '服务助手',
            username: 'service_helper',
            placeholder: '请选择服务',
            version: 2,
            menus: [
              RobotMenu(
                robotId: 'robot-service-helper',
                command: '查询订单',
                remark: '订单查询',
              ),
              RobotMenu(
                robotId: 'robot-service-helper',
                command: '联系客服',
                remark: '人工服务',
              ),
              RobotMenu(
                robotId: 'robot-service-helper',
                command: '常见问题',
                remark: '使用帮助',
              ),
            ],
          ),
        ]
      : const [];
}

Future<void> _pumpSurface(
  WidgetTester tester, {
  required Size size,
  required Widget child,
  Brightness brightness = Brightness.light,
  double textScale = 1,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);

  await tester.pumpWidget(
    MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: buildLinliTheme(brightness, fontFamily: 'NotoSansSC'),
      builder: (context, navigator) => MediaQuery(
        data: MediaQueryData(
          size: size,
          devicePixelRatio: 1,
          disableAnimations: true,
          textScaler: TextScaler.linear(textScale),
        ),
        child: RepaintBoundary(
          key: _surfaceKey,
          child: navigator ?? const SizedBox.shrink(),
        ),
      ),
      home: child,
    ),
  );
  await tester.runAsync(
    () => Future<void>.delayed(const Duration(milliseconds: 80)),
  );
  await _settle(tester);
  // Lazy list items can be created by a post-frame jump after the first asset
  // wait. Give newly visible avatars and message media one real async turn too.
  await tester.runAsync(
    () => Future<void>.delayed(const Duration(milliseconds: 80)),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var index = 0; index < 6; index++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}
