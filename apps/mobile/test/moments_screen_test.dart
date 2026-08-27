import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/ui/screens/moments_screen.dart';

void main() {
  testWidgets('朋友圈错误态说明数据安全并支持按钮和下拉重试', (tester) async {
    var refreshCount = 0;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MomentsErrorState(onRefresh: () async => refreshCount++),
        ),
      ),
    );

    expect(find.text('朋友圈暂时无法加载'), findsOneWidget);
    expect(find.textContaining('已经发布的内容仍保存在服务器'), findsOneWidget);
    expect(find.byType(RefreshIndicator), findsOneWidget);

    await tester.tap(find.text('重新加载'));
    await tester.pump();
    expect(refreshCount, 1);

    await tester.drag(find.byType(CustomScrollView), const Offset(0, 500));
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(refreshCount, 2);
  });

  testWidgets('朋友圈空态提供发布入口并保留下拉刷新', (tester) async {
    var publishCount = 0;
    var refreshCount = 0;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MomentsEmptyState(
            onRefresh: () async => refreshCount++,
            onPublish: () => publishCount++,
          ),
        ),
      ),
    );

    expect(find.text('还没有朋友圈动态'), findsOneWidget);
    expect(find.text('发布第一条动态'), findsOneWidget);
    expect(find.byType(RefreshIndicator), findsOneWidget);
    expect(
      tester.widget<CustomScrollView>(find.byType(CustomScrollView)).physics,
      isA<AlwaysScrollableScrollPhysics>(),
    );

    await tester.tap(find.text('发布第一条动态'));
    expect(publishCount, 1);

    await tester.drag(find.byType(CustomScrollView), const Offset(0, 500));
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(refreshCount, 1);
  });
}
