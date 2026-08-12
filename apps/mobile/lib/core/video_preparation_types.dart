import 'dart:typed_data';

typedef OriginalVideoBytesReader = Future<Uint8List> Function();
typedef VideoPreparationProgress = void Function(double progress);

class PreparedVideoData {
  const PreparedVideoData({
    required this.bytes,
    required this.path,
    required this.fileName,
    required this.mimeType,
    required this.durationSeconds,
    required this.compressed,
  });

  final Uint8List bytes;
  final String path;
  final String fileName;
  final String mimeType;
  final int durationSeconds;
  final bool compressed;
}

class VideoPreparationCancelled implements Exception {
  const VideoPreparationCancelled();

  @override
  String toString() => '视频处理已取消';
}

int videoDurationSeconds(double? milliseconds) {
  if (milliseconds == null || !milliseconds.isFinite || milliseconds <= 0) {
    return 0;
  }
  return (milliseconds / 1000).ceil();
}

String mp4FileName(String originalName) {
  final trimmed = originalName.trim();
  if (trimmed.isEmpty) return 'video.mp4';
  final dot = trimmed.lastIndexOf('.');
  final base = dot > 0 ? trimmed.substring(0, dot) : trimmed;
  return '$base.mp4';
}

void validatePreparedVideo({
  required int byteLength,
  required int maxBytes,
  required int durationSeconds,
  int maxDurationSeconds = 300,
}) {
  if (durationSeconds > maxDurationSeconds) {
    throw const FormatException('视频时长不能超过 5 分钟');
  }
  if (byteLength > maxBytes) {
    final maxMB = maxBytes ~/ (1024 * 1024);
    throw FormatException('压缩后的视频仍超过 $maxMB MB');
  }
}
