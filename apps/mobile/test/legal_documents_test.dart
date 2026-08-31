import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/ui/legal_documents.dart';

void main() {
  testWidgets('正式法律文件在应用内页面加载并保留基础排版', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: LegalDocumentScreen(
          document: LegalDocument.terms,
          uri: Uri.parse('https://example.test/legal/terms'),
          loader: (_) async => '''
            <html><body>
              <h1>青蛙呱呱用户协议</h1>
              <p>更新日期：2026 年 8 月 28 日</p>
              <h2>一、协议范围</h2>
              <ul><li>请妥善保管账号。</li></ul>
            </body></html>
          ''',
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('legal-document-content')), findsOneWidget);
    expect(find.text('青蛙呱呱用户协议'), findsOneWidget);
    expect(find.text('一、协议范围'), findsOneWidget);
    expect(find.text('请妥善保管账号。'), findsOneWidget);
  });

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
