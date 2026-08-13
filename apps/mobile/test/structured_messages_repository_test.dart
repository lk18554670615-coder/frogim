import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('名片消息只发送协议允许的公开资料字段', () async {
    final gateway = FakeWukongGateway();
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _conversationResponse();
        }
        if (request.url.path == '/v2/im/datasource/conversations') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        return http.Response('{}', 404);
      }),
      gateway: gateway,
    );
    await repository.login('13800138000', '123456');

    final sent = await repository.send(
      ChatMessage(
        id: 'local-contact',
        clientMessageId: 'client-contact',
        conversationId: 'c1',
        senderId: 'user-1',
        senderName: '测试用户',
        text: '[名片] 林安',
        sentAt: DateTime.now(),
        isMine: true,
        kind: MessageContentKind.contact,
        contactUserId: 'friend-1',
        contactName: '林安',
        contactHandle: 'linan',
        contactAvatarUrl: 'https://cdn.example.com/avatar.png',
      ),
    );

    final payload = gateway.sentMessages.single.payload;
    expect(payload['type'], 7);
    expect(payload, containsPair('userId', 'friend-1'));
    expect(payload, containsPair('name', '林安'));
    expect(payload, containsPair('handle', 'linan'));
    expect(
      payload,
      containsPair('avatarUrl', 'https://cdn.example.com/avatar.png'),
    );
    expect(
      payload.keys.toSet(),
      containsAll(<String>{'type', 'userId', 'name', 'handle', 'avatarUrl'}),
    );
    expect(jsonEncode(payload), isNot(contains('phone')));
    expect(jsonEncode(payload), isNot(contains('remark')));
    expect(jsonEncode(payload), isNot(contains('tags')));
    expect(sent.kind, MessageContentKind.contact);
    expect(sent.contactUserId, 'friend-1');
    await repository.close();
  });

  test('位置消息发送真实结构并可从服务端响应还原', () async {
    final gateway = FakeWukongGateway();
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _conversationResponse();
        }
        if (request.url.path == '/v2/im/datasource/conversations') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        return http.Response('{}', 404);
      }),
      gateway: gateway,
    );
    await repository.login('13800138000', '123456');

    final sent = await repository.send(
      ChatMessage(
        id: 'local-location',
        clientMessageId: 'client-location',
        conversationId: 'c1',
        senderId: 'user-1',
        senderName: '测试用户',
        text: '[位置] 人民广场',
        sentAt: DateTime.now(),
        isMine: true,
        kind: MessageContentKind.location,
        latitude: 31.2304,
        longitude: 121.4737,
        locationName: '人民广场',
        locationAddress: '上海市黄浦区人民大道',
      ),
    );

    final payload = gateway.sentMessages.single.payload;
    expect(payload['type'], 6);
    expect(payload, containsPair('latitude', 31.2304));
    expect(payload, containsPair('longitude', 121.4737));
    expect(payload, containsPair('name', '人民广场'));
    expect(payload, containsPair('address', '上海市黄浦区人民大道'));
    expect(sent.kind, MessageContentKind.location);
    expect(sent.latitude, 31.2304);
    expect(sent.longitude, 121.4737);
    expect(sent.locationName, '人民广场');
    await repository.close();
  });

  test('平台公告置顶优先且阅读接口使用公告编号', () async {
    final requestedPaths = <String>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        requestedPaths.add('${request.method} ${request.url.path}');
        if (request.url.path == '/v2/announcements') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'id': 'newer',
                  'title': '普通公告',
                  'content': '内容',
                  'status': 'published',
                  'pinned': false,
                  'publishedAt': '2026-07-31T12:00:00Z',
                },
                {
                  'id': 'pinned',
                  'title': '置顶公告',
                  'content': '重要内容',
                  'status': 'published',
                  'pinned': true,
                  'publishedAt': '2026-07-30T12:00:00Z',
                },
              ],
            },
          });
        }
        if (request.url.path == '/v2/announcements/pinned/read') {
          return http.Response('', 204);
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final announcements = await repository.announcements();
    await repository.markAnnouncementRead(announcements.first.id);

    expect(announcements.first.id, 'pinned');
    expect(announcements.first.unread, isTrue);
    expect(requestedPaths, [
      'GET /v2/announcements',
      'POST /v2/announcements/pinned/read',
    ]);
    await repository.close();
  });

  test('会话列表使用服务端成员资料恢复单聊标题和头像', () async {
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'conversation': {
                    'id': 'c1',
                    'type': 'direct',
                    'title': '',
                    'updatedAt': '2026-07-31T12:00:00Z',
                    'lastMessageSeq': 1,
                  },
                  'membership': {'lastReadSeq': 1},
                  'mentionUnreadCount': 2,
                  'members': [
                    {'userId': 'user-1', 'name': '测试用户', 'handle': 'tester'},
                    {
                      'userId': 'friend-1',
                      'name': '林安',
                      'handle': 'linan',
                      'avatarUrl': 'https://cdn.example.com/linan.png',
                    },
                  ],
                },
              ],
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final conversations = await repository.conversations();

    expect(conversations.single.title, '林安');
    expect(conversations.single.avatarUrl, 'https://cdn.example.com/linan.png');
    expect(conversations.single.mentionUnreadCount, 2);
    expect(conversations.single.members, hasLength(2));
    await repository.close();
  });

  test('媒体转发调用服务端原类型转发接口而非降级文本', () async {
    http.Request? forwardRequest;
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/messages/forward') {
          forwardRequest = request;
          return _jsonResponse({
            'data': {
              'duplicate': false,
              'messages': [
                _message(
                  id: 'forwarded-image',
                  type: 'image',
                  body: const {
                    'mediaId': 'media-1',
                    'downloadUrl': 'https://cdn.example.com/signed-image',
                    'forwarded': true,
                    'sourceMessageId': 'image-1',
                  },
                )..['conversationId'] = 'target',
              ],
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final forwarded = await repository.forwardMessages(
      'target',
      ['image-1'],
      mode: 'separate',
      clientBatchId: 'batch-1',
    );

    final payload = jsonDecode(forwardRequest!.body) as Map<String, Object?>;
    expect(payload, {
      'targetConversationId': 'target',
      'sourceMessageIds': ['image-1'],
      'mode': 'separate',
      'clientBatchId': 'batch-1',
    });
    expect(forwarded.single.kind, MessageContentKind.image);
    expect(forwarded.single.mediaUrl, 'https://cdn.example.com/signed-image');
    await repository.close();
  });

  test('合并转发恢复为可读聊天记录而不是空白消息', () async {
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/messages/forward') {
          return _jsonResponse({
            'data': {
              'duplicate': false,
              'messages': [
                _message(
                  id: 'history-1',
                  type: 'chat_history',
                  body: const {
                    'forwarded': true,
                    'mode': 'merged',
                    'entries': [
                      {'summary': '第一条消息'},
                      {'summary': '[图片]'},
                    ],
                  },
                )..['conversationId'] = 'target',
              ],
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final forwarded = await repository.forwardMessages(
      'target',
      ['message-1', 'message-2'],
      mode: 'merged',
      clientBatchId: 'batch-history',
    );

    expect(forwarded.single.kind, MessageContentKind.chatHistory);
    expect(forwarded.single.text, contains('第一条消息'));
    expect(forwarded.single.text, contains('[图片]'));
    expect(forwarded.single.chatHistoryEntries, hasLength(2));
    expect(forwarded.single.chatHistoryEntries.map((entry) => entry.summary), [
      '第一条消息',
      '[图片]',
    ]);
    await repository.close();
  });

  test('视频消息保持独立类型并保留受保护下载地址', () async {
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _conversationResponse();
        }
        if (request.url.path == '/v2/im/datasource/conversations') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        if (request.url.path == '/v2/im/datasource/messages') {
          return _jsonResponse({
            'data': {
              'messages': [
                {
                  'message_idstr': 'video-1',
                  'message_seq': 1,
                  'client_msg_no': 'client-video-1',
                  'client_seq': 1,
                  'from_uid': 'friend-1',
                  'channel_id': 'friend-1',
                  'channel_type': 1,
                  'timestamp': 1786406400,
                  'payload': {
                    'type': 5,
                    'mediaId': 'media-video-1',
                    'mime': 'video/mp4',
                    'fileName': 'clip.mp4',
                    'duration': 12,
                    'url': 'https://cdn.example.com/signed-video',
                  },
                },
              ],
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final messages = await repository.messages('c1');

    expect(messages.single.kind, MessageContentKind.video);
    expect(messages.single.fileName, 'clip.mp4');
    expect(messages.single.durationSeconds, 12);
    expect(messages.single.mediaUrl, 'https://cdn.example.com/signed-video');
    await repository.close();
  });

  test('群二维码、黑名单和通知偏好均调用真实服务端接口', () async {
    final requests = <http.Request>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        requests.add(request);
        if (request.url.path == '/v2/contacts/blocks') {
          return _jsonResponse({
            'data': {
              'items': [
                {'id': 'blocked-1', 'name': '被屏蔽用户', 'handle': 'blocked'},
              ],
            },
          });
        }
        return _jsonResponse({'data': <String, Object?>{}});
      }),
    );
    await repository.login('13800138000', '123456');

    await repository.joinGroupByQr('gqr-token');
    final blocked = await repository.blockedUsers();
    await repository.registerDevice(
      deviceId: 'device-1',
      platform: 'android',
      provider: 'getui',
      pushToken: 'cid-1',
      notificationsEnabled: true,
      previewEnabled: false,
      soundEnabled: false,
      vibrationEnabled: true,
    );

    expect(blocked.single.id, 'blocked-1');
    expect(requests.map((request) => '${request.method} ${request.url.path}'), [
      'POST /v2/channels/groups/join/qr',
      'GET /v2/contacts/blocks',
      'POST /v2/users/me/devices',
    ]);
    expect(
      jsonDecode(requests.last.body),
      containsPair('previewEnabled', false),
    );
    expect(jsonDecode(requests.last.body), containsPair('soundEnabled', false));
    await repository.close();
  });

  test('好友拒绝撤回备注删除和拉黑使用完整服务端状态接口', () async {
    final requests = <http.Request>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        requests.add(request);
        return request.method == 'DELETE'
            ? http.Response('', 204)
            : _jsonResponse({'data': <String, Object?>{}});
      }),
    );
    await repository.login('13800138000', '123456');

    await repository.rejectFriendRequest('request-in');
    await repository.cancelFriendRequest('request-out');
    await repository.updateFriendMetadata(
      'friend-1',
      remark: '楼上邻居',
      tags: ['社区', '咖啡'],
    );
    await repository.deleteFriend('friend-1');
    await repository.blockUser('friend-2', true);

    expect(requests.map((request) => '${request.method} ${request.url.path}'), [
      'POST /v2/contacts/requests/request-in/reject',
      'POST /v2/contacts/requests/request-out/cancel',
      'PATCH /v2/contacts/friends/friend-1',
      'DELETE /v2/contacts/friends/friend-1',
      'PUT /v2/contacts/blocks/friend-2',
    ]);
    expect(jsonDecode(requests[2].body), {
      'remark': '楼上邻居',
      'tags': ['社区', '咖啡'],
    });
    expect(jsonDecode(requests[4].body), {'blocked': true});
    await repository.close();
  });

  test('群邀请列表可解析并调用同意接口', () async {
    final requests = <String>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        requests.add('${request.method} ${request.url.path}');
        if (request.url.path == '/v2/channels/group-invitations') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'invite': {
                    'id': 'ginv-1',
                    'conversationId': 'group-1',
                    'inviterId': 'friend-1',
                    'inviteeId': 'user-1',
                    'status': 'pending',
                    'createdAt': '2026-07-31T12:00:00Z',
                    'expiresAt': '2099-08-01T12:00:00Z',
                    'updatedAt': '2026-07-31T12:00:00Z',
                  },
                  'groupName': '周末咖啡局',
                  'inviter': {
                    'id': 'friend-1',
                    'name': '林安',
                    'handle': 'linan',
                  },
                  'outgoing': false,
                },
              ],
            },
          });
        }
        return _jsonResponse({'data': <String, Object?>{}});
      }),
    );
    await repository.login('13800138000', '123456');

    final invitations = await repository.groupInvitations();
    await repository.respondGroupInvitation(invitations.single.id, 'accept');

    expect(invitations.single.groupName, '周末咖啡局');
    expect(invitations.single.inviter.name, '林安');
    expect(invitations.single.pending, isTrue);
    expect(requests, [
      'GET /v2/channels/group-invitations',
      'POST /v2/channels/group-invitations/ginv-1/accept',
    ]);
    await repository.close();
  });

  test('群资料与成员角色字段从真实接口完整解析', () async {
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/groups/group-1') {
          return _jsonResponse({
            'data': {
              'conversationId': 'group-1',
              'ownerId': 'user-1',
              'name': '周末咖啡局',
              'announcement': '周六下午见',
              'announcementVersion': 3,
              'joinPolicy': 'invite',
              'allowMemberAddFriend': false,
              'updatedAt': '2026-07-31T12:00:00Z',
            },
          });
        }
        if (request.url.path == '/v2/channels/groups/group-1/members') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'userId': 'user-1',
                  'name': '测试用户',
                  'handle': 'tester',
                  'role': 'owner',
                  'groupNickname': '群主',
                  'joinedAt': '2026-07-30T12:00:00Z',
                },
                {
                  'userId': 'friend-1',
                  'name': '林安',
                  'handle': 'linan',
                  'role': 'admin',
                  'mutedUntil': '2026-08-01T12:00:00Z',
                  'joinedAt': '2026-07-31T10:00:00Z',
                },
              ],
            },
          });
        }
        if (request.url.path == '/v2/contacts/friends') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final profile = await repository.groupProfile('group-1');
    final members = await repository.groupMembers('group-1');

    expect(profile.name, '周末咖啡局');
    expect(profile.announcement, '周六下午见');
    expect(profile.joinPolicy, 'invite');
    expect(profile.allowMemberAddFriend, isFalse);
    expect(members.map((member) => member.role), ['owner', 'admin']);
    expect(members.first.groupNickname, '群主');
    expect(members.last.mutedUntil, isNotNull);
    await repository.close();
  });
}

