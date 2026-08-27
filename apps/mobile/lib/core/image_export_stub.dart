import 'dart:typed_data';

import 'image_export_contract.dart';

Future<void> exportPngBytes(
  Uint8List bytes, {
  required String fileName,
  required String album,
}) => throw const ImageExportException('当前平台不支持导出图片');

Future<void> exportImageBytes(
  Uint8List bytes, {
  required String fileName,
  required String mimeType,
  required String extension,
  required String album,
}) async {
  throw const ImageExportException('当前平台不支持导出图片');
}
