import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:linli_im/data/live_repository.dart';

void main() {
  test('通话配置只接受 LiveKit v2 能力契约', () async {
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      client: MockClient((request) async {
        expect(request.url.path, '/v2/calls/config');
        return http.Response(
          jsonEncode({
            'data': {
              'provider': 'livekit',
              'url': 'wss://rtc.example.test/rtc',
              'inviteTimeoutSeconds': 30,
              'tokenTtlSeconds': 300,
              'maxParticipants': 9,
              'supportsScreenShare': true,
            },
          }),
          200,
        );
      }),
    );

    final config = await repository.callConfiguration();

    expect(config.provider, 'livekit');
    expect(config.url, 'wss://rtc.example.test/rtc');
    expect(config.inviteTimeout, const Duration(seconds: 30));
    expect(config.tokenTtl, const Duration(minutes: 5));
    expect(config.maxParticipants, 9);
    expect(config.supportsScreenShare, isTrue);
    await repository.close();
  });

  test('发起通话使用 v2 幂等 callId 和正确媒体类型', () async {
    late Map<String, Object?> body;
    late String path;
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      client: MockClient((request) async {
        path = request.url.path;
        body = jsonDecode(request.body) as Map<String, Object?>;
        return http.Response(
          jsonEncode({
            'data': {
              'call': _callJson(id: 'call-fixed', mediaType: 'video'),
              'duplicate': false,
            },
          }),
          201,
        );
      }),
    );

    final session = await repository.inviteCall(
      callId: 'call-fixed',
      conversationId: 'conversation-1',
      calleeUserId: 'peer',
      mediaType: CallMediaType.video,
    );

    expect(path, '/v2/calls/invite');
    expect(body['callId'], 'call-fixed');
    expect(body['mediaType'], 'video');
    expect(body['calleeUserId'], 'peer');
    expect(session.mediaType, CallMediaType.video);
    await repository.close();
  });

  test('群通话邀请不伪造单一 callee，并解析参与者状态', () async {
    late Map<String, Object?> body;
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      client: MockClient((request) async {
        body = jsonDecode(request.body) as Map<String, Object?>;
        final now = DateTime.utc(2026, 8, 11, 12);
        return http.Response(
          jsonEncode({
            'data': {
              'call': {
                'id': 'group-call',
                'conversationId': 'group-1',
                'kind': 'group',
                'callerId': 'me',
                'participantIds': ['me', 'peer', 'third'],
                'joinedUserIds': ['me'],
                'declinedUserIds': <String>[],
                'leftUserIds': <String>[],
                'mediaType': 'video',
                'status': 'invited',
                'invitedAt': now.toIso8601String(),
                'expiresAt': now
                    .add(const Duration(seconds: 30))
                    .toIso8601String(),
              },
            },
          }),
          201,
        );
      }),
    );

    final session = await repository.inviteCall(
      callId: 'group-call',
      conversationId: 'group-1',
      mediaType: CallMediaType.video,
    );

    expect(body.containsKey('calleeUserId'), isFalse);
    expect(session.isGroup, isTrue);
    expect(session.participantIds, ['me', 'peer', 'third']);
    expect(session.hasJoined('me'), isTrue);
    expect(session.hasJoined('peer'), isFalse);
    await repository.close();
  });

  test('入会凭证通过 v2 token 接口获取并严格解析', () async {
    final expiresAt = DateTime.now().toUtc().add(const Duration(minutes: 5));
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      client: MockClient((request) async {
        expect(request.method, 'POST');
        expect(request.url.path, '/v2/calls/call-fixed/token');
        return http.Response(
          jsonEncode({
            'data': {
              'session': {
                'url': 'wss://rtc.example.test/rtc',
                'roomName': 'call_call-fixed',
                'token': 'short-lived-jwt',
                'expiresAt': expiresAt.toIso8601String(),
              },
            },
          }),
          200,
        );
      }),
    );

    final session = await repository.joinCall('call-fixed');

    expect(session.roomName, 'call_call-fixed');
    expect(session.token, 'short-lived-jwt');
    expect(session.expiresAt.toUtc(), expiresAt);
    await repository.close();
  });
}

Map<String, Object?> _callJson({
  required String id,
  String mediaType = 'audio',
}) {
  final now = DateTime.utc(2026, 7, 31, 12);
  return {
    'id': id,
    'conversationId': 'conversation-1',
    'callerId': 'me',
    'calleeId': 'peer',
    'mediaType': mediaType,
    'status': 'invited',
    'invitedAt': now.toIso8601String(),
    'expiresAt': now.add(const Duration(seconds: 30)).toIso8601String(),
  };
}
