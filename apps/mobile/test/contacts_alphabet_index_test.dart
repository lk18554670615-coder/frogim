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

  testWidgets('联系人按拼音首字母分组并可通过右侧索引快速跳转', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final controller = AppController(
      _AlphabetContactsRepository(latency: Duration.zero),
    );
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ContactsTab(controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('contact-group-A')), findsOneWidget);
    expect(find.byKey(const Key('contact-alphabet-index')), findsOneWidget);

    final scrollable = find.descendant(
      of: find.byType(CustomScrollView),
      matching: find.byType(Scrollable),
    );
    final position = tester.state<ScrollableState>(scrollable).position;
    expect(position.maxScrollExtent, greaterThan(0));
    final zOnRail = find.descendant(
      of: find.byKey(const Key('contact-alphabet-index')),
      matching: find.text('Z'),
    );
    final gesture = await tester.startGesture(tester.getCenter(zOnRail));
    await tester.pump(const Duration(milliseconds: 220));

    expect(find.byKey(const Key('contact-index-bubble')), findsOneWidget);
    expect(
      find.descendant(
        of: find.byKey(const Key('contact-index-bubble')),
        matching: find.text('Z'),
      ),
      findsOneWidget,
    );
    expect(position.pixels, greaterThan(0));
    expect(find.byKey(const Key('contact-group-Z')), findsOneWidget);

    await gesture.up();
    await tester.pump();
    expect(find.byKey(const Key('contact-index-bubble')), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

class _AlphabetContactsRepository extends DemoImRepository {
  _AlphabetContactsRepository({required super.latency});

  @override
  Future<List<AppUser>> contacts() async => [
    for (var code = 'A'.codeUnitAt(0); code <= 'Z'.codeUnitAt(0); code++)
      AppUser(
        id: 'contact-${String.fromCharCode(code).toLowerCase()}',
        name: '${String.fromCharCode(code)} 联系人',
        handle: 'contact_${String.fromCharCode(code).toLowerCase()}',
        presence: '在线',
      ),
  ];
}
