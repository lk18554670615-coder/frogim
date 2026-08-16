import 'dart:io';
import 'dart:typed_data';

import 'image_source_bytes_contract.dart';

Future<Uint8List> readLocalImageBytes(String source) async {
  final uri = Uri.tryParse(source);
  final path = uri?.scheme == 'file' ? uri!.toFilePath() : source;
  final file = File(path);
  if (!await file.exists()) {
    throw const ImageSourceBytesException('本地图片已不可用');
  }
  return file.readAsBytes();
}
