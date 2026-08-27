import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/ui/legal_documents.dart';

void main() {
  testWidgets('未配置法律地址时明确阻止用户把占位说明当作正式文件', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => Scaffold(
            body: TextButton(
              onPressed: () =>
                  showLegalDocument(context, LegalDocument.privacy),
              child: const Text('打开隐私政策'),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('打开隐私政策'));
    await tester.pumpAndSettle();

    expect(find.text('隐私政策未配置'), findsOneWidget);
    expect(find.textContaining('审核后的文本'), findsOneWidget);
    expect(find.textContaining('占位文本'), findsNothing);
  });
}
