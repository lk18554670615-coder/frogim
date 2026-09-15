import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:image/image.dart' as image_lib;

import 'local_media_path.dart';
import 'models.dart';

const maxChatImageSelection = 9;
const maxConcurrentChatImageUploads = 3;
const maxChatImageDimension = 2400;
const chatImageJpegQuality = 88;

class ChatImageSource {
  const ChatImageSource({required this.name, required this.readBytes});

  final String name;
  final Future<Uint8List> Function() readBytes;
}

class EncodedChatImage {
  const EncodedChatImage({
    required this.bytes,
    required this.mimeType,
    required this.extension,
    required this.width,
    required this.height,
  });

  final Uint8List bytes;
  final String mimeType;
  final String extension;
  final int width;
  final int height;
}

class ChatImageBatchFailure {
  const ChatImageBatchFailure({
    required this.fileName,
    required this.message,
    this.retryAvailable = false,
  });

  final String fileName;
  final String message;
  final bool retryAvailable;
}

class ChatImageBatchResult {
  const ChatImageBatchResult({
    required this.succeeded,
    required this.failures,
    required this.notStarted,
  });

  final int succeeded;
  final List<ChatImageBatchFailure> failures;
  final int notStarted;

  int get failed => failures.length;
}

typedef ChatImageSender =
    Future<ChatMessage> Function(
      MediaUpload upload,
      ValueChanged<ChatMessage> onQueued,
    );

typedef ChatImagePreparer =
    Future<MediaUpload> Function(ChatImageSource source, int maxBytes);

Future<MediaUpload> prepareChatImage(
  ChatImageSource source, {
  required int maxBytes,
}) async {
  final original = await source.readBytes().timeout(
    const Duration(seconds: 30),
  );
  if (original.isEmpty) throw const FormatException('图片内容为空');
  if (original.length > maxBytes) {
    throw FormatException('图片不能超过 ${maxBytes ~/ (1024 * 1024)} MB');
  }
  final encoded = await encodeChatImage(original);
  if (encoded.bytes.length > maxBytes) {
    throw FormatException('图片不能超过 ${maxBytes ~/ (1024 * 1024)} MB');
  }
  final localPath = await persistImageBytes(
    encoded.bytes,
    mime: encoded.mimeType,
    extension: encoded.extension,
  );
  return MediaUpload(
    bytes: encoded.bytes,
    fileName: _outputName(source.name, encoded.extension),
    mimeType: encoded.mimeType,
    kind: MessageContentKind.image,
    localPath: localPath,
    width: encoded.width,
    height: encoded.height,
  );
}

Future<EncodedChatImage> encodeChatImage(Uint8List bytes) async {
  if (isGifImageBytes(bytes)) {
    (int, int)? dimensions;
    try {
      dimensions = await compute(_decodeImageDimensions, bytes);
    } catch (_) {
      dimensions = null;
    }
    if (dimensions == null) throw const FormatException('无法读取 GIF 图片');
    return EncodedChatImage(
      bytes: bytes,
      mimeType: 'image/gif',
      extension: '.gif',
      width: dimensions.$1,
      height: dimensions.$2,
    );
  }
  (Uint8List, int, int)? encoded;
  try {
    encoded = await compute(_compressStaticImage, bytes);
  } catch (_) {
    encoded = null;
  }
  if (encoded == null) throw const FormatException('图片格式不受支持或文件已损坏');
  return EncodedChatImage(
    bytes: encoded.$1,
    mimeType: 'image/jpeg',
    extension: '.jpg',
    width: encoded.$2,
    height: encoded.$3,
  );
}

