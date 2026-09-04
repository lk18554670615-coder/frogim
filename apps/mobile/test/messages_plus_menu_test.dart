import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('消息页快捷菜单使用暖白表面、黄色图标和易读主文字', (tester) async {
    final controller = await _pumpConversations(tester);
    addTearDown(controller.dispose);

    final dynamic menuButton = tester.widget(
      find.byKey(const Key('messages-plus-menu')),
    );
    expect(menuButton.color, LinliColors.surface);
    expect(menuButton.surfaceTintColor, Colors.transparent);

    await _openMenu(tester);

    const actionKeys = <String>[
      'header-action-group',
      'header-action-add-friend',
      'header-action-scan',
      'header-action-my-qr',
    ];
    const labels = <String>['发起群聊', '添加朋友', '扫一扫', '我的二维码'];

    for (final key in actionKeys) {
      final dynamic item = tester.widget(find.byKey(Key(key)));
      expect(item.height, greaterThanOrEqualTo(48));
    }
    for (final label in labels) {
      expect(find.text(label), findsOneWidget);

      final iconContainer = tester.widget<Container>(
        find.byKey(Key('header-menu-icon-$label')),
      );
      final decoration = iconContainer.decoration! as BoxDecoration;
      expect(decoration.color, LinliColors.brandYellowStrong);

      final text = tester.widget<Text>(find.text(label));
      expect(text.style?.color, LinliColors.label);
      expect(text.style?.fontWeight, FontWeight.w600);
    }
    expect(tester.takeException(), isNull);
  });

  testWidgets('消息页快捷菜单四个入口均可进入真实功能页', (tester) async {
    final controller = await _pumpConversations(tester);
    addTearDown(controller.dispose);

    await _openAndExpectScreen(
      tester,
      const Key('header-action-group'),
      find.text('创建群聊'),
    );
    await _openAndExpectScreen(
      tester,
      const Key('header-action-add-friend'),
      find.text('搜索'),
    );
    await _openAndExpectScreen(
      tester,
      const Key('header-action-scan'),
      find.text('扫一扫'),
      settle: false,
    );
    await _openAndExpectScreen(
      tester,
      const Key('header-action-my-qr'),
      find.text('我的二维码'),
    );

    expect(tester.takeException(), isNull);
  });
}

Future<AppController> _pumpConversations(WidgetTester tester) async {
  tester.view.physicalSize = const Size(375, 812);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);

  final controller = AppController(DemoImRepository(latency: Duration.zero));
  await tester.runAsync(controller.loginAsDemo);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: Scaffold(body: ConversationsTab(controller: controller)),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

Future<void> _openMenu(WidgetTester tester) async {
  await tester.tap(find.byKey(const Key('messages-plus-menu')));
  await tester.pumpAndSettle();
}

Future<void> _openAndExpectScreen(
  WidgetTester tester,
  Key actionKey,
  Finder target, {
  bool settle = true,
}) async {
  await _openMenu(tester);
  await tester.tap(find.byKey(actionKey));
  if (settle) {
    await tester.pumpAndSettle();
  } else {
    // The camera keeps a platform-backed preview alive. One frame is enough
    // to prove that routing reached the scanner without waiting on hardware.
    await tester.pump();
  }
  expect(target, findsOneWidget);

  Navigator.of(tester.element(target.first)).pop();
  await tester.pumpAndSettle();
}
