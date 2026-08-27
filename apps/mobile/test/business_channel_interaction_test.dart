import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/im/business_features.dart';
import 'package:linli_im/ui/screens/business_channel_screens.dart';

void main() {
  testWidgets('创建频道缺少名称时显示就地规则而不是无响应', (tester) async {
    final repository = _BusinessInteractionRepository();
    final controller = await _controller(tester, repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: BusinessChannelHubScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('创建频道'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '创建'));
    await tester.pump();

    expect(find.text('请输入名称'), findsOneWidget);
    expect(repository.createCalls, 0);
  });

  testWidgets('创建话题通过社区名称选择归属且提交真实社区 ID', (tester) async {
    final repository = _BusinessInteractionRepository();
    final controller = await _controller(tester, repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: BusinessChannelHubScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('话题'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('创建频道'));
    await tester.pumpAndSettle();

    expect(find.text('所属社区'), findsOneWidget);
    expect(find.text('品牌社区'), findsOneWidget);
    expect(find.text('所属社区 ID'), findsNothing);
    await tester.enterText(
      find.byKey(const Key('business-channel-name')),
      '产品交流',
    );
    await tester.tap(find.widgetWithText(FilledButton, '创建'));
    await tester.pumpAndSettle();

    expect(repository.createCalls, 1);
    expect(repository.lastParentId, 'community-brand');
  });

  testWidgets('频道角色使用中文且退出操作必须确认', (tester) async {
    final repository = _BusinessInteractionRepository();
    final controller = await _controller(tester, repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        home: BusinessChannelDetailScreen(
          controller: controller,
          channel: repository.memberChannel,
          onOpenChat: (_) async {},
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('2 位成员 · 成员'), findsOneWidget);
    expect(find.text('2 位成员 · member'), findsNothing);
    await tester.tap(find.text('退出频道'));
    await tester.pumpAndSettle();
    expect(find.text('退出频道？'), findsOneWidget);
    expect(repository.unsubscribeCalls, 0);

    await tester.tap(find.widgetWithText(TextButton, '取消'));
    await tester.pumpAndSettle();
    expect(repository.unsubscribeCalls, 0);

    await tester.tap(find.text('退出频道'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, '确认退出'));
    await tester.pumpAndSettle();
    expect(repository.unsubscribeCalls, 1);
  });
}

Future<AppController> _controller(
  WidgetTester tester,
  _BusinessInteractionRepository repository,
) async {
  final controller = AppController(repository);
  await tester.runAsync(controller.loginAsDemo);
  return controller;
}

class _BusinessInteractionRepository extends DemoImRepository
    implements BusinessFeatureRepository {
  _BusinessInteractionRepository() : super(latency: Duration.zero);

  int createCalls = 0;
  int unsubscribeCalls = 0;
  String lastParentId = '';

  final community = const BusinessChannelSummary(
    id: 'community-brand',
    channelType: 4,
    category: 'community',
    name: '品牌社区',
    description: '青蛙呱呱官方社区',
    ownerId: 'owner',
    visibility: 'public',
    joinPolicy: 'open',
    postingPolicy: 'members',
    memberCount: 3,
    subscribed: true,
    role: 'member',
  );

  final memberChannel = const BusinessChannelSummary(
    id: 'community-member',
    channelType: 4,
    category: 'community',
    name: '用户社区',
    description: '真实用户社区',
    ownerId: 'owner',
    visibility: 'public',
    joinPolicy: 'open',
    postingPolicy: 'members',
    memberCount: 2,
    subscribed: true,
    role: 'member',
  );

  @override
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  }) async {
    if (channelType == 4) return [community];
    return const [];
  }

  @override
  Future<BusinessChannelSummary> createBusinessChannel({
    required int channelType,
    required String name,
    String parentId = '',
    String description = '',
    String visibility = 'public',
    String joinPolicy = 'open',
    String postingPolicy = 'members',
    int slowModeSeconds = 0,
  }) async {
    createCalls++;
    lastParentId = parentId;
    return BusinessChannelSummary(
      id: 'created-topic',
      channelType: channelType,
      category: 'topic',
      name: name,
      description: description,
      ownerId: DemoImRepository.demoUser.id,
      visibility: visibility,
      joinPolicy: joinPolicy,
      postingPolicy: postingPolicy,
      memberCount: 1,
      subscribed: true,
      role: 'owner',
      parentId: parentId,
    );
  }

  @override
  Future<BusinessChannelSummary> businessChannel(
    String channelId,
    int channelType,
  ) async => memberChannel;

  @override
  Future<List<BusinessChannelMemberSummary>> businessChannelMembers(
    String channelId,
    int channelType,
  ) async => const [];

  @override
  Future<List<BusinessChannelAccessSummary>> businessChannelAccess(
    String channelId,
    int channelType, {
    String accessType = '',
  }) async => const [];

  @override
  Future<void> unsubscribeBusinessChannel(
    String channelId,
    int channelType,
  ) async {
    unsubscribeCalls++;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
