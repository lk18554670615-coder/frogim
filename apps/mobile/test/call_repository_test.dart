import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:linli_im/data/live_repository.dart';

void main() {
  test('通话配置解析 STUN/TURN 且凭据仅保留在内存响应', () async {
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      wsUrl: 'wss://im.example.test/v1/ws',
      client: MockClient((request) async {
        expect(request.url.path, '/v1/calls/config');
        return http.Response(
          jsonEncode({
            'data': {
              'iceServers': [
                {
                  'urls': ['stun:stun.example.test:3478'],
                },
                {
                  'urls': ['turn:turn.example.test:3478?transport=udp'],
                  'username': 'temporary-user',
                  'credential': 'temporary-password',
                },
              ],
              'inviteTimeoutSeconds': 30,
            },
          }),
          200,
        );
      }),
    );

    final config = await repository.callConfiguration();

    expect(config.iceServers, hasLength(2));
    expect(config.iceServers.last.username, 'temporary-user');
    expect(config.inviteTimeout, const Duration(seconds: 30));
    await repository.close();
  });

  test('发起通话使用幂等 callId 和正确媒体类型', () async {
    late Map<String, Object?> body;
    final repository = LiveImRepository(
      apiBaseUrl: 'https://im.example.test',
      wsUrl: 'wss://im.example.test/v1/ws',
      client: MockClient((request) async {
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

    expect(body['callId'], 'call-fixed');
    expect(body['mediaType'], 'video');
    expect(session.mediaType, CallMediaType.video);
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
