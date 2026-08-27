import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('标准群成员禁言使用服务端正式接口并以 null 解除', () async {
    final requests = <http.Request>[];
    final repository = LiveImRepository(
      client: MockClient((request) async {
        requests.add(request);
        if (request.url.path == '/v2/auth/login') {
          return _json({
            'data': {
              'accessToken': 'access-token',
              'refreshToken': 'refresh-token',
              'user': {
                'id': 'user-1',
                'name': '群主',
                'handle': 'owner',
                'phone': '13800138000',
              },
              'imSession': {
                'uid': 'user-1',
                'token': 'wk1_test',
                'deviceFlag': 2,
                'deviceLevel': 1,
                'tcpUrl': 'tcp://im.example.com:5100',
                'wsUrl': 'wss://im.example.com/ws',
                'sdk': 'wukongimfluttersdk',
                'issuedAt': '2026-08-13T00:00:00Z',
              },
            },
          });
        }
        if (request.url.path ==
            '/v2/channels/groups/group-1/members/member-1/mute') {
          return _json({'data': jsonDecode(request.body)});
        }
        return http.Response('{}', 404);
      }),
      apiBaseUrl: 'https://api.example.com',
      wukongGateway: FakeWukongGateway(),
    );
    await repository.login('13800138000', '123456');
    final until = DateTime.utc(2026, 8, 13, 9, 30);

    await repository.setGroupMemberMuted('group-1', 'member-1', until);
    await repository.setGroupMemberMuted('group-1', 'member-1', null);

    final muteRequests = requests
        .where((request) => request.url.path.endsWith('/member-1/mute'))
        .toList();
    expect(muteRequests, hasLength(2));
    expect(muteRequests.first.method, 'PUT');
    expect(jsonDecode(muteRequests.first.body), {
      'until': '2026-08-13T09:30:00.000Z',
    });
    expect(jsonDecode(muteRequests.last.body), {'until': null});
    await repository.close();
  });

  test('Demo 角色调整不会丢失既有成员禁言状态', () async {
    final repository = DemoImRepository(latency: Duration.zero);
    final members = await repository.groupMembers('c-team');
    final target = members.firstWhere(
      (member) => member.user.id != DemoImRepository.demoUser.id,
    );
    final until = DateTime.now().add(const Duration(hours: 1));

    await repository.setGroupMemberMuted('c-team', target.user.id, until);
    await repository.setGroupRole('c-team', target.user.id, 'admin');
    final muted = (await repository.groupMembers(
      'c-team',
    )).firstWhere((member) => member.user.id == target.user.id);
    expect(muted.role, 'admin');
    expect(muted.isMuted, isTrue);
    expect(muted.mutedUntil, until);

    await repository.setGroupMemberMuted('c-team', target.user.id, null);
    final unmuted = (await repository.groupMembers(
      'c-team',
    )).firstWhere((member) => member.user.id == target.user.id);
    expect(unmuted.isMuted, isFalse);
    expect(unmuted.mutedUntil, isNull);
    await repository.close();
  });
}

http.Response _json(Map<String, Object?> body, [int status = 200]) =>
    http.Response(
      jsonEncode(body),
      status,
      headers: {'content-type': 'application/json'},
    );
