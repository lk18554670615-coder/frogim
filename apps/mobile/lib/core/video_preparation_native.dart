import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:video_compress/video_compress.dart';
import 'package:image/image.dart' as img;

import 'video_preparation_types.dart';
import 'video_source_lifecycle.dart';

bool get supportsNativeVideoCompression =>
    Platform.isAndroid || Platform.isIOS || Platform.isMacOS;

Future<PreparedVideoData> prepareVideoForSending({
  required String path,
  required String fileName,
  required OriginalVideoBytesReader readOriginalBytes,
  required int maxBytes,
  int? previewDurationSeconds,
  VideoPreparationProgress? onProgress,
}) async {
  if (!supportsNativeVideoCompression || path.trim().isEmpty) {
    final bytes = await readOriginalBytes();
    final duration = previewDurationSeconds ?? 0;
    validatePreparedVideo(
      byteLength: bytes.length,
      maxBytes: maxBytes,
      durationSeconds: duration,
    );
    onProgress?.call(1);
    return PreparedVideoData(
      bytes: bytes,
      path: path,
      fileName: fileName,
      mimeType: videoMimeType(fileName),
      durationSeconds: duration,
      compressed: false,
    );
  }

  Subscription? subscription;
  String? generatedPath;
  var delivered = false;
  try {
    subscription = VideoCompress.compressProgress$.subscribe((value) {
      onProgress?.call((value / 100).clamp(0, 1));
    });
    onProgress?.call(0);
    final originalInfo = await VideoCompress.getMediaInfo(path);
    final sourceDuration = videoDurationSeconds(originalInfo.duration);
    if (sourceDuration > 300 || (previewDurationSeconds ?? 0) > 300) {
      throw const FormatException('视频时长不能超过 5 分钟');
    }
    final compressed = await VideoCompress.compressVideo(
      path,
      quality: VideoQuality.Res1280x720Quality,
      deleteOrigin: false,
      includeAudio: true,
      frameRate: 30,
    );
    if (compressed == null || compressed.isCancel == true) {
      throw const VideoPreparationCancelled();
    }
    final file = compressed.file;
    if (file == null) {
      throw const FormatException('视频压缩失败，请重试');
    }
    if (file.path != path) {
      generatedPath = file.path;
      registerVideoTemporarySource(file.path);
    }
    final bytes = await file.readAsBytes();
    final duration = videoDurationSeconds(compressed.duration) == 0
        ? (sourceDuration == 0 ? previewDurationSeconds ?? 0 : sourceDuration)
        : videoDurationSeconds(compressed.duration);
    validatePreparedVideo(
      byteLength: bytes.length,
      maxBytes: maxBytes,
      durationSeconds: duration,
    );
    onProgress?.call(1);
    Uint8List? cover;
    try {
      final milliseconds = compressed.duration ?? originalInfo.duration ?? 0;
      final seconds = milliseconds > 0 && milliseconds < 2000
          ? milliseconds / 2000
          : 1.0;
      // 3.1.4 passes Android's value to getFrameAtTime (microseconds),
      // while Apple passes it to CMTimeMakeWithSeconds.
      final raw =
          await (Platform.isAndroid
                  ? VideoCompress.getByteThumbnail(
                      file.path,
                      quality: 80,
                      position: (seconds * 1000000).round(),
                    )
                  // The Dart wrapper restricts position to int, but both pinned Apple
                  // implementations accept NSNumber and use Float64 seconds. Reuse
                  // that same byte-thumbnail method to preserve sub-second precision.
                  : VideoCompress.channel.invokeMethod<Uint8List>(
                      'getByteThumbnail',
                      {'path': file.path, 'quality': 80, 'position': seconds},
                    ))
              .timeout(const Duration(seconds: 8));
      final decoded = raw == null ? null : img.decodeImage(raw);
      if (decoded != null) {
        final resized = decoded.width > 640 || decoded.height > 640
            ? img.copyResize(
                decoded,
                width: decoded.width >= decoded.height ? 640 : null,
                height: decoded.height > decoded.width ? 640 : null,
              )
            : decoded;
        cover = Uint8List.fromList(img.encodeJpg(resized, quality: 80));
      }
    } catch (_) {
      /* A playable video does not require a poster. */
    }
    delivered = true;
    return PreparedVideoData(
      bytes: bytes,
      path: file.path,
      fileName: mp4FileName(fileName),
      mimeType: 'video/mp4',
      durationSeconds: duration,
      compressed: true,
      width: compressed.width,
      height: compressed.height,
      coverBytes: cover,
    );
  } on MissingPluginException {
    throw const FormatException('视频压缩组件未正确安装，请更新客户端后重试');
  } finally {
    subscription?.unsubscribe();
    if (!delivered) releaseVideoSource(generatedPath);
  }
}

Future<void> cancelVideoPreparation() async {
  if (supportsNativeVideoCompression) {
    await VideoCompress.cancelCompression();
  }
}
