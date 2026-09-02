import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'support/fake_wukong_gateway.dart';

http.Response response(Map<String, Object?> data) => http.Response(
  jsonEncode({'data': data}),
  200,
  headers: {'content-type': 'application/json'},
);
Map<String, Object?> wire(int seq) => {
  'message_idstr': '$seq',
  'message_seq': seq,
  'client_msg_no': 'c$seq',
  'from_uid': 'peer',
  'channel_id': 'g',
  'channel_type': 2,
  'timestamp': 1788307200 + seq,
  'payload': seq >= 3
      ? {
          'type': seq >= 5 ? 1008 : 1002,
          'schemaVersion': 1,
          'event': seq >= 5 ? 'screenshot.taken' : 'group.members.added',
          'digest': 'private notice',
        }
      : {
          'type': 1,
          'content': seq == 1 ? 'visible history' : 'recalled original',
          if (seq == 2) 'recalledAt': '2026-09-02T09:00:00Z',
        },
};
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });
  for (final platform in ['android', 'ios', 'web', 'macos']) {
    test(
      '$platform recent summary skips hidden pages, role change preserves cache, failures fail closed',
      () async {
        String? role = 'member';
        var failHistory = false;
        final cursors = <int>[];
        final access = {'version': 1, 'visibleAll': true, 'afterSeq': 0};
        final client = MockClient((request) async {
          switch (request.url.path) {
            case '/v2/auth/login':
              return response({
                'accessToken': 'test',
                'refreshToken': 'test-refresh',
                'user': {'id': 'me', 'name': 'Me', 'phone': '13800138000'},
                'imSession': {
                  'uid': 'me',
                  'token': 'wk1_test',
                  'deviceFlag': 2,
                  'deviceLevel': 1,
                  'tcpUrl': 'tcp://im.example.com:5100',
                  'wsUrl': 'wss://im.example.com/ws',
                  'sdk': 'wukongimfluttersdk',
                  'issuedAt': '2026-09-02T00:00:00Z',
                },
              });
            case '/v2/channels/conversations':
              return response({
                'items': [
                  {
                    'conversation': {
                      'id': 'g',
                      'type': 'group',
                      'title': 'Test',
                      'updatedAt': '2026-09-02T00:00:00Z',
                    },
                    'membership': {'role': role},
                    'historyAccess': access,
                    'members': [
                      {'id': 'me', 'name': 'Me'},
                      {'id': 'peer', 'name': 'Peer'},
                    ],
                  },
                ],
              });
            case '/v2/channels/groups/g':
              return response({
                'conversationId': 'g',
                'ownerId': 'peer',
                'name': 'Test',
                'updatedAt': '2026-09-02T00:00:00Z',
                'historyAccess': access,
              });
            case '/v2/im/datasource/conversations':
              return response({
                'items': [
                  {
                    'channel_id': 'g',
                    'channel_type': 2,
                    'last_msg_seq': 6,
                    'recents': [wire(6)],
                  },
                ],
              });
            case '/v2/im/datasource/messages':
              final input = jsonDecode(request.body) as Map<String, dynamic>;
              final cursor = input['startMessageSeq'] as int;
              cursors.add(cursor);
              if (failHistory) return http.Response('{}', 503);
              return response(
                cursor >= 3
                    ? {
                        'messages': [wire(5), wire(4), wire(3)],
                        'more': 1,
                      }
                    : {
                        'messages': [wire(2), wire(1)],
                        'more': 0,
                      },
              );
            default:
              return response({});
          }
        });
        final repo = LiveImRepository(
          client: client,
          apiBaseUrl: 'https://api.example.com',
          clientPlatform: platform,
          wukongGateway: FakeWukongGateway(),
        );
        addTearDown(repo.close);
        await repo.login('13800138000', '123456');
        final member = (await repo.conversations()).single;
        expect(member.currentUserRole, 'member');
        expect(member.subtitle, 'visible history');
        expect(member.lastMessageSeq, 6);
        expect(cursors, [5, 2]);
        expect(
          (await repo.cachedMessages(
            'g',
          )).where((m) => m.event == 'group.members.added'),
          hasLength(2),
          reason: 'display filtering must not delete raw cached notices',
        );
        expect(
          (await repo.cachedMessages(
            'g',
          )).where((m) => m.kind == MessageContentKind.screenshotNotice),
          hasLength(1),
        );
        role = 'admin';
        cursors.clear();
        expect(
          (await repo.conversations()).single.subtitle,
          contains('截取了聊天界面'),
        );
        expect(cursors, isEmpty);
        role = null;
        expect((await repo.conversations()).single.subtitle, 'visible history');
        role = 'member';
        failHistory = true;
        expect((await repo.conversations()).single.subtitle, '打开会话查看消息');
      },
    );
  }
}