Future<ChatImageBatchResult> sendChatImageBatch({
  required List<ChatImageSource> sources,
  required int maxBytes,
  required ChatImageSender send,
  ValueChanged<ChatMessage>? onQueued,
  bool Function()? shouldContinue,
  int maxConcurrent = maxConcurrentChatImageUploads,
  ChatImagePreparer? prepare,
}) async {
  if (sources.length > maxChatImageSelection) {
    throw const FormatException('一次最多发送 9 张图片');
  }
  if (maxConcurrent < 1) {
    throw ArgumentError.value(maxConcurrent, 'maxConcurrent');
  }
  final failures = <ChatImageBatchFailure>[];
  final active = <_ActiveImageSend>[];
  var succeeded = 0;
  var started = 0;
  final prepareImage =
      prepare ??
      (source, bytesLimit) => prepareChatImage(source, maxBytes: bytesLimit);

  Future<void> collectCompleted({bool waitForOne = false}) async {
    if (active.isEmpty) return;
    if (waitForOne && !active.any((item) => item.completed)) {
      await Future.any(active.map((item) => item.future));
    }
    final completed = active.where((item) => item.completed).toList();
    for (final item in completed) {
      active.remove(item);
      final outcome = await item.future;
      final message = outcome.message;
      if (outcome.error != null) {
        failures.add(
          ChatImageBatchFailure(
            fileName: item.fileName,
            message: _friendlyImageError(outcome.error!),
            retryAvailable: item.queued,
          ),
        );
      } else if (message!.status == MessageStatus.failed) {
        failures.add(
          ChatImageBatchFailure(
            fileName: item.fileName,
            message: message.sendError ?? '发送失败，可在消息旁重试',
            retryAvailable: true,
          ),
        );
      } else {
        succeeded++;
      }
    }
  }

  for (final source in sources) {
    if (shouldContinue?.call() == false) break;
    while (active.length >= maxConcurrent) {
      await collectCompleted(waitForOne: true);
    }
    MediaUpload upload;
    try {
      upload = await prepareImage(source, maxBytes);
    } catch (error) {
      failures.add(
        ChatImageBatchFailure(
          fileName: source.name,
          message: _friendlyImageError(error),
        ),
      );
      started++;
      continue;
    }
    if (shouldContinue?.call() == false) break;
    started++;
    final queued = Completer<void>();
    final activeSend = _ActiveImageSend(fileName: source.name);
    Future<_BatchSendOutcome> future;
    try {
      future =
          send(upload, (message) {
            activeSend.queued = true;
            onQueued?.call(message);
            if (!queued.isCompleted) queued.complete();
          }).then(
            (message) => _BatchSendOutcome(message: message),
            onError: (Object error, StackTrace stack) =>
                _BatchSendOutcome(error: error),
          );
    } catch (error) {
      future = Future.value(_BatchSendOutcome(error: error));
    }
    activeSend.future = future;
    active.add(activeSend);
    unawaited(future.whenComplete(() => activeSend.completed = true));
    await Future.any([queued.future, future.then<void>((_) {})]);
    await collectCompleted();
  }

  while (active.isNotEmpty) {
    await collectCompleted(waitForOne: true);
  }
  return ChatImageBatchResult(
    succeeded: succeeded,
    failures: failures,
    notStarted: sources.length - started,
  );
}

class _ActiveImageSend {
  _ActiveImageSend({required this.fileName});

  final String fileName;
  late final Future<_BatchSendOutcome> future;
  bool completed = false;
  bool queued = false;
}

class _BatchSendOutcome {
  const _BatchSendOutcome({this.message, this.error});

  final ChatMessage? message;
  final Object? error;
}

String _friendlyImageError(Object error) => error
    .toString()
    .replaceFirst('FormatException: ', '')
    .replaceFirst('FileSystemException: ', '')
    .trim();

String _outputName(String original, String extension) {
  final trimmed = original.trim();
  if (trimmed.isEmpty) return 'image$extension';
  final separator = trimmed.lastIndexOf('.');
  final base = separator > 0 ? trimmed.substring(0, separator) : trimmed;
  return '$base$extension';
}

bool isGifImageBytes(Uint8List bytes) {
  if (bytes.length < 6) return false;
  return bytes[0] == 0x47 &&
      bytes[1] == 0x49 &&
      bytes[2] == 0x46 &&
      bytes[3] == 0x38 &&
      (bytes[4] == 0x37 || bytes[4] == 0x39) &&
      bytes[5] == 0x61;
}

bool hasSupportedChatImageSignature(Uint8List bytes) {
  if (isGifImageBytes(bytes)) return true;
  if (bytes.length >= 3 &&
      bytes[0] == 0xff &&
      bytes[1] == 0xd8 &&
      bytes[2] == 0xff) {
    return true;
  }
  if (bytes.length >= 8 &&
      bytes[0] == 0x89 &&
      bytes[1] == 0x50 &&
      bytes[2] == 0x4e &&
      bytes[3] == 0x47 &&
      bytes[4] == 0x0d &&
      bytes[5] == 0x0a &&
      bytes[6] == 0x1a &&
      bytes[7] == 0x0a) {
    return true;
  }
  return bytes.length >= 12 &&
      bytes[0] == 0x52 &&
      bytes[1] == 0x49 &&
      bytes[2] == 0x46 &&
      bytes[3] == 0x46 &&
      bytes[8] == 0x57 &&
      bytes[9] == 0x45 &&
      bytes[10] == 0x42 &&
      bytes[11] == 0x50;
}

(int, int)? _decodeImageDimensions(Uint8List bytes) {
  final image = image_lib.decodeImage(bytes);
  if (image == null || image.width <= 0 || image.height <= 0) return null;
  return (image.width, image.height);
}

(Uint8List, int, int)? _compressStaticImage(Uint8List bytes) {
  var image = image_lib.decodeImage(bytes);
  if (image == null || image.width <= 0 || image.height <= 0) return null;
  image = image_lib.bakeOrientation(image);
  final longest = image.width > image.height ? image.width : image.height;
  if (longest > maxChatImageDimension) {
    if (image.width >= image.height) {
      image = image_lib.copyResize(image, width: maxChatImageDimension);
    } else {
      image = image_lib.copyResize(image, height: maxChatImageDimension);
    }
  }
  return (
    Uint8List.fromList(
      image_lib.encodeJpg(image, quality: chatImageJpegQuality),
    ),
    image.width,
    image.height,
  );
}
