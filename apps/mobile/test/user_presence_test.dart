import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/user_presence.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/widgets/user_presence.dart';

List<UserPresenceSnapshot> online(List<String> ids) => [
  for (final id in ids) UserPresenceSnapshot(id, UserPresenceStatus.online),
];

void main() {
  test('状态解析不借用旧用户 isOnline 默认值', () {
    for (final status in UserPresenceStatus.values) {
      expect(
        UserPresenceSnapshot.fromJson({
          'userId': 'u',
          'status': status.name,
        }).status,
        status,
      );
    }
    expect(
      UserPresenceSnapshot.fromJson({'userId': 'u'}).status,
      UserPresenceStatus.unknown,
    );
  });

  for (final platform in ['android', 'ios', 'web', 'macos']) {
    test('$platform 使用相同批量接口和群上下文，不读取 SDK 缺省状态', () async {
      final repo = LiveImRepository(
        clientPlatform: platform,
        apiBaseUrl: 'https://test.invalid',
        client: MockClient((r) async {
          expect(r.method, 'POST');
          expect(r.url.path, '/v2/users/presence');
          expect(jsonDecode(r.body), {
            'userIds': ['u', 'v'],
            'groupId': 'business-group',
          });
          return http.Response(
            jsonEncode({
              'items': [
                {
                  'userId': 'u',
                  'status': 'online',
                  'checkedAt': '2026-09-03T01:00:00Z',
                },
                {
                  'userId': 'v',
                  'status': 'hidden',
                  'checkedAt': '2026-09-03T01:00:00Z',
                },
              ],
            }),
            200,
          );
        }),
      );
      final result = await repo.userPresence([
        'u',
        'v',
      ], groupId: 'business-group');
      expect(result.map((r) => r.status), [
        UserPresenceStatus.online,
        UserPresenceStatus.hidden,
      ]);
      expect(result.first.checkedAt!.isUtc, isTrue);
      await repo.close();
    });
  }

  testWidgets('立即查询、10 秒刷新、共享去重、失败替换绿色状态', (tester) async {
    var calls = 0;
    final c = PresenceCoordinator((ids, {groupId}) async {
      calls++;
      if (calls == 3) throw Exception('offline');
      return online(ids);
    })..setAccount('a');
    final stop = c.watch('u');
    final stop2 = c.watch('u');
    await tester.pump();
    expect(calls, 1);
    expect(c.status('u'), UserPresenceStatus.online);
    await tester.pump(const Duration(seconds: 9));
    expect(calls, 1);
    await tester.pump(const Duration(seconds: 1));
    expect(calls, 2);
    await tester.pump(const Duration(seconds: 10));
    expect(c.status('u'), UserPresenceStatus.unknown);
    await tester.pump(const Duration(seconds: 10));
    expect(c.status('u'), UserPresenceStatus.online);
    stop();
    stop2();
    await tester.pump(const Duration(seconds: 30));
    expect(calls, 4);
    c.dispose();
  });

  testWidgets('超过 200 人分批、最多两个并发、在途不重复', (tester) async {
    final pending = <Completer<List<UserPresenceSnapshot>>>[];
    final batches = <List<String>>[];
    final c = PresenceCoordinator((ids, {groupId}) {
      batches.add(ids);
      final task = Completer<List<UserPresenceSnapshot>>();
      pending.add(task);
      return task.future;
    })..setAccount('a');
    for (var i = 0; i < 601; i++) {
      c.watch('u$i');
    }
    await tester.pump();
    expect(batches.map((b) => b.length), [200, 200]);
    await tester.pump(const Duration(seconds: 10));
    expect(batches.length, 2);
    pending[0].complete(online(batches[0]));
    await tester.pump();
    expect(batches.length, 3);
    pending[1].complete(online(batches[1]));
    await tester.pump();
    expect(batches.length, 4);
    expect(batches.map((b) => b.length), [200, 200, 200, 1]);
    pending[2].complete(online(batches[2]));
    pending[3].complete(online(batches[3]));
    await tester.pump();
    c.dispose();
  });

  testWidgets('账号、群上下文隔离与旧请求失效', (tester) async {
    final pending = <Completer<List<UserPresenceSnapshot>>>[];
    final c = PresenceCoordinator((ids, {groupId}) {
      final task = Completer<List<UserPresenceSnapshot>>();
      pending.add(task);
      return task.future;
    })..setAccount('a');
    c.watch('u', groupId: 'group-a');
    c.watch('u');
    await tester.pump();
    expect(pending.length, 2);
    pending[0].complete(online(['u']));
    pending[1].complete([
      const UserPresenceSnapshot('u', UserPresenceStatus.hidden),
    ]);
    await tester.pump();
    expect(c.status('u', groupId: 'group-a'), UserPresenceStatus.online);
    expect(c.status('u'), UserPresenceStatus.hidden);
    c.invalidate();
    expect(c.status('u', groupId: 'group-a'), UserPresenceStatus.hidden);
    await tester.pump();
    c.setAccount('b');
    pending[2].complete(online(['u']));
    pending[3].complete(online(['u']));
    await tester.pump();
    expect(c.status('u'), UserPresenceStatus.hidden);
    expect(pending.length, 6);
    c.setAccount(null);
    pending[4].complete(online(['u']));
    pending[5].complete(online(['u']));
    await tester.pump(const Duration(seconds: 30));
    expect(pending.length, 6);
    expect(c.status('u'), UserPresenceStatus.hidden);
    c.dispose();
  });

  testWidgets('后台停止、恢复立即查、页面关闭拒绝迟到结果', (tester) async {
    final pending = <Completer<List<UserPresenceSnapshot>>>[];
    final c = PresenceCoordinator((ids, {groupId}) {
      final task = Completer<List<UserPresenceSnapshot>>();
      pending.add(task);
      return task.future;
    })..setAccount('a');
    var stop = c.watch('u');
    await tester.pump();
    c.setForeground(false);
    pending[0].complete(online(['u']));
    await tester.pump(const Duration(seconds: 30));
    expect(pending.length, 1);
    expect(c.status('u'), UserPresenceStatus.hidden);
    c.setForeground(true);
    await tester.pump();
    expect(pending.length, 2);
    stop();
    stop = c.watch('u');
    pending[1].complete(online(['u']));
    await tester.pump();
    expect(c.status('u'), UserPresenceStatus.hidden);
    pending[2].complete(online(['u']));
    await tester.pump();
    expect(c.status('u'), UserPresenceStatus.online);
    stop();
    c.dispose();
  });

  for (final brightness in Brightness.values) {
    for (final width in [390.0, 1280.0]) {
      testWidgets('$width $brightness 状态标签与大字体无溢出', (tester) async {
        tester.view.physicalSize = Size(width, 850);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(brightness),
            home: Scaffold(
              body: MediaQuery(
                data: MediaQueryData(textScaler: TextScaler.linear(2)),
                child: Column(
                  children: [
                    for (final s in UserPresenceStatus.values) PresenceLabel(s),
                  ],
                ),
              ),
            ),
          ),
        );
        expect(find.text('● 在线'), findsOneWidget);
        expect(find.text('● 离线'), findsOneWidget);
        expect(find.text('状态未知'), findsOneWidget);
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('可见路由和活动面板查询，返回页面立即刷新', (tester) async {
    final repo = _PresenceRepo();
    final controller = AppController(repo);
    controller.presence.setAccount('a');
    final navigator = GlobalKey<NavigatorState>();
    Widget page(bool enabled) => MaterialApp(
      navigatorKey: navigator,
      home: Scaffold(
        body: TickerMode(
          enabled: enabled,
          child: UserPresence(
            controller: controller,
            userId: 'u',
            builder: (context, s) => PresenceLabel(s),
          ),
        ),
      ),
    );
    await tester.pumpWidget(page(false));
    await tester.pump();
    expect(repo.calls, 0);
    await tester.pumpWidget(page(true));
    await tester.pump();
    expect(repo.calls, 1);
    navigator.currentState!.push(
      MaterialPageRoute<void>(
        builder: (_) => const Scaffold(body: Text('covered')),
      ),
    );
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 30));
    expect(repo.calls, 1);
    navigator.currentState!.pop();
    await tester.pumpAndSettle();
    expect(repo.calls, 2);
    await tester.pumpWidget(const SizedBox());
    controller.dispose();
    await repo.close();
  });
}

class _PresenceRepo extends DemoImRepository {
  int calls = 0;
  @override
  Future<List<UserPresenceSnapshot>> userPresence(
    List<String> ids, {
    String? groupId,
  }) async {
    calls++;
    return online(ids);
  }
}
