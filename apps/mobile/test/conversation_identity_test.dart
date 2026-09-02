import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/widgets/conversation_identity.dart';
import 'package:linli_im/ui/widgets/linli_widgets.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _longName = '同名会话这是一个需要省略但仍然能够识别类型的很长名称';

Conversation _conversation({
  String id = 'c-team',
  ConversationKind kind = ConversationKind.group,
  int channelType = 2,
}) => Conversation(
  id: id,
  title: _longName,
  subtitle: '最近一条消息',
  updatedAt: DateTime.now().subtract(const Duration(days: 30)),
  kind: kind,
  channelType: channelType,
  memberCount: 11,
  members: [DemoImRepository.people.first],
  avatarUrl: 'assets/avatars/an-ran.png',
  unread: 99,
  muted: true,
  pinned: true,
  currentUserRole: 'owner',
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const record = MethodChannel('com.llfbandit.record/messages');
  setUpAll(() => messenger.setMockMethodCallHandler(record, (_) async => null));
  tearDownAll(() => messenger.setMockMethodCallHandler(record, null));
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('普通群聊依赖类型，不依赖群名、成员数或预览数量', () {
    expect(
      isOrdinaryGroupConversation(_conversation().copyWith(memberCount: 1)),
      isTrue,
    );
    expect(
      isOrdinaryGroupConversation(
        _conversation(kind: ConversationKind.direct, channelType: 1),
      ),
      isFalse,
    );
    for (final channelType in [3, 4, 5, 6, 9, 10, 11, 12]) {
      expect(
        isOrdinaryGroupConversation(_conversation(channelType: channelType)),
        isFalse,
      );
    }
  });

  testWidgets('同名同头像仅群聊有角标和标签，保留尺寸、原头像及单聊在线点', (tester) async {
    final group = _conversation();
    final direct = _conversation(
      id: 'direct',
      kind: ConversationKind.direct,
      channelType: 1,
    );
    final business = _conversation(id: 'support', channelType: 10);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Column(
            children: [
              for (final c in [group, direct, business])
                Row(
                  children: [
                    ConversationAvatar(
                      key: ValueKey(c.id),
                      conversation: c,
                      name: _longName,
                      size: 48,
                      avatarUrl: c.avatarUrl,
                      online: true,
                    ),
                    Expanded(
                      child: ConversationTitle(
                        conversation: c,
                        name: _longName,
                      ),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
    expect(find.byType(ConversationTypeBadge), findsOneWidget);
    expect(find.byIcon(CupertinoIcons.group_solid), findsOneWidget);
    for (final c in [group, direct, business]) {
      final avatar = find.byKey(ValueKey(c.id));
      expect(tester.getSize(avatar), const Size.square(48));
      final person = tester.widget<PersonAvatar>(
        find.descendant(of: avatar, matching: find.byType(PersonAvatar)),
      );
      expect(person.avatarUrl, 'assets/avatars/an-ran.png');
      expect(person.online, c != group);
    }
    final semantics = tester.ensureSemantics();
    expect(
      tester
          .getSemantics(find.byType(ConversationTitle).first)
          .getSemanticsData()
          .label,
      '群聊，$_longName',
    );
    semantics.dispose();
    expect(tester.takeException(), isNull);
  });

  for (final brightness in Brightness.values) {
    for (final width in [320.0, 390.0]) {
      testWidgets('$brightness $width 置顶长群名、类型、时间和未读在大字体下不溢出', (tester) async {
        final controller = AppController(_IdentityRepository());
        await tester.runAsync(controller.loginAsDemo);
        addTearDown(controller.dispose);
        final conversation = controller.conversations.firstWhere(
          (c) => c.id == 'c-team',
        );
        tester.view.physicalSize = Size(width, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        for (final scale in [1.0, 2.0]) {
          await tester.pumpWidget(
            MaterialApp(
              theme: buildLinliTheme(brightness),
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(
                  context,
                ).copyWith(textScaler: TextScaler.linear(scale)),
                child: child!,
              ),
              home: Scaffold(
                body: ConversationTile(
                  conversation: conversation,
                  controller: controller,
                  highlighted: true,
                ),
              ),
            ),
          );
          await tester.pumpAndSettle();
          expect(find.byType(ConversationTypeBadge), findsOneWidget);
          expect(
            find.byKey(const ValueKey('conversation-group-mark-c-team')),
            findsOneWidget,
          );
          expect(
            find.byKey(const ValueKey('conversation-pinned-indicator-c-team')),
            findsOneWidget,
          );
          expect(
            find.text('置顶'),
            width < 360 && scale > 1.4 ? findsNothing : findsOneWidget,
          );
          expect(
            tester
                .getSize(
                  find.byKey(const ValueKey('conversation-title-c-team')),
                )
                .width,
            greaterThanOrEqualTo(24),
            reason: '群名不能被标签、时间和置顶挤没',
          );
          expect(find.byIcon(CupertinoIcons.bell_slash_fill), findsOneWidget);
          expect(find.text('99'), findsNothing, reason: '免打扰保留原来的未读圆点');
          expect(
            tester.getSize(
              find.byKey(const ValueKey('conversation-avatar-c-team')),
            ),
            const Size.square(48),
          );
          final labelRect = tester.getRect(find.byType(ConversationTypeBadge));
          expect(labelRect.right, lessThan(width));
          expect(labelRect.width, greaterThan(20));
          expect(tester.takeException(), isNull);
        }
        await tester.pumpWidget(const SizedBox());
      });
    }
  }

  for (final (width, panelWidth, scale, brightness) in [
    (390.0, 390.0, 1.0, Brightness.light),
    (390.0, 390.0, 2.0, Brightness.dark),
    (1280.0, 360.0, 2.0, Brightness.dark),
  ]) {
    testWidgets('$width 窄聊天列 $panelWidth 字体 $scale 群标识和输入状态共存', (tester) async {
      final repository = _IdentityRepository();
      final controller = AppController(repository);
      await tester.runAsync(controller.loginAsDemo);
      addTearDown(controller.dispose);
      tester.view.physicalSize = Size(width, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final group = controller.conversations.firstWhere(
        (c) => c.id == 'c-team',
      );
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(brightness),
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(scale)),
            child: child!,
          ),
          home: Align(
            alignment: Alignment.centerRight,
            child: SizedBox(
              width: panelWidth,
              child: ChatScreen(controller: controller, conversation: group),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byType(ConversationTypeBadge), findsOneWidget);
      expect(find.text('11 位成员'), findsOneWidget);
      expect(
        tester.getSize(find.byType(ConversationAvatar)),
        const Size.square(34),
      );
      expect(find.byKey(const Key('pinned-messages-button')), findsOneWidget);
      expect(tester.takeException(), isNull);
      repository.updates.add(
        const ImEvent(
          type: ImEventType.typing,
          payload: {'conversationId': 'c-team', 'userId': 'u1', 'typing': true},
        ),
      );
      await tester.pump();
      await tester.pump();
      expect(find.textContaining('正在输入'), findsOneWidget);
      expect(find.byType(ConversationTypeBadge), findsOneWidget);
      repository.updates.add(
        const ImEvent(
          type: ImEventType.typing,
          payload: {
            'conversationId': 'c-team',
            'userId': 'u1',
            'typing': false,
          },
        ),
      );
      await tester.pump();
      await tester.pump();
      expect(find.text('11 位成员'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    });
  }
}

class _IdentityRepository extends DemoImRepository {
  _IdentityRepository() : super(latency: Duration.zero);
  final updates = StreamController<ImEvent>.broadcast();
  @override
  Stream<ImEvent> get events => updates.stream;
  @override
  Future<List<Conversation>> conversations() async => [
    _conversation(),
    _conversation(id: 'c-linyu', kind: ConversationKind.direct, channelType: 1),
  ];
  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [];
  @override
  Future<void> close() async {
    await updates.close();
    await super.close();
  }
}
