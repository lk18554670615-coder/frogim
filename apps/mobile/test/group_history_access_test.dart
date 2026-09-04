import 'dart:convert';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/history_access.dart';
import 'package:linli_im/im/local_conversation_cache.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'support/fake_wukong_gateway.dart';

class HistoryGateway extends FakeWukongGateway implements WukongHistoryCache {
  final invalidations = <GroupHistoryAccess?>[];
  @override
  Future<void> invalidateGroupHistory(
    String channelId,
    GroupHistoryAccess? access,
  ) async {
    expect(channelId, 'g1');
    invalidations.add(access);
  }
}

http.Response reply(Map<String, Object?> data) => http.Response(
  jsonEncode({'data': data}),
  200,
  headers: {'content-type': 'application/json'},
);

Map<String, Object?> wire(int seq) => {
  'message_idstr': 'm$seq',
  'message_seq': seq,
  'client_msg_no': 'client-m$seq',
  'from_uid': 'peer',
  'channel_id': 'g1',
  'channel_type': 2,
  'timestamp': 1788307200 + seq,
  'payload': {'type': 1, 'content': 'history $seq'},
};

ChatMessage local(int seq, {MessageStatus status = MessageStatus.sent}) =>
    ChatMessage(
      id: 'local-$seq',
      clientMessageId: 'local-client-$seq',
      conversationId: 'g1',
      senderId: seq == 0 ? 'user-1' : 'peer',
      senderName: 'Test',
      text: 'local $seq',
      sentAt: DateTime.utc(2026, 9, 2),
      isMine: seq == 0,
      conversationSeq: seq,
      status: status,
    );

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });
  test(
    'history policy validates sequence and strict legacy whole-second boundary',
    () {
      expect(GroupHistoryAccess.parse(null), isNull);
      expect(
        GroupHistoryAccess.parse({'version': 1, 'visibleAll': false}),
        isNull,
      );
      final boundary = GroupHistoryAccess.parse({
        'version': 1,
        'visibleAll': false,
        'afterSeq': 10,
      })!;
      expect(boundary.allows(10, DateTime.now()), isFalse);
      expect(boundary.allows(11, DateTime.now()), isTrue);
      final legacy = GroupHistoryAccess.parse({
        'version': 1,
        'visibleAll': false,
        'afterTimestamp': 100,
      })!;
      expect(
        legacy.allows(99, DateTime.fromMillisecondsSinceEpoch(100999)),
        isFalse,
      );
      expect(
        legacy.allows(99, DateTime.fromMillisecondsSinceEpoch(101000)),
        isTrue,
      );
    },
  );

  for (final platform in ['android', 'ios', 'web', 'macos']) {
    test(
      '$platform: close/open/close, cache purge, pending and drafts, stale CMD and offline recovery',
      () async {
        var version = 1;
        var visible = false;
        var cutoff = 10;
        var offline = false;
        var profileReads = 0;
        Map<String, Object?> policy() => {
          'version': version,
          'visibleAll': visible,
          'afterSeq': cutoff,
        };
        Map<String, Object?> profile() => {
          'conversationId': 'g1',
          'ownerId': 'peer',
          'name': 'Test group',
          'updatedAt': '2026-09-02T00:00:00Z',
          'historyVisibleToNewMembers': visible,
          'historyPolicyVersion': version,
          'historyAccess': policy(),
        };
        final gateway = HistoryGateway();
        final store = SecureLocalStore();
        final client = MockClient((request) async {
          if (request.url.path == '/v2/auth/login') {
            return reply({
              'accessToken': 'access-token',
              'refreshToken': 'refresh-token',
              'user': {'id': 'user-1', 'name': 'Me', 'phone': '13800138000'},
              'imSession': {
                'uid': 'user-1',
                'token': 'wk1_test',
                'deviceFlag': 2,
                'deviceLevel': 1,
                'tcpUrl': 'tcp://im.example.com:5100',
                'wsUrl': 'wss://im.example.com/ws',
                'sdk': 'wukongimfluttersdk',
                'issuedAt': '2026-09-02T00:00:00Z',
              },
            });
          }
          if (offline) return http.Response('{}', 503);
          if (request.url.path == '/v2/im/datasource/message-extras') {
            return reply({'items': []});
          }
          if (request.url.path == '/v2/channels/conversations') {
            return reply({
              'items': [
                {
                  'conversation': {
                    'id': 'g1',
                    'type': 'group',
                    'title': 'Test group',
                    'updatedAt': '2026-09-02T00:00:00Z',
                  },
                  'historyAccess': policy(),
                  'members': [
                    {'id': 'user-1', 'name': 'Me'},
                    {'id': 'peer', 'name': 'Peer'},
                  ],
                },
              ],
            });
          }
          if (request.url.path == '/v2/channels/groups/g1') {
            profileReads++;
            return reply(profile());
          }
          if (request.url.path == '/v2/im/datasource/conversations') {
            return reply({'items': []});
          }
          if (request.url.path == '/v2/im/datasource/messages') {
            return reply({
              'messages': [wire(9), wire(10), wire(11)],
              'more': 0,
            });
          }
          return http.Response('{}', 404);
        });
        final repository = LiveImRepository(
          client: client,
          store: store,
          apiBaseUrl: 'https://api.example.com',
          clientPlatform: platform,
          wukongGateway: gateway,
        );
        addTearDown(repository.close);
        await repository.login('13800138000', '123456');
        await repository.conversations();
        expect(
          (await repository.messages('g1')).map((m) => m.conversationSeq),
          [11],
        );
        expect(repository.canReadCachedMessage(local(10)), isFalse);
        visible = true;
        version = 2;
        await repository.groupProfile('g1');
        expect(
          (await repository.messages('g1')).map((m) => m.conversationSeq),
          [9, 10, 11],
        );
        await repository.persistMessages('g1', [
          local(9),
          local(11),
          local(0, status: MessageStatus.failed),
        ]);
        await store.writeJson('favorites', [
          local(9).toJson(),
          local(11).toJson(),
        ]);
        await store.writeJson('draft.g1', {'text': 'do not remove my draft'});
        visible = false;
        version = 3;
        await repository.groupProfile('g1');
        final retained = (await store.readJson('messages.g1') as List)
            .cast<Map>();
        expect(retained.map((m) => m['conversationSeq']).toSet(), {0, 11});
        expect((await store.readJson('favorites') as List).length, 1);
        expect(await store.readJson('draft.g1'), {
          'text': 'do not remove my draft',
        });
        expect(
          (await LocalConversationCache(store).readMessages(
            'user-1',
            const WukongChannel(id: 'g1', type: 2),
          )).map((m) => m.messageSeq),
          [11],
        );
        expect(gateway.invalidations.last?.version, 3);
        // Initialization must purge native SDK rows even if policies arrived first.
        final beforeConnect = gateway.invalidations.length;
        await repository.connect();
        await Future<void>.delayed(const Duration(milliseconds: 30));
        expect(gateway.invalidations.length, greaterThan(beforeConnect));
        // A newer CMD immediately revokes trust, even if a stale HTTP response arrives.
        final changed = repository.events.firstWhere(
          (e) => e.type == ImEventType.groupHistoryChanged,
        );
        gateway.emit(
          const WukongGatewayEvent(
            kind: WukongGatewayEventKind.command,
            data: {
              'type': 99,
              'cmd': 'group.system',
              'param': {
                'schemaVersion': 1,
                'event': 'group.system',
                'payload': {
                  'conversationId': 'g1',
                  'event': 'group.history.updated',
                  'data': {'historyPolicyVersion': 4},
                },
              },
            },
          ),
        );
        await changed.timeout(const Duration(seconds: 2));
        await Future<void>.delayed(const Duration(milliseconds: 30));
        expect(repository.canReadCachedMessage(local(11)), isFalse);
        version = 4;
        visible = true;
        await repository.groupProfile('g1');
        expect(
          (await repository.messages(
            'g1',
          )).where((m) => m.conversationSeq > 0).map((m) => m.conversationSeq),
          [9, 10, 11],
        );
        expect(
          (await repository.cachedMessages('g1')).any(
            (m) => m.status == MessageStatus.failed && m.conversationSeq == 0,
          ),
          isTrue,
        );
        // Duplicate notifications are idempotent.
        final reads = profileReads;
        gateway.emit(
          const WukongGatewayEvent(
            kind: WukongGatewayEventKind.command,
            data: {
              'cmd': 'group.system',
              'param': {
                'schemaVersion': 1,
                'event': 'group.system',
                'payload': {
                  'conversationId': 'g1',
                  'event': 'group.history.updated',
                  'data': {'historyPolicyVersion': 4},
                },
              },
            },
          ),
        );
        await Future<void>.delayed(const Duration(milliseconds: 30));
        expect(profileReads, reads);
        offline = true;
        gateway.setConnectionState(WukongConnectionState.networkUnavailable);
        await Future<void>.delayed(const Duration(milliseconds: 30));
        expect(
          (await repository.cachedMessages(
            'g1',
          )).where((m) => m.conversationSeq > 0),
          isEmpty,
        );
        offline = false;
        visible = false;
        version = 5;
        cutoff = 11;
        await repository.conversations();
        expect(
          (await repository.messages('g1')).where((m) => m.conversationSeq > 0),
          isEmpty,
        );
        // Same-policy-version responses from the previous membership cannot reopen it.
        cutoff = 10;
        await repository.groupProfile('g1');
        expect(repository.canReadCachedMessage(local(11)), isFalse);
      },
    );
  }

  test(
    'cold-start unknown group never exposes old confirmed page snapshots',
    () async {
      final store = SecureLocalStore();
      await store.writeJson('channel.g1', {'id': 'g1', 'type': 2});
      await store.writeJson('messages.g1', [local(9).toJson()]);
      final repository = LiveImRepository(
        apiBaseUrl: 'https://api.example.com',
        store: store,
        client: MockClient(
          (_) async => throw StateError('cold cache must not access network'),
        ),
        wukongGateway: HistoryGateway(),
      );
      expect(await repository.cachedMessages('g1'), isEmpty);
      await repository.close();
    },
  );
}
