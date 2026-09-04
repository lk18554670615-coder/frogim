import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
// Already supplied by video_player; no production dependency is introduced.
// ignore: depend_on_referenced_packages
import 'package:video_player_platform_interface/video_player_platform_interface.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/media_access.dart';
import 'package:linli_im/core/video_preparation_types.dart';
import 'package:linli_im/im/message_mapper.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:linli_im/ui/widgets/video_message_card.dart';
import 'package:linli_im/ui/widgets/media_send_widgets.dart';

ChatMessage video({
  String? source = 'https://example.com/expired.mp4',
  String? mediaId = 'video',
}) => ChatMessage(
  id: 'video-message',
  conversationId: 'c',
  senderId: 'u',
  senderName: 'User',
  text: '[视频]',
  sentAt: DateTime.utc(2026, 9, 3),
  isMine: true,
  kind: MessageContentKind.video,
  mediaId: mediaId,
  mediaUrl: source,
  fileName: 'a-long-video-name.mp4',
  durationSeconds: 5,
  mediaWidth: 640,
  mediaHeight: 360,
  coverMediaId: 'cover',
  coverUrl: 'https://example.com/cover.jpg',
);

class FakeVideoPlatform extends VideoPlayerPlatform {
  final sources = <String>[];
  final headers = <Map<String, String>>[];
  int plays = 0, pauses = 0, disposed = 0;
  bool rejectAutoplay = false, invalid = false;
  @override
  Future<void> init() async {}
  @override
  Future<int?> createWithOptions(VideoCreationOptions options) async {
    sources.add(options.dataSource.uri ?? '');
    headers.add(options.dataSource.httpHeaders);
    if (invalid) throw PlatformException(code: 'MEDIA_ERR_SRC_NOT_SUPPORTED');
    return sources.length;
  }

  @override
  Stream<VideoEvent> videoEventsFor(int playerId) {
    late StreamController<VideoEvent> events;
    events = StreamController<VideoEvent>(
      onListen: () {
        events.add(
          VideoEvent(
            eventType: VideoEventType.initialized,
            duration: const Duration(seconds: 5),
            size: const Size(640, 360),
          ),
        );
      },
    );
    return events.stream;
  }

  @override
  Future<void> dispose(int playerId) async {
    disposed++;
  }

  @override
  Future<void> setLooping(int playerId, bool looping) async {}
  @override
  Future<void> setVolume(int playerId, double volume) async {}
  @override
  Future<void> setPlaybackSpeed(int playerId, double speed) async {}
  @override
  Future<void> play(int playerId) async {
    plays++;
    if (rejectAutoplay) throw PlatformException(code: 'NotAllowedError');
  }

  @override
  Future<void> pause(int playerId) async {
    pauses++;
  }

  @override
  Future<void> seekTo(int playerId, Duration position) async {}
  @override
  Future<Duration> getPosition(int playerId) async => Duration.zero;
  @override
  Widget buildViewWithOptions(VideoViewOptions options) =>
      const ColoredBox(color: Colors.green);
}

