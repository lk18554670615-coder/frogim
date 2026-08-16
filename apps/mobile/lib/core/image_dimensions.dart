import 'package:flutter/foundation.dart';
import 'package:image/image.dart' as image_lib;

Future<({int width, int height})?> decodeImagePixelSize(Uint8List bytes) async {
  if (bytes.isEmpty) return null;
  try {
    return await compute(_decodeImagePixelSize, bytes);
  } catch (_) {
    return null;
  }
}

({int width, int height})? _decodeImagePixelSize(Uint8List bytes) {
  final image = image_lib.decodeImage(bytes);
  if (image == null || image.width <= 0 || image.height <= 0) return null;
  return (width: image.width, height: image.height);
}
