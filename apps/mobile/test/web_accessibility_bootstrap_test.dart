import 'package:flutter/foundation.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/web_context_menu.dart';
import 'package:linli_im/main.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('非 Web 平台不调用浏览器右键菜单通道', () async {
    final calls = <MethodCall>[];
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.contextMenu, (
      call,
    ) async {
      calls.add(call);
      return null;
    });
    addTearDown(
      () =>
          messenger.setMockMethodCallHandler(SystemChannels.contextMenu, null),
    );

    await configureWebContextMenu();

    expect(calls, isEmpty);
  }, skip: kIsWeb);

  test('Web 从首帧持有语义树且重复初始化不会叠加句柄', () {
    disposePersistentWebSemanticsForTest();
    final before = SemanticsBinding.instance.debugOutstandingSemanticsHandles;

    enablePersistentWebSemantics(isWebOverride: true);
    enablePersistentWebSemantics(isWebOverride: true);

    expect(
      SemanticsBinding.instance.debugOutstandingSemanticsHandles,
      before + 1,
    );
    expect(SemanticsBinding.instance.semanticsEnabled, isTrue);

    disposePersistentWebSemanticsForTest();
    expect(SemanticsBinding.instance.debugOutstandingSemanticsHandles, before);
  });
}
