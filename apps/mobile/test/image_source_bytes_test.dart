import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/image_source_bytes.dart';

void main() {
  test('data 图片按原字节读取并执行大小上限', () async {
    final source = 'data:image/png;base64,${base64Encode([1, 2, 3, 4])}';
    expect(await loadImageSourceBytes(source, maxBytes: 4), [1, 2, 3, 4]);
    await expectLater(
      loadImageSourceBytes(source, maxBytes: 3),
      throwsA(
        isA<ImageSourceBytesException>().having(
          (error) => error.message,
          'message',
          '图片过大，暂时无法保存或编辑',
        ),
      ),
    );
  });

  test('无效 data 图片给出可理解错误', () async {
    await expectLater(
      loadImageSourceBytes('data:image/png;base64,***', maxBytes: 1024),
      throwsA(
        isA<ImageSourceBytesException>().having(
          (error) => error.message,
          'message',
          '图片数据无效',
        ),
      ),
    );
  });
}
