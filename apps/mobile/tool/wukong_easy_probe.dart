import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:linli_im/im/wukong_gateway_macos_easy.dart';
import 'package:wukong_easy_sdk/wukong_easy_sdk.dart' as easy;

Future<void> main(List<String> arguments) async {
  final api = Uri.parse(
    arguments.isEmpty ? 'http://127.0.0.1:8080' : arguments.first,
  );
  final suffix = DateTime.now().microsecondsSinceEpoch.toString().substring(8);
  final alice = await _login(api, '139$suffix', 'Easy probe Alice');
  final bob = await _login(api, '138$suffix', 'Easy probe Bob');
  final aliceID = _map(alice['user'])['id']! as String;
  final bobID = _map(bob['user'])['id']! as String;
  final aliceToken = alice['accessToken']! as String;
  final bobToken = bob['accessToken']! as String;
  final aliceSession = _map(alice['imSession']);
  final bobSession = _map(bob['imSession']);
  if (aliceSession['sdk'] != 'wukong_easy_sdk' ||
      bobSession['sdk'] != 'wukong_easy_sdk' ||
      bobSession['deviceFlag'] != 2) {
    throw StateError('business API did not issue a macOS Easy SDK session');
  }

  final request = await _json(
    api,
    'POST',
    '/v2/contacts/requests',
    token: aliceToken,
    body: {'userId': bobID, 'message': 'macOS Easy SDK probe'},
  );
  final requestID = request['id']?.toString() ?? '';
  if (requestID.isEmpty) throw StateError('friend request id is missing');
  await _json(
    api,
    'POST',
    '/v2/contacts/requests/$requestID/accept',
    token: bobToken,
    body: const {},
  );
  final direct = await _json(
    api,
    'POST',
    '/v2/channels/direct',
    token: aliceToken,
    body: {'userId': bobID},
  );
  final conversationID = direct['id']?.toString() ?? '';
  if (conversationID.isEmpty) {
    throw StateError('direct conversation id is missing');
  }

  final sdk = easy.WuKongEasySDK.getInstance();
  final received = Completer<easy.Message>();
  final eventReceived = Completer<easy.EventNotification>();
  sdk.addEventListener<easy.Message>(easy.WuKongEvent.message, (message) {
    final payload = decodeWukongEasyPayload(message.payload);
    if (payload['content'] == 'server to Easy SDK') {
      if (!received.isCompleted) received.complete(message);
    }
  });
  sdk.addEventListener<easy.EventNotification>(easy.WuKongEvent.customEvent, (
    event,
  ) {
    final data = decodeWukongEasyPayload(event.data);
    if (event.type == 'stream.delta' &&
        jsonEncode(data).contains('EASY_CUSTOM_EVENT_PASS') &&
        !eventReceived.isCompleted) {
      eventReceived.complete(event);
    }
  });
  await sdk.init(
    easy.WuKongConfig(
      serverUrl: bobSession['wsUrl']! as String,
      uid: bobID,
      token: bobSession['token']! as String,
      deviceId: 'easy-probe-$suffix',
      deviceFlag: easy.WuKongDeviceFlag.pc,
    ),
  );
  await sdk.connect().timeout(const Duration(seconds: 10));
  try {
    final serverClientNo = 'easy-server-$suffix';
    await _json(
      api,
      'POST',
      '/v2/messages/conversations/$conversationID/send',
      token: aliceToken,
      body: {
        'clientMsgId': serverClientNo,
        'type': 'text',
        'body': {'text': 'server to Easy SDK'},
      },
    );
    final incoming = await received.future.timeout(const Duration(seconds: 10));
    final incomingPayload = decodeWukongEasyPayload(incoming.payload);
    if (incoming.fromUid != aliceID ||
        incoming.channelType.value != 1 ||
        incomingPayload['content'] != 'server to Easy SDK') {
      throw StateError('received Easy SDK message does not match the sender');
    }

    final clientMsgNo = 'easy-client-$suffix';
    final expiresAt = DateTime.now().toUtc().add(const Duration(minutes: 2));
    final send = await sdk
        .send(
          channelId: aliceID,
          channelType: easy.WuKongChannelType.person,
          clientMsgNo: clientMsgNo,
          payload: {
            'type': 1,
            'content': 'Easy SDK to server',
            'expiresAt': expiresAt.toIso8601String(),
          },
        )
        .timeout(const Duration(seconds: 10));
    if (send.reasonCode.value != 1 ||
        send.messageId.isEmpty ||
        send.messageSeq <= 0) {
      throw StateError('Easy SDK send ACK was rejected: $send');
    }

    final streamClientNo = 'easy-stream-$suffix';
    await _json(
      api,
      'POST',
      '/v2/messages/conversations/$conversationID/streams',
      token: aliceToken,
      body: {'clientMsgNo': streamClientNo, 'initialText': ''},
    );
    await _appendStreamEvent(
      api,
      aliceToken,
      conversationID,
      streamClientNo,
      eventType: 'stream.delta',
      eventID: '$streamClientNo-delta',
      payload: const {'kind': 'text', 'delta': 'EASY_CUSTOM_EVENT_PASS'},
    );
    final customEvent = await eventReceived.future.timeout(
      const Duration(seconds: 10),
    );
    await _appendStreamEvent(
      api,
      aliceToken,
      conversationID,
      streamClientNo,
      eventType: 'stream.close',
      eventID: '$streamClientNo-close',
      payload: const {'end_reason': 0},
    );
    await _appendStreamEvent(
      api,
      aliceToken,
      conversationID,
      streamClientNo,
      eventType: 'stream.finish',
      eventID: '$streamClientNo-finish',
      payload: const {},
    );

    stdout.writeln(
      jsonEncode({
        'ok': true,
        'sdk': bobSession['sdk'],
        'deviceFlag': bobSession['deviceFlag'],
        'wss': bobSession['wsUrl'],
        'receivedMessageId': incoming.messageId,
        'sentMessageId': send.messageId,
        'sentMessageSeq': send.messageSeq,
        'customEventType': customEvent.type,
        'customEventId': customEvent.id,
        'portableExpiry': expiresAt.toIso8601String(),
      }),
    );
  } finally {
    sdk.dispose();
  }
}

