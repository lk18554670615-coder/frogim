import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:linli_im/ui/widgets/linli_widgets.dart';

double _contrast(Color foreground, Color background) {
  final a = foreground.computeLuminance();
  final b = background.computeLuminance();
  return ((a > b ? a : b) + .05) / ((a > b ? b : a) + .05);
}

class _InviteStatusRepository extends DemoImRepository {
  _InviteStatusRepository(this.status) : super(latency: Duration.zero);

  final String status;

  @override
  Future<InviteCodeProfile> inviteCode() async => InviteCodeProfile(
    id: 'contrast-test-invite',
    code: 'TESTCODE',
    status: status,
    selfChangesUsed: 0,
    selfChangesRemaining: 1,
    qrPayload: 'qingwaguagua://register?invite=TESTCODE',
    createdAt: DateTime.utc(2026, 9, 8),
  );
}

void main() {
  for (final brightness in Brightness.values) {
    final palette = LinliPalette(brightness == Brightness.dark);
    final theme = buildLinliTheme(brightness);

    for (final status in ['active', 'disabled']) {
      testWidgets(
        '$brightness invite status $status uses semantic text color',
        (tester) async {
          tester.view.physicalSize = const Size(390, 844);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.reset);
          final controller = AppController(_InviteStatusRepository(status));
          addTearDown(controller.dispose);

          await tester.pumpWidget(
            MaterialApp(
              theme: theme,
              home: MyInviteCodeScreen(controller: controller),
            ),
          );
          await tester.pumpAndSettle();

          final active = status == 'active';
          final text = tester.widget<Text>(
            find.text(active ? '邀请码有效' : '邀请码已被停用'),
          );
          expect(
            text.style!.color,
            active ? palette.successText : theme.colorScheme.error,
          );
          if (active) {
            expect(
              _contrast(text.style!.color!, theme.scaffoldBackgroundColor),
              greaterThanOrEqualTo(4.5),
            );
          }
          expect(tester.takeException(), isNull);
        },
      );
    }

    testWidgets(
      '$brightness connection warning and retry text remain readable',
      (tester) async {
        var retries = 0;
        for (final retrying in [false, true]) {
          await tester.pumpWidget(
            MaterialApp(
              theme: theme,
              home: Scaffold(
                body: Align(
                  alignment: Alignment.topCenter,
                  child: SizedBox(
                    width: 390,
                    child: MessagingConnectionBanner(
                      retrying: retrying,
                      onRetry: () => retries++,
                    ),
                  ),
                ),
              ),
            ),
          );
          await tester.pump();

          final banner = tester.widget<Container>(
            find.byKey(const Key('messaging-connection-banner')),
          );
          final message = tester.widget<Text>(
            find.text(retrying ? '正在重新连接消息服务…' : '消息服务未连接，发送暂不可用'),
          );
          expect(
            _contrast(message.style!.color!, banner.color!),
            greaterThanOrEqualTo(4.5),
          );
          final retry = find.byKey(const Key('retry-messaging-connection'));
          if (retrying) {
            expect(retry, findsNothing);
            expect(find.byType(CupertinoActivityIndicator), findsOneWidget);
          } else {
            final label = tester.widget<Text>(
              find.descendant(of: retry, matching: find.text('重试')),
            );
            expect(
              _contrast(label.style!.color!, banner.color!),
              greaterThanOrEqualTo(4.5),
            );
            await tester.tap(retry);
            await tester.pump();
            expect(retries, 1);
          }
          expect(tester.takeException(), isNull);
        }
      },
    );
  }
}
