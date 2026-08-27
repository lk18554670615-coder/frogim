import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/avatar_image.dart';

void main() {
  test(
    'detects supported avatar formats from bytes instead of the extension',
    () {
      expect(
        avatarImageMimeType(Uint8List.fromList([0xff, 0xd8, 0xff, 0x00])),
        'image/jpeg',
      );
      expect(
        avatarImageMimeType(
          Uint8List.fromList([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
        ),
        'image/png',
      );
      expect(
        avatarImageMimeType(
          Uint8List.fromList([
            0x52,
            0x49,
            0x46,
            0x46,
            0,
            0,
            0,
            0,
            0x57,
            0x45,
            0x42,
            0x50,
          ]),
        ),
        'image/webp',
      );
    },
  );

  test('rejects unsupported or truncated avatar bytes', () {
    expect(avatarImageMimeType(Uint8List(0)), isNull);
    expect(avatarImageMimeType(Uint8List.fromList([0xff, 0xd8])), isNull);
    expect(
      avatarImageMimeType(Uint8List.fromList('not an image'.codeUnits)),
      isNull,
    );
  });
}
