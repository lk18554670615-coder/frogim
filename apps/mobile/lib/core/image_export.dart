import 'dart:typed_data';

import 'image_export_stub.dart'
    if (dart.library.io) 'image_export_native.dart'
    if (dart.library.js_interop) 'image_export_web.dart'
    as platform;

export 'image_export_contract.dart';

Future<void> exportPngBytes(
  Uint8List bytes, {
  required String fileName,
  String album = '青蛙呱呱',
}) => platform.exportPngBytes(bytes, fileName: fileName, album: album);

Future<void> exportImageBytes(
  Uint8List bytes, {
  required String fileName,
  required String mimeType,
  required String extension,
  String album = '青蛙呱呱',
}) => platform.exportImageBytes(
  bytes,
  fileName: fileName,
  mimeType: mimeType,
  extension: extension,
  album: album,
);
