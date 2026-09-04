import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:math';
import 'dart:typed_data';

import 'package:web/web.dart' as web;
import 'video_preparation_types.dart';

bool get supportsNativeVideoCompression => false;
Future<void> cancelVideoPreparation() async {}

Future<void> _wait(
  web.HTMLVideoElement video,
  String event,
  void Function() start,
) async {
  final done = Completer<void>();
  final success = ((web.Event _) {
    if (!done.isCompleted) done.complete();
  }).toJS;
  final failure = ((web.Event _) {
    if (!done.isCompleted) {
      done.completeError(const FormatException('当前浏览器无法解码此视频，可作为文件发送'));
    }
  }).toJS;
  video.addEventListener(event, success);
  video.addEventListener('error', failure);
  try {
    start();
    await done.future.timeout(const Duration(seconds: 8));
  } finally {
    video.removeEventListener(event, success);
    video.removeEventListener('error', failure);
  }
}

Future<PreparedVideoData> prepareVideoForSending({
  required String path,
  required String fileName,
  required OriginalVideoBytesReader readOriginalBytes,
  required int maxBytes,
  int? previewDurationSeconds,
  VideoPreparationProgress? onProgress,
}) async {
  onProgress?.call(0);
  final bytes = await readOriginalBytes().timeout(
    const Duration(seconds: 30),
    onTimeout: () => throw const FormatException('读取视频超时，请重试'),
  );
  final video = web.HTMLVideoElement()
    ..muted = true
    ..preload = 'auto'
    ..playsInline = true;
  int? width, height;
  var duration = previewDurationSeconds ?? 0;
  Uint8List? cover;
  try {
    await _wait(video, 'loadeddata', () {
      video.src = path;
      video.load();
    });
    if (!video.duration.isFinite ||
        video.duration <= 0 ||
        video.videoWidth <= 0) {
      throw const FormatException('无法读取视频信息，请重试或作为文件发送');
    }
    duration = video.duration.ceil();
    width = video.videoWidth;
    height = video.videoHeight;
    validatePreparedVideo(
      byteLength: bytes.length,
      maxBytes: maxBytes,
      durationSeconds: duration,
    );
    try {
      await _wait(video, 'seeked', () {
        video.currentTime = min(1, video.duration / 2);
      });
      final scale = min(1.0, 640 / max(width, height));
      final canvas = web.HTMLCanvasElement()
        ..width = max(1, (width * scale).round())
        ..height = max(1, (height * scale).round());
      final context = canvas.getContext('2d')! as web.CanvasRenderingContext2D;
      context.drawImage(video, 0, 0, canvas.width, canvas.height);
      cover = base64Decode(
        canvas.toDataURL('image/jpeg', 0.8.toJS).split(',').last,
      );
    } catch (_) {
      /* Missing poster must not discard a playable video. */
    }
  } finally {
    video.pause();
    video.removeAttribute('src');
    video.load();
  }
  onProgress?.call(1);
  return PreparedVideoData(
    bytes: bytes,
    path: path,
    fileName: fileName,
    mimeType: videoMimeType(fileName),
    durationSeconds: duration,
    compressed: false,
    width: width,
    height: height,
    coverBytes: cover,
  );
}