class DeferredVideoFile extends XFile {
  DeferredVideoFile(this.reader) : super('test.mp4');
  final Future<Uint8List> Function() reader;
  @override
  Future<Uint8List> readAsBytes() => reader();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late FakeVideoPlatform platform;
  setUp(() {
    platform = FakeVideoPlatform();
    VideoPlayerPlatform.instance = platform;
  });
  test('official cover/second, legacy duration, cache and file semantics', () {
    final mapper = MessageMapper();
    final m = video();
    final payload = mapper
        .toOutgoing(m, channel: const WukongChannel(id: 'peer', type: 1))
        .payload;
    expect(payload['type'], 5);
    expect(payload['coverMediaId'], 'cover');
    expect(payload['second'], 5);
    expect(payload['duration'], 5);
    expect(ChatMessage.fromJson(m.toJson()).copyWith().coverUrl, m.coverUrl);
    final wire = WukongMessage(
      messageId: '1',
      messageSeq: 1,
      clientMsgNo: 'client',
      clientSeq: 1,
      fromUid: 'u',
      state: WukongMessageState.sent,
      channel: const WukongChannel(id: 'peer', type: 1),
      timestamp: m.sentAt,
      payload: payload,
    );
    expect(
      mapper
          .toChatMessage(wire, currentUserId: 'u', conversationId: 'c')
          .durationSeconds,
      5,
    );
    expect(
      mapper
          .toChatMessage(
            wire.copyWith(payload: {'type': 5, 'duration': 8}),
            currentUserId: 'u',
            conversationId: 'c',
          )
          .durationSeconds,
      8,
    );
    expect(
      mapper
          .toOutgoing(
            m.copyWith(kind: MessageContentKind.file),
            channel: wire.channel,
          )
          .payload['type'],
      8,
    );
    final local = mapper
        .toOutgoing(
          m.copyWith(mediaUrl: 'blob:video', coverUrl: 'blob:cover'),
          channel: wire.channel,
        )
        .payload;
    expect(local.containsKey('url'), false);
    expect(local.containsKey('cover'), false);
  });
  test('formats and preparation limits stay explicit', () {
    expect(videoMimeType('VIDEO.WEBM'), 'video/webm');
    expect(videoMimeType('clip.mov'), 'video/quicktime');
    expect(videoMimeType('unknown.xyz'), 'application/octet-stream');
    expect(
      () => validatePreparedVideo(
        byteLength: 1,
        maxBytes: 100,
        durationSeconds: 301,
      ),
      throwsFormatException,
    );
    expect(
      () => validatePreparedVideo(
        byteLength: 101,
        maxBytes: 100,
        durationSeconds: 5,
      ),
      throwsFormatException,
    );
    expect(
      () => validatePreparedVideo(
        byteLength: 0,
        maxBytes: 100,
        durationSeconds: 5,
      ),
      throwsFormatException,
    );
  });
  testWidgets(
    'upload completion is visibly distinct from message acknowledgement',
    (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: VideoSendProgress(progress: .5, clientMessageId: 'video'),
          ),
        ),
      );
      expect(find.text('上传 50%'), findsOneWidget);
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: VideoSendProgress(progress: 1, clientMessageId: 'video'),
          ),
        ),
      );
      expect(find.text('等待消息确认'), findsOneWidget);
      expect(find.text('已发送'), findsNothing);
    },
  );
  testWidgets('click opens in-app player with refreshed URL and autoplay', (
    tester,
  ) async {
    var resolutions = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: VideoMessageCard(
            message: video().copyWith(coverUrl: ''),
            color: Colors.green,
            resolveMedia: (m) async {
              resolutions++;
              return m.copyWith(mediaUrl: 'https://example.com/fresh.mp4');
            },
          ),
        ),
      ),
    );
    await tester.tap(find.byKey(const Key('play-video-video-message')));
    await tester.pumpAndSettle();
    expect(find.byType(VideoPlayerScreen), findsOneWidget);
    expect(resolutions, 1);
    expect(platform.sources.single, 'https://example.com/fresh.mp4');
    expect(platform.plays, greaterThan(0));
    await tester.pageBack();
    await tester.pumpAndSettle();
    await tester.pump(const Duration(seconds: 1));
    expect(find.byType(VideoPlayerScreen), findsNothing);
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 20)),
    );
    expect(platform.disposed, 1);
  });
  testWidgets(
    'fixed video and poster use media credentials without URL lookup',
    (tester) async {
      final owner = Object();
      mediaAccess.configure(
        owner: owner,
        apiBaseUrl: 'https://example.com',
        userId: 'u',
        token: 'media-only',
      );
      addTearDown(() => mediaAccess.clear(owner));
      var resolutions = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: VideoMessageCard(
              message: video(),
              color: Colors.green,
              resolveMedia: (m) async {
                resolutions++;
                return m;
              },
            ),
          ),
        ),
      );
      final image = tester.widget<Image>(find.byType(Image).first);
      final provider = image.image as NetworkImage;
      expect(provider.url, contains('/v2/media/video/cover?viewer=u'));
      expect(provider.headers, {'Authorization': 'Media media-only'});
      await tester.tap(find.byKey(const Key('play-video-video-message')));
      await tester.pumpAndSettle();
      expect(
        platform.sources.single,
        contains('/v2/media/video/content?viewer=u'),
      );
      expect(platform.headers.single, {'Authorization': 'Media media-only'});
      expect(resolutions, 0);
      await tester.pageBack();
      await tester.pumpAndSettle();
    },
  );
  testWidgets('expired source retries exactly once then exposes retry', (
    tester,
  ) async {
    var count = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: VideoPlayerScreen(
          source: '',
          title: 'video',
          resolveSource: () async {
            count++;
            throw Exception('network expired');
          },
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(count, 2);
    expect(find.text('重试'), findsOneWidget);
    await tester.tap(find.text('重试'));
    await tester.pumpAndSettle();
    expect(count, 4);
  });
  testWidgets('autoplay refusal is not a decode failure', (tester) async {
    platform.rejectAutoplay = true;
    await tester.pumpWidget(
      const MaterialApp(
        home: VideoPlayerScreen(
          source: 'https://example.com/video.mp4',
          title: 'video',
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('点击播放按钮开始播放'), findsOneWidget);
    expect(find.textContaining('格式不支持'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
  });
  testWidgets(
    'closing during URL lookup cancels timeout and ignores late response',
    (tester) async {
      final lookup = Completer<String>();
      await tester.pumpWidget(
        MaterialApp(
          home: VideoPlayerScreen(
            source: '',
            title: 'video',
            resolveSource: () => lookup.future,
          ),
        ),
      );
      await tester.pump();
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      lookup.complete('https://example.com/late.mp4');
      await tester.pump();
      expect(platform.sources, isEmpty);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets('unsupported preview offers explicit original file fallback', (
    tester,
  ) async {
    platform.invalid = true;
    VideoPreviewResult? selected;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await showVideoSendPreview(
                context,
                source: 'https://example.com/video.mov',
                title: 'clip',
              );
            },
            child: const Text('choose'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('choose'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('video-fallback-file')));
    await tester.pumpAndSettle();
    expect(selected?.asFile, true);
  });
  testWidgets('portrait/narrow/dark/large-text card has no overflow', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.dark(),
        home: Scaffold(
          body: MediaQuery(
            data: const MediaQueryData(textScaler: TextScaler.linear(2)),
            child: Center(
              child: SizedBox(
                width: 190,
                child: VideoMessageCard(
                  message: video().copyWith(
                    coverUrl: '',
                    mediaWidth: 360,
                    mediaHeight: 640,
                  ),
                  color: Colors.white,
                ),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });
  testWidgets(
    'read failure retains selection and explicit file retry is single-flight',
    (tester) async {
      var reads = 0;
      final bytes = Completer<Uint8List>();
      final file = DeferredVideoFile(() {
        reads++;
        return reads == 1
            ? Future.error(const FormatException('读取失败'))
            : bytes.future;
      });
      MediaUpload? selected;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => TextButton(
              onPressed: () async {
                selected = await prepareVideoUploadWithDialog(
                  context,
                  file: file,
                  maxBytes: 100,
                  previewDurationSeconds: 5,
                  asFile: true,
                );
              },
              child: const Text('choose'),
            ),
          ),
        ),
      );
      await tester.tap(find.text('choose'));
      await tester.pumpAndSettle();
      expect(find.text('读取失败'), findsOneWidget);
      await tester.tap(find.text('重试'));
      await tester.tap(find.text('重试'));
      await tester.pump();
      expect(reads, 2);
      bytes.complete(Uint8List.fromList([1, 2, 3]));
      await tester.pumpAndSettle();
      expect(selected?.kind, MessageContentKind.file);
    },
  );
  testWidgets('cancel processing does not produce a late video upload', (
    tester,
  ) async {
    final bytes = Completer<Uint8List>();
    MediaUpload? selected;
    var closed = false;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () async {
              selected = await prepareVideoUploadWithDialog(
                context,
                file: DeferredVideoFile(() => bytes.future),
                maxBytes: 100,
                previewDurationSeconds: 5,
              );
              closed = true;
            },
            child: const Text('choose'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('choose'));
    await tester.pump();
    await tester.tap(find.byKey(const Key('cancel-video-compression')));
    await tester.pumpAndSettle();
    bytes.complete(Uint8List.fromList([1, 2, 3]));
    await tester.pumpAndSettle();
    expect(closed, true);
    expect(selected, isNull);
    expect(tester.takeException(), isNull);
  });
}
