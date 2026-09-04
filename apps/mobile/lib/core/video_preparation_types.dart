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
    this.width,
    this.height,
    this.coverBytes,
  });

  final Uint8List bytes;
  final String path;
  final String fileName;
  final String mimeType;
  final int durationSeconds;
  final bool compressed;
  final int? width;
  final int? height;
  final Uint8List? coverBytes;
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
    throw FormatException('视频文件超过 $maxMB MB');
  }
  if (byteLength <= 0) throw const FormatException('视频文件为空');
}

String videoMimeType(String fileName) =>
    switch (fileName.toLowerCase().split('.').last) {
      'mp4' => 'video/mp4',
      'mov' => 'video/quicktime',
      'm4v' => 'video/x-m4v',
      'webm' => 'video/webm',
      'ogv' || 'ogg' => 'video/ogg',
      'avi' => 'video/x-msvideo',
      'mkv' => 'video/x-matroska',
      _ => 'application/octet-stream',
    };
