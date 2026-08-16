import 'dart:typed_data';

import 'image_source_bytes_contract.dart';

Future<Uint8List> readLocalImageBytes(String source) async {
  throw const ImageSourceBytesException('网页端无法读取此本地图片');
}
