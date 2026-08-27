import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/im/business_features.dart';
import 'package:linli_im/ui/screens/business_channel_screens.dart';
import 'package:linli_im/ui/screens/sticker_store_screen.dart';

void main() {
  testWidgets('表情商店失败时显示可恢复说明且不泄露内部异常', (tester) async {
    final controller = await _controller(tester);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: StickerStoreScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    expect(find.text('表情商店暂时无法加载'), findsOneWidget);
    expect(find.text('重新加载'), findsOneWidget);
    expect(find.textContaining('internal-sticker-token'), findsNothing);
  });

  testWidgets('社区频道失败时显示数据安全说明且不泄露内部异常', (tester) async {
    final controller = await _controller(tester);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: BusinessChannelHubScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    expect(find.text('社区与频道暂时无法加载'), findsOneWidget);
    expect(find.textContaining('已经加入的频道不会受到影响'), findsOneWidget);
    expect(find.textContaining('internal-channel-token'), findsNothing);
  });

  testWidgets('客服中心失败时显示会话安全说明且不泄露内部异常', (tester) async {
    final controller = await _controller(tester);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(home: SupportCenterScreen(controller: controller)),
    );
    await tester.pumpAndSettle();

    expect(find.text('客服中心暂时无法加载'), findsOneWidget);
    expect(find.textContaining('已有客服会话不会因此结束'), findsOneWidget);
    expect(find.textContaining('internal-support-token'), findsNothing);
  });
}

Future<AppController> _controller(WidgetTester tester) async {
  final controller = AppController(_FailingBusinessRepository());
  await tester.runAsync(controller.loginAsDemo);
  return controller;
}

class _FailingBusinessRepository extends DemoImRepository
    implements BusinessFeatureRepository {
  _FailingBusinessRepository() : super(latency: Duration.zero);

  @override
  Future<List<StickerCategorySummary>> stickerCategories() =>
      Future.error(StateError('internal-sticker-token'));

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
  }) => Future.error(StateError('internal-channel-token'));

  @override
  Future<List<SupportSkillGroupSummary>> supportSkillGroups() =>
      Future.error(StateError('internal-support-token'));

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
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
