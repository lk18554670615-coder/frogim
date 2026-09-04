import 'video_preparation_types.dart';

bool get supportsNativeVideoCompression => false;

Future<PreparedVideoData> prepareVideoForSending({
  required String path,
  required String fileName,
  required OriginalVideoBytesReader readOriginalBytes,
  required int maxBytes,
  int? previewDurationSeconds,
  VideoPreparationProgress? onProgress,
}) async {
  onProgress?.call(0);
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

Future<void> cancelVideoPreparation() async {}
