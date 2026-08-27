import 'dart:typed_data';

import 'package:image/image.dart' as image_library;
import 'package:zxing2/qrcode.dart';

String? decodeQrImageBytes(Uint8List bytes) {
  final image = image_library.decodeImage(bytes);
  if (image == null) {
    throw const FormatException('无法解析图片，请选择 PNG、JPG 或 WebP 图片');
  }
  final pixels = image
      .convert(numChannels: 4)
      .getBytes(order: image_library.ChannelOrder.abgr)
      .buffer
      .asInt32List();
  final source = RGBLuminanceSource(image.width, image.height, pixels);
  final bitmap = BinaryBitmap(HybridBinarizer(source));
  try {
    final result = QRCodeReader().decode(bitmap);
    final value = result.text.trim();
    return value.isEmpty ? null : value;
  } on ReaderException {
    return null;
  }
}
