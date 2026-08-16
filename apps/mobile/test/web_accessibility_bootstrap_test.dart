import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/main.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

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
