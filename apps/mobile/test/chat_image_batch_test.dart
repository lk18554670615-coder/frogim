import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as image_lib;
import 'package:linli_im/core/chat_image_batch.dart';
import 'package:linli_im/core/models.dart';

void main() {
  test('静态图片压缩为最长边 2400 的 JPEG', () async {
    final source = image_lib.Image(width: 2500, height: 1250);
    final encoded = await encodeChatImage(
      Uint8List.fromList(image_lib.encodePng(source)),
    );

    expect(encoded.mimeType, 'image/jpeg');
    expect(encoded.extension, '.jpg');
    expect(encoded.width, 2400);
    expect(encoded.height, 1200);
    expect(encoded.bytes.take(3), orderedEquals([0xff, 0xd8, 0xff]));
  });

  test('GIF 根据实际内容识别并保留原始动画字节', () async {
    final gif = Uint8List.fromList(
      image_lib.encodeGif(image_lib.Image(width: 24, height: 18)),
    );
    final encoded = await encodeChatImage(gif);

    expect(encoded.mimeType, 'image/gif');
    expect(encoded.extension, '.gif');
    expect(encoded.width, 24);
    expect(encoded.height, 18);
    expect(encoded.bytes, orderedEquals(gif));
  });

  test('常见图片根据实际文件签名识别', () {
    expect(
      hasSupportedChatImageSignature(Uint8List.fromList([0xff, 0xd8, 0xff])),
      isTrue,
    );
    expect(
      hasSupportedChatImageSignature(
        Uint8List.fromList([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
      ),
      isTrue,
    );
    expect(
      hasSupportedChatImageSignature(
        Uint8List.fromList([
          0x52,
          0x49,
          0x46,
          0x46,
          0,
          0,
          0,
          0,
          0x57,
          0x45,
          0x42,
          0x50,
        ]),
      ),
      isTrue,
    );
    expect(
      hasSupportedChatImageSignature(Uint8List.fromList([1, 2, 3, 4])),
      isFalse,
    );
  });

  test('损坏图片返回明确错误', () async {
    await expectLater(
      encodeChatImage(Uint8List.fromList([1, 2, 3, 4])),
      throwsA(
        isA<FormatException>().having(
          (error) => error.message,
          'message',
          contains('不受支持'),
        ),
      ),
    );
  });

  test('批量图片按选择顺序入队且同时上传不超过 3 张', () async {
    final sources = [
      for (var index = 0; index < 7; index++)
        ChatImageSource(
          name: '$index.png',
          readBytes: () async => Uint8List(1),
        ),
    ];
    final queued = <String>[];
    var active = 0;
    var maximumActive = 0;

    final result = await sendChatImageBatch(
      sources: sources,
      maxBytes: 1024,
      prepare: (source, _) async => _upload(source.name),
      send: (upload, onQueued) async {
        active++;
        if (active > maximumActive) maximumActive = active;
        onQueued(_message(upload.fileName, MessageStatus.sending));
        final index = int.parse(upload.fileName.split('.').first);
        await Future<void>.delayed(Duration(milliseconds: 10 + (6 - index)));
        active--;
        return _message(
          upload.fileName,
          index == 3 ? MessageStatus.failed : MessageStatus.sent,
        );
      },
      onQueued: (message) => queued.add(message.fileName!),
    );

    expect(queued, [for (var index = 0; index < 7; index++) '$index.png']);
    expect(maximumActive, 3);
    expect(result.succeeded, 6);
    expect(result.failed, 1);
    expect(result.failures.single.retryAvailable, isTrue);
    expect(result.notStarted, 0);
  });

  test('单张准备失败不阻止后续图片发送', () async {
    final sources = [
      for (final name in ['first.png', 'broken.png', 'last.png'])
        ChatImageSource(name: name, readBytes: () async => Uint8List(1)),
    ];
    final sent = <String>[];
    final result = await sendChatImageBatch(
      sources: sources,
      maxBytes: 1024,
      prepare: (source, _) async {
        if (source.name == 'broken.png') {
          throw const FormatException('图片已损坏');
        }
        return _upload(source.name);
      },
      send: (upload, onQueued) async {
        sent.add(upload.fileName);
        final message = _message(upload.fileName, MessageStatus.sent);
        onQueued(message);
        return message;
      },
    );

    expect(sent, ['first.png', 'last.png']);
    expect(result.succeeded, 2);
    expect(result.failed, 1);
    expect(result.failures.single.fileName, 'broken.png');
    expect(result.failures.single.retryAvailable, isFalse);
  });

  test('页面失效后停止尚未开始的图片', () async {
    var current = true;
    final result = await sendChatImageBatch(
      sources: [
        for (final name in ['first.png', 'second.png', 'third.png'])
          ChatImageSource(name: name, readBytes: () async => Uint8List(1)),
      ],
      maxBytes: 1024,
      shouldContinue: () => current,
      prepare: (source, _) async => _upload(source.name),
      send: (upload, onQueued) async {
        final message = _message(upload.fileName, MessageStatus.sent);
        onQueued(message);
        return message;
      },
      onQueued: (_) => current = false,
    );

    expect(result.succeeded, 1);
    expect(result.notStarted, 2);
  });

  test('取消选择对应空批次且不产生消息', () async {
    var sendCount = 0;
    final result = await sendChatImageBatch(
      sources: const [],
      maxBytes: 1024,
      send: (upload, onQueued) async {
        sendCount++;
        return _message(upload.fileName, MessageStatus.sent);
      },
    );

    expect(sendCount, 0);
    expect(result.succeeded, 0);
    expect(result.failed, 0);
    expect(result.notStarted, 0);
  });

  for (final count in [2, 9]) {
    test('$count 张图片全部按选择顺序入队', () async {
      final queued = <String>[];
      final result = await sendChatImageBatch(
        sources: [
          for (var index = 0; index < count; index++)
            ChatImageSource(
              name: '$index.png',
              readBytes: () async => Uint8List(1),
            ),
        ],
        maxBytes: 1024,
        prepare: (source, _) async => _upload(source.name),
        send: (upload, onQueued) async {
          final message = _message(upload.fileName, MessageStatus.sent);
          onQueued(message);
          return message;
        },
        onQueued: (message) => queued.add(message.fileName!),
      );

      expect(queued, [
        for (var index = 0; index < count; index++) '$index.png',
      ]);
      expect(result.succeeded, count);
      expect(result.failed, 0);
    });
  }

  test('防御性拒绝超过 9 张图片且不静默截断', () async {
    final sources = [
      for (var index = 0; index < 10; index++)
        ChatImageSource(
          name: '$index.png',
          readBytes: () async => Uint8List(1),
        ),
    ];
    await expectLater(
      sendChatImageBatch(
        sources: sources,
        maxBytes: 1024,
        prepare: (source, _) async => _upload(source.name),
        send: (upload, onQueued) async =>
            _message(upload.fileName, MessageStatus.sent),
      ),
      throwsA(isA<FormatException>()),
    );
  });
}

MediaUpload _upload(String name) => MediaUpload(
  bytes: Uint8List.fromList([1]),
  fileName: name,
  mimeType: 'image/jpeg',
  kind: MessageContentKind.image,
  width: 1,
  height: 1,
);

ChatMessage _message(String fileName, MessageStatus status) => ChatMessage(
  id: 'message-$fileName',
  conversationId: 'conversation',
  senderId: 'me',
  senderName: '我',
  text: '[图片]',
  sentAt: DateTime(2026),
  isMine: true,
  kind: MessageContentKind.image,
  fileName: fileName,
  status: status,
  sendError: status == MessageStatus.failed ? '服务端拒绝' : null,
);
