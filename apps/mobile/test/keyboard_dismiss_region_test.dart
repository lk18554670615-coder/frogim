import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/ui/widgets/keyboard_dismiss_region.dart';

void main() {
  testWidgets('tapping page chrome dismisses the focused text field', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: KeyboardDismissRegion(
          child: Scaffold(
            appBar: AppBar(title: const Text('编辑资料')),
            body: const TextField(key: Key('input')),
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('input')));
    await tester.pump();
    final input = tester.widget<EditableText>(find.byType(EditableText));
    expect(input.focusNode.hasFocus, isTrue);

    await tester.tap(find.text('编辑资料'));
    await tester.pump();
    expect(input.focusNode.hasFocus, isFalse);
  });

  testWidgets('tapping another text field transfers focus normally', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: KeyboardDismissRegion(
          child: Scaffold(
            body: Column(
              children: [
                TextField(key: Key('first')),
                TextField(key: Key('second')),
              ],
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('first')));
    await tester.pump();
    final first = tester.widget<EditableText>(
      find.descendant(
        of: find.byKey(const Key('first')),
        matching: find.byType(EditableText),
      ),
    );
    expect(first.focusNode.hasFocus, isTrue);

    await tester.tap(find.byKey(const Key('second')));
    await tester.pump();
    final second = tester.widget<EditableText>(
      find.descendant(
        of: find.byKey(const Key('second')),
        matching: find.byType(EditableText),
      ),
    );
    expect(second.focusNode.hasFocus, isTrue);
  });
}
