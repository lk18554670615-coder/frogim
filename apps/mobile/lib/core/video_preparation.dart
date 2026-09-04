import 'video_preparation_stub.dart'
    if (dart.library.io) 'video_preparation_native.dart'
    if (dart.library.js_interop) 'video_preparation_web.dart'
    as platform;
import 'video_preparation_types.dart';

export 'video_preparation_types.dart';

Future<PreparedVideoData> prepareVideoForSending({
  required String path,
  required String fileName,
  required OriginalVideoBytesReader readOriginalBytes,
  required int maxBytes,
  int? previewDurationSeconds,
  VideoPreparationProgress? onProgress,
}) => platform.prepareVideoForSending(
  path: path,
  fileName: fileName,
  readOriginalBytes: readOriginalBytes,
  maxBytes: maxBytes,
  previewDurationSeconds: previewDurationSeconds,
  onProgress: onProgress,
);

Future<void> cancelVideoPreparation() => platform.cancelVideoPreparation();

bool get supportsNativeVideoCompression =>
    platform.supportsNativeVideoCompression;
