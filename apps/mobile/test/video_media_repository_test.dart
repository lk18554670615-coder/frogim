import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/media_access.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/business_repository.dart';
import 'support/fake_wukong_gateway.dart';

http.Response json(Map<String, Object?> value, [int status = 200]) =>
    http.Response(
      jsonEncode({'data': value}),
      status,
      headers: {'content-type': 'application/json'},
    );

class Fixture {
  final gateway = FakeWukongGateway();
  final store = SecureLocalStore();
  late final LiveImRepository repository;
  final requests = <http.Request>[];
  bool failCover = false, failBody = false, failBind = false;
  Fixture({bool fixedMedia = false}) {
    repository = LiveImRepository(
      store: store,
      wukongGateway: gateway,
      apiBaseUrl: 'https://api.example.com',
      client: MockClient((r) async {
        requests.add(r);
        final path = r.url.path;
        if (path == '/v2/im/datasource/message-extras') return json({'items': []});
        if (path == '/v2/auth/login') {
          return json({
            'accessToken': 'access',
            'refreshToken': 'refresh',
            if (fixedMedia) 'mediaAccessToken': 'media-session',
            'user': {'id': 'me', 'name': 'Me', 'handle': 'me'},
            'imSession': {
              'uid': 'me',
              'token': 'wk1_test',
              'deviceFlag': 2,
              'deviceLevel': 1,
              'tcpUrl': 'tcp://im.example.com:5100',
              'wsUrl': 'wss://im.example.com/ws',
              'sdk': 'wukongimfluttersdk',
              'issuedAt': '2026-09-03T00:00:00Z',
            },
          });
        }
        if (path == '/v2/channels/conversations') {
          return json({
            'items': [
              {
                'conversation': {
                  'id': 'c1',
                  'type': 'direct',
                  'title': 'Friend',
                  'updatedAt': '2026-09-03T00:00:00Z',
                },
                'members': [
                  {'id': 'me', 'name': 'Me'},
                  {'id': 'peer', 'name': 'Friend'},
                ],
              },
            ],
          });
        }
        if (path == '/v2/im/datasource/conversations') {
          return json({'items': []});
        }
        if (path == '/v2/media/presign') {
          final cover = (jsonDecode(r.body) as Map)['mime'] == 'image/jpeg';
          final id = cover ? 'cover' : 'body';
          return json({
            'mediaId': id,
            'uploadUrl': 'https://upload.example.com/$id',
            'expiresAt': '2099-01-01T00:00:00Z',
            'headers': <String, Object?>{},
          });
        }
        if (r.url.host == 'upload.example.com') {
          final fail = path == '/cover' ? failCover : failBody;
          return http.Response('', fail ? 503 : 200);
        }
        if (path.endsWith('/complete')) return json({});
        if (path.endsWith('/bind')) {
          if (failBind) {
            return http.Response(
              '{"error":{"code":"TEMPORARY","message":"retry bind"}}',
              503,
            );
          }
          return json({'url': 'https://cdn.example.com/video.webm'});
        }
        if (path.endsWith('/url')) {
          return json({
            'mediaId': 'body',
            'url': 'https://cdn.example.com/fresh.webm',
            if (!failCover) 'coverMediaId': 'cover',
            if (!failCover) 'cover': 'https://cdn.example.com/cover.jpg',
          });
        }
        return http.Response('{}', 404);
      }),
    );
  }
  ChatMessage get pending => ChatMessage(
    id: 'local-video',
    clientMessageId: 'stable-video',
    conversationId: 'c1',
    senderId: 'me',
    senderName: 'Me',
    text: '[视频]',
    sentAt: DateTime.now(),
    isMine: true,
    kind: MessageContentKind.video,
    status: MessageStatus.sending,
  );
  MediaUpload get upload => MediaUpload(
    bytes: Uint8List.fromList([1, 2, 3]),
    fileName: 'clip.webm',
    mimeType: 'video/webm',
    kind: MessageContentKind.video,
    localPath: 'blob:local',
    durationSeconds: 5,
    width: 640,
    height: 360,
    coverBytes: Uint8List.fromList([4, 5, 6]),
  );
  Future<void> login() =>
      repository.login('13800138000', '123456').then((_) {});
  int puts(String part) => requests
      .where(
        (r) =>
            r.method == 'PUT' &&
            r.url.host == 'upload.example.com' &&
            r.url.path == '/$part',
      )
      .length;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  test(
    'fixed media reads use stable IDs without fetching /url again',
    () async {
      final f = Fixture(fixedMedia: true);
      addTearDown(f.repository.close);
      await f.login();
      final previous = f.pending.copyWith(
        mediaId: 'body',
        mediaUrl: 'https://storage.example/expired',
      );
      final count = f.requests.length;
      final first = await f.repository.refreshMessageMedia(previous);
      final second = await f.repository.refreshMessageMedia(previous);
      expect(
        first.mediaUrl,
        'https://api.example.com/v2/media/body/content?viewer=me',
      );
      expect(second.mediaUrl, first.mediaUrl);
      expect(
        first.coverUrl,
        'https://api.example.com/v2/media/body/cover?viewer=me',
      );
    expect(f.requests.length, count);
    await f.repository.sendMedia(f.pending, f.upload);
    final wire = f.gateway.sentMessages.single.payload;
    expect(wire['url'], 'https://api.example.com/v2/media/body/content');
    expect(wire.toString(), isNot(contains('viewer=')));
    expect(wire.toString(), isNot(contains('media-session')));
    expect(f.requests.where((r) => r.url.path.endsWith('/url')), isEmpty);
      await f.repository.logout();
      expect(mediaAccess.headersFor(first.mediaUrl!), isEmpty);
    },
  );
  test(
    'video uploads linked JPEG and preserves real format and official fields',
    () async {
      final f = Fixture();
      addTearDown(f.repository.close);
      await f.login();
      final sent = await f.repository.sendMedia(f.pending, f.upload);
      final complete = f.requests.singleWhere(
        (r) => r.url.path == '/v2/media/body/complete',
      );
      expect(jsonDecode(complete.body)['coverMediaId'], 'cover');
      expect(f.puts('cover'), 1);
      expect(f.puts('body'), 1);
      final payload = f.gateway.sentMessages.single.payload;
      expect(payload['type'], 5);
      expect(payload['second'], 5);
      expect(payload['coverMediaId'], 'cover');
      expect(payload['mime'], 'video/webm');
      expect(payload.toString(), isNot(contains('blob:')));
      expect(sent.coverUrl, 'https://cdn.example.com/cover.jpg');
    },
  );
  test(
    'completed upload is reused after bind failure and recoverable without local file',
    () async {
      final f = Fixture();
      addTearDown(f.repository.close);
      await f.login();
      f.failBind = true;
      await expectLater(
        f.repository.sendMedia(f.pending, f.upload),
        throwsA(isA<BusinessApiException>()),
      );
      final recovered = await f.repository.refreshMessageMedia(f.pending);
      expect(recovered.mediaId, 'body');
      expect(recovered.mediaUrl, 'https://cdn.example.com/fresh.webm');
      f.failBind = false;
      await f.repository.sendMedia(f.pending, f.upload);
      expect(f.puts('cover'), 1);
      expect(f.puts('body'), 1);
      expect(f.gateway.sentMessages.single.clientMsgNo, 'stable-video');
    },
  );
  test(
    'interrupted body upload retries only body and retains stable identity',
    () async {
      final f = Fixture();
      addTearDown(f.repository.close);
      await f.login();
      f.failBody = true;
      await expectLater(
        f.repository.sendMedia(f.pending, f.upload),
        throwsA(isA<ImApiException>()),
      );
      f.failBody = false;
      await f.repository.sendMedia(f.pending, f.upload);
      expect(f.puts('cover'), 1);
      expect(f.puts('body'), 2);
      expect(f.gateway.sentMessages.length, 1);
    },
  );
  test(
    'cover failure does not prevent video and file entry remains type 8',
    () async {
      final f = Fixture();
      addTearDown(f.repository.close);
      await f.login();
      f.failCover = true;
      await f.repository.sendMedia(f.pending, f.upload);
      expect(f.gateway.sentMessages.single.payload['type'], 5);
      expect(
        f.gateway.sentMessages.single.payload.containsKey('coverMediaId'),
        false,
      );
      final file = MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'clip.mp4',
        mimeType: 'video/mp4',
        kind: MessageContentKind.file,
      );
      await f.repository.sendMedia(
        f.pending.copyWith(
          id: 'local-file',
          clientMessageId: 'file',
          kind: MessageContentKind.file,
        ),
        file,
      );
      expect(f.gateway.sentMessages.last.payload['type'], 8);
    },
  );
}
