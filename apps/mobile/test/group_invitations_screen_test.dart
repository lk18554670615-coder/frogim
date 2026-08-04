import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/people_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('群邀请支持查看邀请人并同意加入', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await tester.runAsync(() => controller.login('13800138000', '123456'));

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: GroupInvitationsScreen(controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('周末咖啡局'), findsOneWidget);
    expect(find.text('林屿 邀请你加入'), findsOneWidget);
    await tester.tap(find.text('加入群聊'));
    await tester.pumpAndSettle();

    expect(find.text('已加入'), findsOneWidget);
    expect(find.text('已加入群聊'), findsOneWidget);
  });
}
