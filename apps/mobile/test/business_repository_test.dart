import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/im/business_repository.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';

void main() {
  test(
    'business datasource authenticates and never accepts a caller uid',
    () async {
      late http.Request captured;
      final client = MockClient((request) async {
        captured = request;
        return http.Response(
          jsonEncode({
            'data': {
              'items': [
                {'channel_id': 'usr_b', 'channel_type': 1, 'recents': []},
              ],
            },
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      });
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => 'access-token',
        client: client,
      );

      final result = await repository.syncConversations(
        version: 0,
        lastMsgSeqs: '',
        messageCount: 20,
      );

      expect(result.single['channel_id'], 'usr_b');
      expect(captured.headers['authorization'], 'Bearer access-token');
      expect(captured.headers['x-client-platform'], 'web');
      expect(jsonDecode(captured.body), isNot(contains('uid')));
    },
  );

  test('business datasource parses ImSession exactly', () async {
    final client = MockClient(
      (_) async => http.Response(
        jsonEncode({
          'data': {
            'imSession': {
              'uid': 'usr_a',
              'token': 'wk1_token',
              'deviceFlag': 0,
              'deviceLevel': 1,
              'tcpUrl': 'tcp://im.example:5100',
              'wsUrl': 'wss://im.example/im',
              'sdk': 'wukongimfluttersdk',
              'issuedAt': '2026-08-11T00:00:00Z',
            },
          },
        }),
        200,
      ),
    );
    final repository = BusinessRepository(
      apiBaseUrl: 'https://api.example',
      platform: 'android',
      accessToken: () => 'token',
      client: client,
    );

    final session = await repository.issueImSession();
    expect(session.sdk, 'wukongimfluttersdk');
    expect(session.tcpAddress, 'im.example:5100');
  });

  test('message datasource sends channel access input only', () async {
    late Map<String, Object?> body;
    final repository = BusinessRepository(
      apiBaseUrl: 'https://api.example',
      platform: 'ios',
      accessToken: () => 'token',
      client: MockClient((request) async {
        body = (jsonDecode(request.body) as Map).map(
          (key, value) => MapEntry(key.toString(), value),
        );
        return http.Response(
          '{"data":{"start_message_seq":1,"end_message_seq":1,"more":0,"messages":[]}}',
          200,
        );
      }),
    );

    await repository.syncMessages(
      channel: const WukongChannel(id: 'group_1', type: 2),
      startMessageSeq: 0,
      endMessageSeq: 0,
      limit: 50,
      pullMode: 1,
    );
    expect(body['channelId'], 'group_1');
    expect(body, isNot(contains('loginUid')));
  });

  test('stream API preserves pinned event identity and cursor', () async {
    final requests = <http.Request>[];
    final repository = BusinessRepository(
      apiBaseUrl: 'https://api.example',
      platform: 'web',
      accessToken: () => 'token',
      client: MockClient((request) async {
        requests.add(request);
        if (request.method == 'POST') {
          return http.Response(
            '{"data":{"eventId":"evt-1","msgEventSeq":1}}',
            200,
          );
        }
        return http.Response(
          '{"data":{"clientMsgNo":"stream-1","events":[]}}',
          200,
        );
      }),
    );

    await repository.appendMessageStreamEvent(
      conversationId: 'conversation/1',
      clientMsgNo: 'stream 1',
      eventId: 'evt-1',
      eventType: 'stream.delta',
      payload: const {'kind': 'text', 'delta': 'hello'},
    );
    await repository.syncMessageStreamEvents(
      conversationId: 'conversation/1',
      clientMsgNo: 'stream 1',
      fromMsgEventSeq: 7,
      eventKey: 'main',
      limit: 100,
    );

    expect(requests.first.url.path, contains('conversation%2F1'));
    final appended = jsonDecode(requests.first.body) as Map<String, Object?>;
    expect(appended['eventId'], 'evt-1');
    expect((appended['payload'] as Map<String, Object?>)['delta'], 'hello');
    expect(requests.last.url.queryParameters['fromMsgEventSeq'], '7');
    expect(requests.last.url.queryParameters['eventKey'], 'main');
  });

  test('channel datasource preserves the official member version cursor', () async {
    final requests = <http.Request>[];
    final repository = BusinessRepository(
      apiBaseUrl: 'https://api.example',
      platform: 'web',
      accessToken: () => 'token',
      client: MockClient((request) async {
        requests.add(request);
        if (request.url.path.endsWith('/channel')) {
          return http.Response(
            '{"data":{"item":{"channel_id":"group_1","channel_type":2,"channel_name":"Group"}}}',
            200,
          );
        }
        return http.Response(
          '{"data":{"items":[{"member_uid":"usr_a","version":42,"is_deleted":0}]}}',
          200,
        );
      }),
    );
    const channel = WukongChannel(id: 'group_1', type: 2);

    final info = await repository.channelInfo(channel);
    final members = await repository.syncChannelMembers(
      channel: channel,
      version: 41,
      limit: 200,
    );

    expect(info['channel_name'], 'Group');
    expect(members.single['version'], 42);
    expect(requests.map((request) => request.url.path), [
      '/v2/im/datasource/channel',
      '/v2/im/datasource/members',
    ]);
    expect(jsonDecode(requests.last.body), {
      'channelId': 'group_1',
      'channelType': 2,
      'version': 41,
      'limit': 200,
    });
  });

  test(
    'message extra datasource preserves the official version cursor',
    () async {
      late http.Request captured;
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => 'token',
        client: MockClient((request) async {
          captured = request;
          return http.Response(
            '{"data":{"items":[{"message_idstr":"101","extra_version":43,"revoke":1,"revoker":"usr_a"}]}}',
            200,
          );
        }),
      );

      final extras = await repository.syncMessageExtras(
        channel: const WukongChannel(id: 'group_1', type: 2),
        version: 42,
        limit: 200,
      );

      expect(captured.url.path, '/v2/im/datasource/message-extras');
      expect(jsonDecode(captured.body), {
        'channelId': 'group_1',
        'channelType': 2,
        'version': 42,
        'limit': 200,
      });
      expect(extras.single['extra_version'], 43);
      expect(extras.single['revoker'], 'usr_a');
    },
  );

  test(
    'reminder datasource syncs and completes official reminder ids',
    () async {
      final requests = <http.Request>[];
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'android',
        accessToken: () => 'token',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.path.endsWith('/done')) {
            return http.Response('{"data":{"status":"ok"}}', 200);
          }
          return http.Response(
            '{"data":{"items":[{"reminder_id":7,"version":43,"done":0}]}}',
            200,
          );
        }),
      );

      final reminders = await repository.syncReminders(version: 42, limit: 200);
      await repository.doneReminders([7]);

      expect(reminders.single['version'], 43);
      expect(requests.map((request) => request.url.path), [
        '/v2/im/datasource/reminders',
        '/v2/im/datasource/reminders/done',
      ]);
      expect(jsonDecode(requests.first.body), {'version': 42, 'limit': 200});
      expect(jsonDecode(requests.last.body), {
        'reminderIds': [7],
      });
    },
  );

  test('media is bound to an authorized WuKong channel before send', () async {
    late http.Request captured;
    final repository = BusinessRepository(
      apiBaseUrl: 'https://api.example',
      platform: 'android',
      accessToken: () => 'token',
      client: MockClient((request) async {
        captured = request;
        return http.Response(
          '{"data":{"mediaId":"med_1","url":"https://cdn.example/med_1"}}',
          200,
        );
      }),
    );

    final url = await repository.bindMedia(
      'med_1',
      const WukongChannel(id: 'group_1', type: 2),
    );

    expect(captured.url.path, '/v2/media/med_1/bind');
    expect(jsonDecode(captured.body), {
      'channelId': 'group_1',
      'channelType': 2,
    });
    expect(url, 'https://cdn.example/med_1');
  });

  test(
    'a datasource 401 refreshes once and replays with the new token',
    () async {
      var token = 'expired-token';
      var refreshes = 0;
      final authorizations = <String?>[];
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => token,
        refreshAccessToken: () async {
          refreshes += 1;
          token = 'fresh-token';
          return true;
        },
        client: MockClient((request) async {
          authorizations.add(request.headers['authorization']);
          if (authorizations.length == 1) {
            return http.Response(
              '{"error":{"code":"TOKEN_EXPIRED","message":"expired"}}',
              401,
            );
          }
          return http.Response('{"data":{"items":[]}}', 200);
        }),
      );

      await repository.syncConversations(
        version: 0,
        lastMsgSeqs: '',
        messageCount: 1,
      );

      expect(refreshes, 1);
      expect(authorizations, ['Bearer expired-token', 'Bearer fresh-token']);
    },
  );

  test(
    'a failed datasource refresh remains fail-closed without replay',
    () async {
      var requests = 0;
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => 'expired-token',
        refreshAccessToken: () async => false,
        client: MockClient((_) async {
          requests += 1;
          return http.Response(
            '{"error":{"code":"TOKEN_EXPIRED","message":"expired"}}',
            401,
          );
        }),
      );

      await expectLater(
        repository.syncConversations(
          version: 0,
          lastMsgSeqs: '',
          messageCount: 1,
        ),
        throwsA(
          isA<BusinessApiException>()
              .having((error) => error.statusCode, 'statusCode', 401)
              .having((error) => error.code, 'code', 'TOKEN_EXPIRED'),
        ),
      );
      expect(requests, 1);
    },
  );

  test(
    'business channel and support APIs keep exact WuKong channel types',
    () async {
      final requests = <http.Request>[];
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => 'token',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.path == '/v2/channels/business') {
            return http.Response(
              jsonEncode({
                'data': {
                  'items': [
                    {
                      'id': 'community_1',
                      'channelType': 4,
                      'category': 'community',
                      'name': '邻里社区',
                      'description': '',
                      'ownerId': 'u1',
                      'visibility': 'public',
                      'joinPolicy': 'open',
                      'postingPolicy': 'members',
                      'memberCount': 3,
                      'subscribed': false,
                      'role': '',
                    },
                  ],
                },
              }),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          if (request.url.path == '/v2/support/skills') {
            return http.Response(
              jsonEncode({
                'data': {
                  'items': [
                    {
                      'id': 'after_sales',
                      'name': '售后',
                      'description': '订单问题',
                      'queueCount': 2,
                      'availableAgents': 1,
                    },
                  ],
                },
              }),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          if (request.url.path == '/v2/support/sessions') {
            return http.Response(
              jsonEncode({
                'data': {
                  'item': {
                    'id': 'session_1',
                    'visitorId': 'u1',
                    'visitorName': '访客',
                    'skillGroupId': 'after_sales',
                    'skillGroupName': '售后',
                    'channelId': 'u1',
                    'channelType': 10,
                    'subject': '退款',
                    'status': 'queued',
                    'queuePosition': 1,
                    'assignedAgentId': '',
                    'agentName': '',
                    'rating': 0,
                    'ratingComment': '',
                  },
                },
              }),
              201,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          return http.Response('{"data":{"status":"ok"}}', 200);
        }),
      );

      final channels = await repository.businessChannels(channelType: 4);
      final expiresAt = DateTime.utc(2026, 8, 20, 12);
      await repository.subscribeBusinessChannel(
        'community_1',
        4,
        expiresAt: expiresAt,
      );
      final skills = await repository.supportSkillGroups();
      final session = await repository.createSupportSession(
        skillGroupId: 'after_sales',
        subject: '退款',
      );

      expect(channels.single.channelType, 4);
      expect(skills.single.queueCount, 2);
      expect(session.channelType, 10);
      expect(session.channelId, 'u1');
      expect(requests[0].url.queryParameters['channelType'], '4');
      expect(
        requests[1].url.path,
        '/v2/channels/business/community_1/subscribe',
      );
      expect(requests[1].url.queryParameters['channelType'], '4');
      expect(jsonDecode(requests[1].body), {
        'expiresAt': expiresAt.toIso8601String(),
      });
      expect(jsonDecode(requests.last.body), containsPair('channelType', 10));
    },
  );

  test(
    'business channel management covers detail members and access lists',
    () async {
      final requests = <http.Request>[];
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'web',
        accessToken: () => 'token',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.path.endsWith('/members')) {
            return _jsonResponse({
              'data': {
                'items': [
                  {
                    'userId': 'u2',
                    'name': '成员',
                    'role': 'member',
                    'joinedAt': '2026-08-12T00:00:00Z',
                    'updatedAt': '2026-08-12T00:00:00Z',
                  },
                ],
              },
            });
          }
          if (request.url.path.endsWith('/access')) {
            return _jsonResponse({
              'data': {
                'items': [
                  {
                    'userId': 'u3',
                    'name': '受限用户',
                    'accessType': 'deny',
                    'reason': 'abuse',
                    'createdAt': '2026-08-12T00:00:00Z',
                  },
                ],
              },
            });
          }
          if (request.method == 'GET') {
            return _jsonResponse({
              'data': {
                'item': {
                  'id': 'live_1',
                  'channelType': 9,
                  'name': '直播间',
                  'ownerId': 'u1',
                  'visibility': 'public',
                  'joinPolicy': 'open',
                  'postingPolicy': 'members',
                  'memberCount': 2,
                  'subscribed': true,
                  'role': 'owner',
                  'slowModeSeconds': 5,
                },
              },
            });
          }
          if (request.method == 'PATCH' &&
              request.url.path == '/v2/channels/business/live_1') {
            final body = jsonDecode(request.body) as Map<String, Object?>;
            return _jsonResponse({
              'data': {
                'item': {
                  'id': 'live_1',
                  'channelType': 9,
                  'name': '直播间',
                  'ownerId': 'u1',
                  'visibility': 'public',
                  'joinPolicy': 'open',
                  'postingPolicy': 'members',
                  'memberCount': 2,
                  'subscribed': true,
                  'role': 'owner',
                  'slowModeSeconds': body['slowModeSeconds'],
                  'sendBan': body['sendBan'],
                },
              },
            });
          }
          return _jsonResponse({
            'data': {'status': 'ok'},
          });
        }),
      );

      final channel = await repository.businessChannel('live_1', 9);
      final members = await repository.businessChannelMembers('live_1', 9);
      final access = await repository.businessChannelAccess('live_1', 9);
      await repository.updateBusinessChannel(
        'live_1',
        9,
        slowModeSeconds: 15,
        sendBan: true,
      );
      await repository.updateBusinessChannelMember(
        'live_1',
        9,
        'u2',
        mutedUntil: DateTime.utc(2026, 8, 13),
      );
      await repository.updateBusinessChannelMember(
        'live_1',
        9,
        'u2',
        expiresAt: DateTime.utc(2026, 8, 14),
      );
      await repository.updateBusinessChannelMember(
        'live_1',
        9,
        'u2',
        clearExpiry: true,
      );
      await repository.setBusinessChannelAccess(
        'live_1',
        9,
        'u3',
        'deny',
        false,
      );

      expect(channel.slowModeSeconds, 5);
      expect(members.single.userId, 'u2');
      expect(access.single.accessType, 'deny');
      expect(
        requests
            .where((request) => request.method == 'PATCH')
            .first
            .url
            .queryParameters['channelType'],
        '9',
      );
      expect(
        jsonDecode(
          requests.where((request) => request.method == 'PATCH').first.body,
        ),
        containsPair('sendBan', true),
      );
      final memberUpdates = requests
          .where(
            (request) =>
                request.method == 'PATCH' &&
                request.url.path.endsWith('/members/u2'),
          )
          .map((request) => jsonDecode(request.body) as Map<String, Object?>)
          .toList();
      expect(memberUpdates[1], contains('expiresAt'));
      expect(memberUpdates[2], containsPair('clearExpiry', true));
      expect(
        requests.last.url.path,
        '/v2/channels/business/live_1/access/deny/u3',
      );
      expect(requests.last.method, 'DELETE');
    },
  );

  test(
    'moments and stickers use durable ids and hydrate signed media URLs',
    () async {
      final requests = <http.Request>[];
      final repository = BusinessRepository(
        apiBaseUrl: 'https://api.example',
        platform: 'android',
        accessToken: () => 'token',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.path.startsWith('/v2/media/')) {
            final id = request.url.pathSegments[2];
            return _jsonResponse({
              'data': {'mediaId': id, 'url': 'https://cdn.example/$id'},
            });
          }
          if (request.url.path == '/v2/moments' && request.method == 'GET') {
            return _jsonResponse({
              'data': {
                'nextCursor': 'cursor-2',
                'items': [
                  {
                    'id': 'moment-1',
                    'authorId': 'u1',
                    'authorName': 'Alice',
                    'content': 'hello',
                    'mediaKind': 'images',
                    'media': [
                      {'id': 'moment-media-1', 'mime': 'image/webp'},
                    ],
                    'visibility': 'friends',
                    'visibleUserIds': <String>[],
                    'location': <String, Object?>{},
                    'likeCount': 1,
                    'commentCount': 0,
                    'likedByMe': false,
                    'comments': <Object?>[],
                    'status': 'published',
                    'createdAt': '2026-08-11T00:00:00Z',
                    'updatedAt': '2026-08-11T00:00:00Z',
                  },
                ],
              },
            });
          }
          if (request.url.path == '/v2/stickers/packs') {
            return _jsonResponse({
              'data': {
                'items': [
                  {
                    'id': 'pack-1',
                    'categoryId': 'popular',
                    'categoryName': 'Popular',
                    'name': 'Animals',
                    'description': '',
                    'coverMediaId': 'cover-1',
                    'coverMime': 'image/webp',
                    'status': 'published',
                    'sortOrder': 1,
                    'favorite': true,
                    'items': [
                      {
                        'id': 'sticker-1',
                        'packId': 'pack-1',
                        'name': 'Cat',
                        'mediaId': 'sticker-media-1',
                        'mime': 'image/webp',
                        'emoji': '🐱',
                        'status': 'published',
                        'metadata': <String, Object?>{},
                        'favorite': false,
                        'useCount': 0,
                      },
                    ],
                  },
                ],
              },
            });
          }
          if (request.url.path == '/v2/stickers/sticker-1/used') {
            return http.Response('', 204);
          }
          return _jsonResponse({'data': <String, Object?>{}});
        }),
      );

      final moments = await repository.moments(cursor: 'cursor-1');
      final packs = await repository.stickerPacks(categoryId: 'popular');
      await repository.recordStickerUse('sticker-1');

      expect(moments.nextCursor, 'cursor-2');
      expect(
        moments.items.single.media.single.url,
        'https://cdn.example/moment-media-1',
      );
      expect(packs.single.coverUrl, 'https://cdn.example/cover-1');
      expect(
        packs.single.items.single.url,
        'https://cdn.example/sticker-media-1',
      );
      expect(
        requests.first.url.queryParameters,
        containsPair('cursor', 'cursor-1'),
      );
      expect(
        requests
            .where((request) => request.url.path == '/v2/stickers/packs')
            .single
            .url
            .queryParameters,
        containsPair('categoryId', 'popular'),
      );
      expect(requests.last.method, 'POST');
    },
  );
}

http.Response _jsonResponse(Map<String, Object?> value, [int status = 200]) =>
    http.Response(
      jsonEncode(value),
      status,
      headers: {'content-type': 'application/json; charset=utf-8'},
    );
