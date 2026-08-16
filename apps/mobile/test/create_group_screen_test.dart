import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/people_screens.dart';

void main() {
  testWidgets('创建群聊可按昵称和呱呱号筛选且保留已选联系人', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: CreateGroupScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    await tester.enterText(
      find.byKey(const Key('create-group-search')),
      'linyu',
    );
    await tester.pump();
    expect(find.byKey(const Key('create-group-contact-u1')), findsOneWidget);
    expect(find.byKey(const Key('create-group-contact-u2')), findsNothing);

    await tester.tap(find.byKey(const Key('create-group-contact-u1')));
    await tester.pump();
    expect(find.byKey(const Key('create-group-selected-u1')), findsOneWidget);
    expect(find.text('完成 1'), findsOneWidget);

    await tester.enterText(find.byKey(const Key('create-group-search')), '安然');
    await tester.pump();
    expect(find.byKey(const Key('create-group-contact-u2')), findsOneWidget);
    expect(find.byKey(const Key('create-group-selected-u1')), findsOneWidget);
  });

  testWidgets('创建群聊无匹配结果可恢复且提交失败有明确反馈', (tester) async {
    final controller = AppController(_FailingCreateGroupRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: CreateGroupScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    await tester.enterText(
      find.byKey(const Key('create-group-search')),
      '不存在的联系人',
    );
    await tester.pump();
    expect(find.text('没有匹配的联系人'), findsOneWidget);
    await tester.tap(find.text('清除搜索'));
    await tester.pump();

    await tester.tap(find.byKey(const Key('create-group-contact-u1')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('create-group-submit')));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '创建'));
    await tester.pumpAndSettle();

    expect(find.text('群聊创建失败'), findsOneWidget);
    expect(find.byType(CreateGroupScreen), findsOneWidget);
  });

  testWidgets('联系人没有公开呱呱号时显示完善提示而不是空的 @', (tester) async {
    final controller = AppController(_ContactWithoutHandleRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: CreateGroupScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    expect(find.text('尚未设置呱呱号'), findsOneWidget);
    expect(find.text('@'), findsNothing);
  });
}

class _FailingCreateGroupRepository extends DemoImRepository {
  _FailingCreateGroupRepository() : super(latency: Duration.zero);

  @override
  Future<Conversation> createGroup(String name, List<AppUser> members) =>
      Future.error(StateError('internal-create-group-token'));
}

class _ContactWithoutHandleRepository extends DemoImRepository {
  _ContactWithoutHandleRepository() : super(latency: Duration.zero);

  @override
  Future<List<AppUser>> contacts() async => const [
    AppUser(id: 'user-no-handle', name: '新朋友', handle: '', presence: '在线'),
  ];
}
