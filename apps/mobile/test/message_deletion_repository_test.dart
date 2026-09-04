import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'support/fake_wukong_gateway.dart';

class MemoryStore extends SecureLocalStore {
  final values = <String, Object>{};
  bool failDeletionWrite = false;
  @override
  Future<Object?> readJson(String key) async => values[key];
  @override
  Future<void> writeJson(String key, Object value) async {
    if (failDeletionWrite && key.startsWith('mutual-deletions.')) {
      throw StateError('disk unavailable');
    }
    values[key] = jsonDecode(jsonEncode(value)) as Object;
  }

  @override
  Future<void> remove(String key) async => values.remove(key);
  @override
  Future<void> clearAccountData() async => values.clear();
}

class Fixture {
  final store = MemoryStore();
  final gateway = FakeWukongGateway();
  late final LiveImRepository repo;
  String uid = 'me';
  int deletes = 0;
  Completer<void>? pending;
  Fixture() {
    repo = LiveImRepository(
      store: store,
      wukongGateway: gateway,
      apiBaseUrl: 'https://test.invalid',
      client: MockClient((request) async {
        Map<String, Object?> data = {};
        switch (request.url.path) {
          case '/v2/auth/login':
            data = {
              'accessToken': uid,
              'refreshToken': uid,
              'user': {
                'id': uid,
                'name': uid,
                'canDeleteMessagesForEveryone': true,
              },
              'imSession': {
                'uid': uid,
                'token': 'wk1_test',
                'deviceFlag': 2,
                'deviceLevel': 1,
                'tcpUrl': 'tcp://test.invalid:5100',
                'wsUrl': 'wss://test.invalid/ws',
                'sdk': 'wukongimfluttersdk',
                'issuedAt': '2026-09-04T00:00:00Z',
              },
            };
          case '/v2/channels/conversations':
            data = {
              'items': [
                {
                  'conversation': {
                    'id': 'c',
                    'type': 'direct',
                    'title': 'peer',
                    'updatedAt': '2026-09-04T00:00:00Z',
                  },
                  'members': [
                    {'id': uid, 'name': uid},
                    {'id': 'peer', 'name': 'peer'},
                  ],
                },
              ],
            };
          case '/v2/messages/delete-for-everyone':
            deletes++;
            await pending?.future;
            data = {
              'conversationId': 'c',
              'messageIds': ['100'],
              'version': 1,
            };
          default:
            data = {'items': [], 'messages': []};
        }
        return http.Response(
          jsonEncode({'data': data}),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );
  }
  Future<void> login() async {
    await repo.login('13800138000', '123456');
    await repo.connect();
    await repo.conversations();
    await repo.cachedMessages('c');
  }

  void command(String event, Map<String, Object?> payload) {
    gateway.emit(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'cmd': event,
          'param': {'schemaVersion': 1, 'event': event, 'payload': payload},
        },
      ),
    );
  }
}

ChatMessage message(String id, {String? reply}) => ChatMessage(
  id: id,
  clientMessageId: 'client-$id',
  conversationId: 'c',
  senderId: 'peer',
  senderName: 'peer',
  text: 'body $id',
  sentAt: DateTime.utc(2026),
  isMine: false,
  conversationSeq: int.parse(id),
  replyToId: reply,
  replyToText: reply == null ? null : 'deleted original body',
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'CMD deletes persisted copies and later cache writes cannot restore quotes',
    () async {
      final f = Fixture();
      addTearDown(f.repo.close);
      await f.login();
      final original = message('100');
      final reply = message('101', reply: '100');
      await f.repo.persistMessages('c', [original, reply]);
      await f.store.writeJson('favorites', [original.toJson(), reply.toJson()]);
      final received = f.repo.events.firstWhere(
        (e) => e.type == ImEventType.messagesDeleted,
      );
      f.command('messages.deleted', {
        'conversationId': 'c',
        'messageIds': ['100'],
        'version': 1,
      });
      await received.timeout(const Duration(seconds: 2));
      expect(f.repo.isMessageDeleted('100'), true);
      // Simulate a controller or delayed page snapshot writing after the CMD.
      await f.repo.persistMessages('c', [original, reply]);
      await f.repo.saveFavorite(reply);
      for (final key in ['messages.c', 'favorites']) {
        final cached = (await f.store.readJson(key))! as List;
        expect(cached.map((m) => (m as Map)['id']), ['101']);
        expect((cached.single as Map).containsKey('replyToText'), false);
        expect((cached.single as Map).containsKey('replyToId'), false);
      }
      final permission = f.repo.events.firstWhere(
        (e) => e.type == ImEventType.messagePermissionsChanged,
      );
      f.command('user.message_permissions.updated', {
        'userId': 'me',
        'changeId': 'unique',
      });
      await permission.timeout(const Duration(seconds: 2));
    },
  );

  test(
    'server success stays successful when local tombstone persistence fails',
    () async {
      final f = Fixture();
      addTearDown(f.repo.close);
      await f.login();
      f.store.failDeletionWrite = true;
      expect(await f.repo.deleteMessagesForEveryone('c', ['100']), ['100']);
      expect(f.deletes, 1);
      expect(f.repo.isMessageDeleted('100'), true);
    },
  );

  test('late delete response cannot apply to a new account', () async {
    final f = Fixture();
    addTearDown(f.repo.close);
    await f.login();
    f.pending = Completer<void>();
    final request = f.repo.deleteMessagesForEveryone('c', ['100']);
    await Future<void>.delayed(Duration.zero);
    await f.repo.logout();
    f.uid = 'other-account';
    await f.login();
    f.pending!.complete();
    expect(await request, ['100']);
    expect(f.repo.isMessageDeleted('100'), false);
    expect(f.store.values.containsKey('mutual-deletions.other-account'), false);
  });
}
