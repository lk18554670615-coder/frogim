import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/widgets/forward_conversation_sheet.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/forward_fakes.dart';

const confirmKey = Key('forward-conversation-confirm');
const searchKey = Key('forward-conversation-search');
const allKey = Key('forward-conversation-select-all');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('复选可切换，确认才发送，取消不会发出请求', (tester) async {
    final fixture = await openForwardSheet(tester);
    expect(
      tester.widget<FilledButton>(find.byKey(confirmKey)).onPressed,
      isNull,
    );
    await tester.tap(find.byKey(const Key('forward-conversation-target-0')));
    await tester.tap(find.byKey(const Key('forward-conversation-target-1')));
    await tester.pump();
    expect(find.text('转发（2）'), findsOneWidget);
    expect(fixture.repository.requests, isEmpty);
    await tester.tap(find.byKey(const Key('forward-conversation-target-0')));
    await tester.pump();
    expect(find.text('转发（1）'), findsOneWidget);
    await tester.tap(find.byKey(const Key('forward-conversation-cancel')));
    await tester.pumpAndSettle();
    expect(find.text('选择转发对象'), findsNothing);
    expect(fixture.repository.requests, isEmpty);
  });

  testWidgets('搜索全选和取消全选不影响其他结果的选中状态', (tester) async {
    final fixture = await openForwardSheet(
      tester,
      targets: [
        forwardConversation(0, title: '好友甲'),
        forwardConversation(1, title: '工作群甲'),
        forwardConversation(2, title: '工作群乙'),
        forwardConversation(3, title: '已归档群', archived: true),
      ],
    );
    await tester.tap(find.byKey(const Key('forward-conversation-target-0')));
    await tester.enterText(find.byKey(searchKey), '工作群');
    await tester.pump();
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    expect(find.text('转发（3）'), findsOneWidget);
    expect(find.text('取消全选'), findsOneWidget);
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    expect(find.text('转发（1）'), findsOneWidget);
    await tester.enterText(find.byKey(searchKey), '没有匹配');
    await tester.pump();
    expect(find.text('没有匹配的会话'), findsOneWidget);
    expect(tester.widget<TextButton>(find.byKey(allKey)).onPressed, isNull);
    expect(find.text('转发（1）'), findsOneWidget);
    await tester.enterText(find.byKey(searchKey), '');
    await tester.pump();
    expect(
      find.byKey(const Key('forward-conversation-target-3')),
      findsNothing,
    );
    await tester.tap(find.byKey(confirmKey));
    await tester.pumpAndSettle();
    expect(fixture.repository.requests.single.targetId, 'target-0');
  });

  for (final mode in ['separate', 'merged']) {
    testWidgets('$mode 全选包含屏幕外125个目标，源消息顺序和模式不变', (tester) async {
      final fixture = await openForwardSheet(
        tester,
        count: 125,
        messages: [forwardSource(2), forwardSource(1)],
        mode: mode,
      );
      expect(
        find.byKey(const Key('forward-conversation-target-124')),
        findsNothing,
      );
      await tester.tap(find.byKey(allKey));
      await tester.pump();
      expect(find.text('转发（125）'), findsOneWidget);
      expect(fixture.repository.requests, isEmpty);
      await tester.tap(find.byKey(confirmKey));
      await tester.pumpAndSettle();
      expect(fixture.repository.requests, hasLength(125));
      expect(fixture.repository.requests.every((r) => r.mode == mode), isTrue);
      expect(fixture.repository.requests.first.sourceIds, [
        'source-2',
        'source-1',
      ]);
      expect(find.text('选择转发对象'), findsNothing);
      expect(find.text('转发进度'), findsNothing);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('发送防重复并拦截返回，停止后显示未发送对象且可以继续', (tester) async {
    final fixture = await openForwardSheet(tester, count: 8);
    final gate = Completer<void>();
    fixture.repository.onForward = (request) async {
      await gate.future;
      return fixture.repository.responseFor(request);
    };
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    final send = tester.widget<FilledButton>(find.byKey(confirmKey)).onPressed!;
    send();
    send();
    await tester.pump();
    expect(fixture.repository.requests, hasLength(3));
    expect(find.byKey(searchKey), findsNothing);
    await tester.binding.handlePopRoute();
    await tester.pump();
    expect(find.text('转发进度'), findsOneWidget);
    await tester.tap(find.byKey(const Key('forward-stop')));
    await tester.pump();
    expect(find.text('正在停止…'), findsOneWidget);
    gate.complete();
    await tester.pumpAndSettle();
    expect(fixture.repository.requests, hasLength(3));
    expect(find.text('成功 3 · 失败 0 · 未发送 5'), findsOneWidget);
    await tester.tap(find.byKey(const Key('forward-retry')));
    await tester.pumpAndSettle();
    expect(fixture.repository.requests, hasLength(8));
    expect(find.text('转发进度'), findsNothing);
  });

  testWidgets('失败原因逐对象展示，重试不会重复成功对象', (tester) async {
    final fixture = await openForwardSheet(tester, count: 3);
    var failing = true;
    fixture.repository.onForward = (request) async {
      if (failing && request.targetId == 'target-1') {
        throw const ImApiException(
          statusCode: 403,
          code: 'FORBIDDEN',
          message: '群聊已禁言',
        );
      }
      return fixture.repository.responseFor(request);
    };
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    await tester.tap(find.byKey(confirmKey));
    await tester.pumpAndSettle();
    expect(find.text('成功 2 · 失败 1 · 未发送 0'), findsOneWidget);
    expect(find.text('群聊已禁言'), findsOneWidget);
    final failedId = fixture.repository.requests
        .firstWhere((r) => r.targetId == 'target-1')
        .batchId;
    failing = false;
    await tester.tap(find.byKey(const Key('forward-retry')));
    await tester.pumpAndSettle();
    expect(fixture.repository.requests, hasLength(4));
    expect(fixture.repository.requests.last.batchId, failedId);
  });

  testWidgets('页面销毁停止剩余目标，返回的请求不会更新已销毁页面', (tester) async {
    final fixture = await openForwardSheet(tester, count: 8);
    final gate = Completer<void>();
    fixture.repository.onForward = (request) async {
      await gate.future;
      return fixture.repository.responseFor(request);
    };
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    await tester.tap(find.byKey(confirmKey));
    await tester.pump();
    await tester.pumpWidget(const SizedBox.shrink());
    gate.complete();
    await tester.pumpAndSettle();
    expect(fixture.repository.requests, hasLength(3));
    expect(tester.takeException(), isNull);
  });

  testWidgets('登录失效后禁止发送和重试', (tester) async {
    final fixture = await openForwardSheet(tester);
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    await fixture.controller.logout();
    await tester.pumpAndSettle();
    expect(find.textContaining('登录状态已失效'), findsOneWidget);
    expect(
      tester.widget<FilledButton>(find.byKey(confirmKey)).onPressed,
      isNull,
    );
    expect(fixture.repository.requests, isEmpty);
  });

  testWidgets('空列表无可选对象且全选和确认均禁用', (tester) async {
    await openForwardSheet(tester, count: 0);
    expect(find.text('暂无可转发会话'), findsOneWidget);
    expect(tester.widget<TextButton>(find.byKey(allKey)).onPressed, isNull);
    expect(
      tester.widget<FilledButton>(find.byKey(confirmKey)).onPressed,
      isNull,
    );
  });

  testWidgets('超过100条源消息会提示而不打开转发面板', (tester) async {
    final fixture = await openForwardSheet(
      tester,
      messages: List.generate(101, forwardSource),
    );
    expect(find.text('每次最多转发 100 条消息，请减少选择后重试'), findsOneWidget);
    expect(find.text('选择转发对象'), findsNothing);
    expect(fixture.repository.requests, isEmpty);
  });

  testWidgets('小屏大字体和键盘同时出现时仍能搜索、确认和查看结果', (tester) async {
    final fixture = await openForwardSheet(
      tester,
      size: const Size(320, 568),
      textScale: 2,
      count: 2,
    );
    await tester.tap(find.byKey(allKey));
    await tester.pump();
    await tester.enterText(find.byKey(searchKey), '会话');
    tester.view.viewInsets = const FakeViewPadding(bottom: 280);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(
      tester.getRect(find.byKey(confirmKey)).bottom,
      lessThanOrEqualTo(288),
    );
    fixture.repository.onForward = (_) async => throw const ImApiException(
      statusCode: 403,
      code: 'FORBIDDEN',
      message: '当前群聊已禁言',
    );
    await tester.tap(find.byKey(confirmKey));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.byKey(const Key('forward-finish')), findsOneWidget);
    expect(fixture.repository.requests, hasLength(2));
  });

  for (final size in [const Size(320, 568), const Size(1280, 900)]) {
    for (final brightness in Brightness.values) {
      testWidgets('${size.width} $brightness 大字体选择和结果布局不溢出', (tester) async {
        final fixture = await openForwardSheet(
          tester,
          size: size,
          brightness: brightness,
          textScale: 2,
          count: 2,
        );
        fixture.repository.onForward = (_) async => throw const ImApiException(
          statusCode: 403,
          code: 'FORBIDDEN',
          message: '当前会话不可发送，请检查成员身份和禁言设置',
        );
        expect(tester.takeException(), isNull);
        await tester.tap(find.byKey(allKey));
        await tester.pump();
        await tester.tap(find.byKey(confirmKey));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        expect(find.byKey(const Key('forward-finish')), findsOneWidget);
      });
    }
  }
}

Future<({AppController controller, RecordingForwardRepository repository})>
openForwardSheet(
  WidgetTester tester, {
  int count = 5,
  List<Conversation>? targets,
  List<ChatMessage>? messages,
  String mode = 'separate',
  Size size = const Size(375, 812),
  Brightness brightness = Brightness.light,
  double textScale = 1,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.view.resetViewInsets();
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetViewInsets);
  final repository = RecordingForwardRepository();
  final controller = forwardTestController(repository, count: count);
  if (targets != null) controller.conversations = targets;
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
  });
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(brightness),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(textScale)),
        child: child!,
      ),
      home: Scaffold(
        body: Builder(
          builder: (context) => TextButton(
            onPressed: () => showForwardConversations(
              context,
              controller: controller,
              messages: messages ?? [forwardSource(1)],
              mode: mode,
            ),
            child: const Text('打开转发'),
          ),
        ),
      ),
    ),
  );
  await tester.tap(find.text('打开转发'));
  await tester.pumpAndSettle();
  return (controller: controller, repository: repository);
}
