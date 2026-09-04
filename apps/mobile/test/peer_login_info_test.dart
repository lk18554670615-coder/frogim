import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/peer_login_info.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/widgets/peer_login_info.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _info = PeerLoginInfo(
  userId: 'peer',
  lastLoginIp: '2001:4860:4860::8888',
  regionLabel: '中国 · 广东省 · 深圳市 · 电信',
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final audioChannels = <EventChannel>[];
  setUp(() => SharedPreferences.setMockInitialValues({}));

  setUpAll(() async {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      (_) async => null,
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      MockStreamHandler.inline(onListen: (_, _) {}),
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      (call) async {
        if (call.method == 'create') {
          final id = (call.arguments as Map)['playerId'];
          final channel = EventChannel('xyz.luan/audioplayers/events/$id');
          audioChannels.add(channel);
          messenger.setMockStreamHandler(
            channel,
            MockStreamHandler.inline(onListen: (_, _) {}),
          );
        }
        return null;
      },
    );
    for (final (family, asset) in [
      ('NotoSansSC', 'assets/fonts/NotoSansSC-Regular.otf'),
      ('Roboto', 'assets/fonts/NotoSansSC-Regular.otf'),
      ('.SF Pro Text', 'assets/fonts/NotoSansSC-Regular.otf'),
      ('.SF Pro Display', 'assets/fonts/NotoSansSC-Regular.otf'),
      ('MaterialIcons', 'fonts/MaterialIcons-Regular.otf'),
      (
        'packages/cupertino_icons/CupertinoIcons',
        'packages/cupertino_icons/assets/CupertinoIcons.ttf',
      ),
    ]) {
      await (FontLoader(family)..addFont(rootBundle.load(asset))).load();
    }
  });

  tearDownAll(() {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      null,
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      null,
    );
    for (final channel in audioChannels) {
      messenger.setMockStreamHandler(channel, null);
    }
  });

  test('完整 IP 和归属地字段映射，缺失或失败不伪造归属地', () {
    final info = PeerLoginInfo.fromJson({
      'userId': 'peer',
      'lastLoginIp': ' 2001:4860:4860::8888 ',
      'region': {
        'status': 'ok',
        'country': '中国',
        'province': '上海',
        'city': '上海',
        'isp': '电信',
      },
    });
    expect(info.userId, 'peer');
    expect(info.lastLoginIp, '2001:4860:4860::8888');
    expect(info.regionLabel, '中国 · 上海 · 电信');
    for (final pair in {
      'private': '内网地址',
      'loopback': '本机回环地址',
      'reserved': '保留地址',
      'unknown': '未记录',
      'unavailable': '暂不可用',
      'not_found': '暂不可用',
    }.entries) {
      expect(
        PeerLoginInfo.fromJson({
          'region': {'status': pair.key},
        }).regionLabel,
        pair.value,
      );
    }
    expect(PeerLoginInfo.fromJson({}).lastLoginIp, isEmpty);
  });

  test('真实仓库调用会话专用认证接口，不把 IP 合并到普通用户缓存', () async {
    FlutterSecureStorage.setMockInitialValues({});
    final calls = <String>[];
    final repository = LiveImRepository(
      apiBaseUrl: 'https://api.example',
      client: MockClient((request) async {
        calls.add(request.url.path);
        if (request.url.path == '/v2/auth/login') {
          return http.Response(
            jsonEncode({
              'accessToken': 'access',
              'refreshToken': 'refresh',
              'user': {'id': 'me', 'name': 'Me'},
            }),
            200,
          );
        }
        expect(request.method, 'GET');
        expect(request.headers['Authorization'], 'Bearer access');
        expect(
          request.url.path,
          '/v2/channels/conversations/c-peer/peer-login-info',
        );
        return http.Response(
          jsonEncode({
            'userId': 'peer',
            'lastLoginIp': '192.168.1.5',
            'region': {'status': 'private'},
          }),
          200,
        );
      }),
    );
    addTearDown(repository.close);
    await repository.login('13800138000', '123456');
    final info = await repository.peerLoginInfo('c-peer');
    expect(info.lastLoginIp, '192.168.1.5');
    expect(info.regionLabel, '内网地址');
    expect(repository.currentUser!.id, 'me');
    expect(calls.length, 2);
  });

  testWidgets('进入立即查询，30 秒刷新；失败清除旧值，可手动重试，后台停止', (tester) async {
    final repository = _PeerRepository();
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pumpLabel(tester, controller);
    expect(repository.requests, ['direct']);
    expect(find.text('最近登录 IP：${_info.lastLoginIp}'), findsOneWidget);
    expect(find.text('归属地：${_info.regionLabel}'), findsOneWidget);
    await tester.pump(const Duration(seconds: 29));
    expect(repository.requests.length, 1);
    repository.fail = true;
    await tester.pump(const Duration(seconds: 1));
    await tester.pump();
    expect(find.text('最近登录 IP：暂不可用'), findsOneWidget);
    expect(find.textContaining(_info.lastLoginIp), findsNothing);
    repository.fail = false;
    await tester.tap(find.byKey(const Key('peer-login-ip-retry')));
    await tester.pump();
    expect(find.text('最近登录 IP：${_info.lastLoginIp}'), findsOneWidget);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump();
    final count = repository.requests.length;
    await tester.pump(const Duration(seconds: 60));
    expect(repository.requests.length, count);
    expect(find.byKey(const Key('peer-login-ip')), findsNothing);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    expect(repository.requests.length, count + 1);
    await tester.pumpWidget(const SizedBox());
    await tester.pump(const Duration(seconds: 60));
    expect(repository.requests.length, count + 1);
  });

  testWidgets('切换会话忽略旧响应；退出和切换账号立即清掉 IP', (tester) async {
    final delayed = Completer<PeerLoginInfo>();
    final repository = _PeerRepository()..pending = delayed;
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pumpLabel(tester, controller);
    expect(find.text('最近登录 IP：正在查询…'), findsOneWidget);
    await _pumpLabel(tester, controller);
    expect(repository.requests.length, 1);
    repository.pending = null;
    await _pumpLabel(tester, controller, conversationId: 'another');
    delayed.complete(
      const PeerLoginInfo(userId: 'wrong', lastLoginIp: '1.1.1.1'),
    );
    await tester.pump();
    expect(repository.requests, ['direct', 'another']);
    expect(find.text('最近登录 IP：${_info.lastLoginIp}'), findsOneWidget);
    expect(find.textContaining('1.1.1.1'), findsNothing);
    controller.authenticated = false;
    controller.refreshPushConfiguration();
    await tester.pump();
    expect(find.byKey(const Key('peer-login-ip')), findsNothing);
    repository.value = const PeerLoginInfo(userId: 'new-peer');
    controller.currentUser = DemoImRepository.people.first;
    controller.authenticated = true;
    controller.refreshPushConfiguration();
    await tester.pump();
    expect(find.text('最近登录 IP：未记录'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('非当前路由不查询，返回后重新取值', (tester) async {
    final repository = _PeerRepository();
    final controller = _controller(repository);
    addTearDown(controller.dispose);
    await _pumpLabel(tester, controller);
    final navigator = tester.state<NavigatorState>(find.byType(Navigator));
    unawaited(
      navigator.push(
        MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('其他页面')),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final count = repository.requests.length;
    await tester.pump(const Duration(seconds: 60));
    expect(repository.requests.length, count);
    navigator.pop();
    await tester.pumpAndSettle();
    expect(repository.requests.length, count + 1);
    await tester.pumpWidget(const SizedBox());
  });

  for (final platform in [
    TargetPlatform.android,
    TargetPlatform.iOS,
    TargetPlatform.windows,
    TargetPlatform.macOS,
    TargetPlatform.linux,
  ]) {
    testWidgets('$platform 仅 PC 普通单聊显示，不扩大到群或业务频道', (tester) async {
      final expected = [
        TargetPlatform.windows,
        TargetPlatform.macOS,
        TargetPlatform.linux,
      ].contains(platform);
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(Brightness.light).copyWith(platform: platform),
          home: Builder(
            builder: (context) {
              expect(showPeerLoginInfoFor(context, _conversation()), expected);
              expect(
                showPeerLoginInfoFor(context, _conversation(group: true)),
                isFalse,
              );
              expect(
                showPeerLoginInfoFor(context, _conversation(channelType: 10)),
                isFalse,
              );
              return const SizedBox();
            },
          ),
        ),
      );
    });
  }

  for (final (width, scale, brightness) in [
    (1280.0, 1.0, Brightness.light),
    (360.0, 2.0, Brightness.dark),
  ]) {
    testWidgets('PC 聊天顶部 $width/$scale/$brightness 完整 IPv6 无溢出', (
      tester,
    ) async {
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      const record = MethodChannel('com.llfbandit.record/messages');
      messenger.setMockMethodCallHandler(record, (_) async => null);
      addTearDown(() => messenger.setMockMethodCallHandler(record, null));
      final repository = _PeerRepository();
      final controller = _controller(repository);
      addTearDown(controller.dispose);
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(
            brightness,
          ).copyWith(platform: TargetPlatform.macOS),
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(scale)),
            child: child!,
          ),
          home: Align(
            child: SizedBox(
              width: width,
              child: ChatScreen(
                controller: controller,
                conversation: _conversation(),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byType(PeerLoginInfoLabel), findsOneWidget);
      expect(find.text('最近登录 IP：${_info.lastLoginIp}'), findsOneWidget);
      expect(find.byType(SelectableText), findsOneWidget);
      expect(tester.takeException(), isNull);
      if (!kIsWeb && Platform.isWindows) {
        await expectLater(
          find.byType(AppBar),
          matchesGoldenFile(
            'goldens/windows/peer-login-header-${width.toInt()}.png',
          ),
        );
      }
      await tester.pumpWidget(const SizedBox());
    });
  }
}

Future<void> _pumpLabel(
  WidgetTester tester,
  AppController controller, {
  String conversationId = 'direct',
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: PeerLoginInfoLabel(
          controller: controller,
          conversationId: conversationId,
        ),
      ),
    ),
  );
  await tester.pump();
}

AppController _controller(_PeerRepository repository) =>
    AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;

Conversation _conversation({bool group = false, int channelType = 1}) =>
    Conversation(
      id: 'direct',
      title: '测试联系人较长昵称',
      subtitle: '',
      updatedAt: DateTime(2026, 9, 3),
      kind: group ? ConversationKind.group : ConversationKind.direct,
      channelType: channelType,
      members: [DemoImRepository.people.first],
    );

class _PeerRepository extends DemoImRepository {
  _PeerRepository() : super(latency: Duration.zero);
  final requests = <String>[];
  PeerLoginInfo value = _info;
  Completer<PeerLoginInfo>? pending;
  bool fail = false;
  @override
  Future<PeerLoginInfo> peerLoginInfo(String conversationId) async {
    requests.add(conversationId);
    if (fail) throw StateError('unavailable');
    return pending?.future ?? value;
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [];
}
