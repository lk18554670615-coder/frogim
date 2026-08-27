import 'dart:typed_data';

import 'package:gal/gal.dart';

import 'image_export_contract.dart';

Future<void> exportPngBytes(
  Uint8List bytes, {
  required String fileName,
  required String album,
}) async {
  try {
    var allowed = await Gal.hasAccess();
    if (!allowed) allowed = await Gal.requestAccess();
    if (!allowed) {
      throw const ImageExportException('请在系统设置中允许青蛙呱呱添加照片');
    }
    await Gal.putImageBytes(bytes, album: album, name: fileName);
  } on ImageExportException {
    rethrow;
  } on GalException {
    throw const ImageExportException('保存失败，请检查相册权限和设备存储空间');
  }
}

Future<void> exportImageBytes(
  Uint8List bytes, {
  required String fileName,
  required String mimeType,
  required String extension,
  required String album,
}) async {
  // gal detects the byte format and appends its own extension. Passing a name
  // that already contains one produces files such as `photo.jpg.jpg` on
  // Android, which some gallery editors cannot hand off correctly.
  final cleanName = imageFileStem(fileName, extension);
  try {
    var allowed = await Gal.hasAccess();
    if (!allowed) allowed = await Gal.requestAccess();
    if (!allowed) {
      throw const ImageExportException('请在系统设置中允许青蛙呱呱添加照片');
    }
    await Gal.putImageBytes(bytes, album: album, name: cleanName);
  } on ImageExportException {
    rethrow;
  } on GalException {
    throw const ImageExportException('保存失败，请检查相册权限和设备存储空间');
  }
}
