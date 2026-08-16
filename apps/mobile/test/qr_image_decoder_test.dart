import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as image_library;
import 'package:linli_im/core/qr_image_decoder.dart';
import 'package:zxing2/qrcode.dart';

void main() {
  test('Web 相册二维码使用本地字节解码', () {
    const content = 'qingwaguagua://user?handle=qingwa_2026';
    final code = Encoder.encode(content, ErrorCorrectionLevel.h);
    final matrix = code.matrix!;
    const scale = 10;
    const quietZone = 4;
    final side = (matrix.width + quietZone * 2) * scale;
    final image = image_library.Image(
      width: side,
      height: side,
      numChannels: 4,
    );
    image_library.fill(
      image,
      color: image_library.ColorRgba8(255, 255, 255, 255),
    );
    for (var x = 0; x < matrix.width; x++) {
      for (var y = 0; y < matrix.height; y++) {
        if (matrix.get(x, y) != 1) continue;
        image_library.fillRect(
          image,
          x1: (x + quietZone) * scale,
          y1: (y + quietZone) * scale,
          x2: (x + quietZone + 1) * scale - 1,
          y2: (y + quietZone + 1) * scale - 1,
          color: image_library.ColorRgba8(0, 0, 0, 255),
        );
      }
    }

    expect(decodeQrImageBytes(image_library.encodePng(image)), content);
  });

  test('图片中没有二维码时返回空结果', () {
    final image = image_library.Image(width: 80, height: 80, numChannels: 4);
    image_library.fill(
      image,
      color: image_library.ColorRgba8(255, 255, 255, 255),
    );

    expect(decodeQrImageBytes(image_library.encodePng(image)), isNull);
  });
}
