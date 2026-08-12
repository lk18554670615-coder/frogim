import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:video_compress/video_compress.dart';

import 'video_preparation_types.dart';

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
      mimeType: _mimeForVideo(fileName),
      durationSeconds: duration,
      compressed: false,
    );
  }

  Subscription? subscription;
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
    return PreparedVideoData(
      bytes: bytes,
      path: file.path,
      fileName: mp4FileName(fileName),
      mimeType: 'video/mp4',
      durationSeconds: duration,
      compressed: true,
    );
  } on MissingPluginException {
    throw const FormatException('视频压缩组件未正确安装，请更新客户端后重试');
  } finally {
    subscription?.unsubscribe();
  }
}

Future<void> cancelVideoPreparation() async {
  if (supportsNativeVideoCompression) {
    await VideoCompress.cancelCompression();
  }
}

String _mimeForVideo(String fileName) {
  final lower = fileName.toLowerCase();
  if (lower.endsWith('.mov')) return 'video/quicktime';
  if (lower.endsWith('.m4v')) return 'video/x-m4v';
  return 'video/mp4';
}
