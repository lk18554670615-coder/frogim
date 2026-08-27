import 'dart:typed_data';

import 'browser_download_web.dart';
import 'image_export_contract.dart';

Future<void> exportPngBytes(
  Uint8List bytes, {
  required String fileName,
  required String album,
}) async {
  try {
    downloadBytesInBrowser(
      bytes,
      fileName: '$fileName.png',
      mimeType: 'image/png',
    );
  } catch (_) {
    throw const ImageExportException('浏览器未能下载二维码，请检查下载权限后重试');
  }
}

Future<void> exportImageBytes(
  Uint8List bytes, {
  required String fileName,
  required String mimeType,
  required String extension,
  required String album,
}) async {
  final cleanName = imageFileName(fileName, extension);
  try {
    downloadBytesInBrowser(bytes, fileName: cleanName, mimeType: mimeType);
  } catch (_) {
    throw const ImageExportException('浏览器未能下载图片，请检查下载权限后重试');
  }
}
