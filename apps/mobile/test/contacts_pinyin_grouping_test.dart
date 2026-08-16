import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('中文联系人按拼音首字母分组且非字母组放在末尾', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final controller = AppController(_PinyinContactsRepository());
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ContactsTab(controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    final rail = find.byKey(const Key('contact-alphabet-index'));
    expect(rail, findsOneWidget);
    final letters = <String>['A', 'C', 'L', 'Z', '#'];
    final centers = <Offset>[];
    for (final letter in letters) {
      final letterFinder = find.descendant(
        of: rail,
        matching: find.text(letter),
      );
      expect(letterFinder, findsOneWidget);
      centers.add(tester.getCenter(letterFinder));
      final semanticTarget = find.bySemanticsLabel('跳转到 $letter');
      expect(semanticTarget, findsOneWidget);
      expect(tester.getSize(semanticTarget).height, greaterThanOrEqualTo(44));
    }
    for (var index = 1; index < centers.length; index++) {
      expect(centers[index].dy, greaterThan(centers[index - 1].dy));
    }

    final scrollable = find.descendant(
      of: find.byType(CustomScrollView),
      matching: find.byType(Scrollable),
    );
    final position = tester.state<ScrollableState>(scrollable).position;
    final initialOffset = position.pixels;
    final zLetter = find.descendant(of: rail, matching: find.text('Z'));
    final gesture = await tester.startGesture(tester.getCenter(zLetter));
    await tester.pump(const Duration(milliseconds: 220));

    expect(position.pixels, greaterThan(initialOffset));
    expect(find.byKey(const Key('contact-index-bubble')), findsOneWidget);
    expect(
      find.descendant(
        of: find.byKey(const Key('contact-index-bubble')),
        matching: find.text('Z'),
      ),
      findsOneWidget,
    );
    expect(find.byKey(const Key('contact-group-Z')), findsOneWidget);
    expect(find.text('周末'), findsOneWidget);

    await gesture.up();
    await tester.pump();
    expect(find.byKey(const Key('contact-index-bubble')), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

class _PinyinContactsRepository extends DemoImRepository {
  _PinyinContactsRepository() : super(latency: Duration.zero);

  @override
  Future<List<AppUser>> contacts() async => const [
    AppUser(id: 'anran', name: '安然', handle: 'anran', presence: '在线'),
    AppUser(id: 'chenche', name: '陈澈', handle: 'chenche', presence: '在线'),
    AppUser(id: 'linyu', name: '林屿', handle: 'linyu', presence: '在线'),
    AppUser(id: 'zhoumo', name: '周末', handle: 'zhoumo', presence: '在线'),
    AppUser(id: 'service', name: '1 号客服', handle: 'service_1', presence: '在线'),
  ];
}