LiveImRepository _repository(
  http.Client client, {
  FakeWukongGateway? gateway,
}) => LiveImRepository(
  client: client,
  apiBaseUrl: 'https://api.example.com',
  wukongGateway: gateway ?? FakeWukongGateway(),
);

http.Response _loginResponse() => _jsonResponse({
  'data': {
    'accessToken': 'access-token',
    'refreshToken': 'refresh-token',
    'user': {
      'id': 'user-1',
      'name': '测试用户',
      'handle': 'tester',
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
      'issuedAt': '2026-08-11T00:00:00Z',
    },
  },
});

http.Response _conversationResponse() => _jsonResponse({
  'data': {
    'items': [
      {
        'conversation': {
          'id': 'c1',
          'type': 'direct',
          'title': '林安',
          'updatedAt': '2026-08-11T00:00:00Z',
        },
        'members': [
          {'id': 'user-1', 'name': '测试用户'},
          {'id': 'friend-1', 'name': '林安'},
        ],
      },
    ],
  },
});

http.Response _jsonResponse(Map<String, Object?> body, [int status = 200]) =>
    http.Response(
      jsonEncode(body),
      status,
      headers: {'content-type': 'application/json'},
    );

Map<String, Object?> _message({
  required String id,
  required String type,
  required Map<String, Object?> body,
}) => {
  'id': id,
  'clientMsgId': 'client-$id',
  'conversationId': 'c1',
  'senderId': 'user-1',
  'type': type,
  'body': body,
  'createdAt': '2026-07-31T12:00:00Z',
  'conversationSeq': 1,
};
