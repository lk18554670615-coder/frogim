import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import '../tool/write_native_call_mode.dart';

void main() {
  String encode(List<String> entries) =>
      entries.map((e) => base64.encode(utf8.encode(e))).join(',');
  test('native mode matches Flutter platform authentication build input', () {
    expect(nativeTenantMode(''), isFalse);
    expect(nativeTenantMode(encode(['APP_ENV=production'])), isFalse);
    expect(nativeTenantMode(encode(['PLATFORM_AUTH_URL='])), isFalse);
    expect(
      nativeTenantMode(
        encode([
          'PLATFORM_AUTH_URL=https://platform.example.test',
          'UNRELATED=fixture-only',
        ]),
      ),
      isTrue,
    );
  });
  test('malformed or ambiguous build input cannot silently enable legacy', () {
    expect(() => nativeTenantMode('invalid-base64!!!'), throwsFormatException);
    expect(
      () => nativeTenantMode(
        encode([
          'PLATFORM_AUTH_URL=',
          'PLATFORM_AUTH_URL=https://platform.example.test',
        ]),
      ),
      throwsFormatException,
    );
  });
}