Future<Map<String, Object?>> _appendStreamEvent(
  Uri api,
  String token,
  String conversationID,
  String clientMsgNo, {
  required String eventType,
  required String eventID,
  required Map<String, Object?> payload,
}) async {
  Object? lastError;
  for (var attempt = 0; attempt < 50; attempt += 1) {
    try {
      return await _json(
        api,
        'POST',
        '/v2/messages/conversations/$conversationID/streams/$clientMsgNo/events',
        token: token,
        body: {
          'eventId': eventID,
          'eventType': eventType,
          'eventKey': 'main',
          'payload': payload,
        },
      );
    } on _ProbeHttpException catch (error) {
      lastError = error;
      if (error.statusCode != 404) rethrow;
      await Future<void>.delayed(const Duration(milliseconds: 100));
    }
  }
  throw StateError('stream anchor was not indexed: $lastError');
}

Future<Map<String, Object?>> _login(Uri api, String phone, String name) =>
    _json(
      api,
      'POST',
      '/v2/auth/login',
      headers: const {'X-Client-Platform': 'macos'},
      body: {'phone': phone, 'code': '123456', 'name': name},
    );

Future<Map<String, Object?>> _json(
  Uri api,
  String method,
  String path, {
  String? token,
  Map<String, String> headers = const {},
  Object? body,
}) async {
  final request = http.Request(method, api.resolve(path));
  request.headers.addAll({
    'Accept': 'application/json',
    'Content-Type': 'application/json',
    ...headers,
    if (token != null) 'Authorization': 'Bearer $token',
  });
  if (body != null) request.body = jsonEncode(body);
  final response = await request.send().timeout(const Duration(seconds: 10));
  final text = await response.stream.bytesToString();
  if (response.statusCode < 200 || response.statusCode >= 300) {
    throw _ProbeHttpException(
      response.statusCode,
      '$method $path returned ${response.statusCode}: $text',
    );
  }
  if (text.trim().isEmpty) return {};
  return _map(jsonDecode(text));
}

Map<String, Object?> _map(Object? value) => value is Map
    ? value.map((key, item) => MapEntry(key.toString(), item))
    : <String, Object?>{};

final class _ProbeHttpException implements Exception {
  const _ProbeHttpException(this.statusCode, this.message);

  final int statusCode;
  final String message;

  @override
  String toString() => message;
}
