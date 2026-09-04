import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/media_access.dart';

void main() {
  late MediaAccess access;
  late Object owner;
  setUp(() {
    access = MediaAccess();
    owner = Object();
    access.configure(
      owner: owner,
      apiBaseUrl: 'https://im.example.com',
      userId: 'alice',
      token: 'media-only-secret',
    );
  });
  test('image/video/cover identity never contains an expiry or a token', () {
    final first = access.url('med_123');
    expect(
      first,
      'https://im.example.com/v2/media/med_123/content?viewer=alice',
    );
    expect(
      access.source(
        'med_123',
        'https://storage.example.com/expired?X-Amz-Expires=900',
      ),
      first,
    );
    expect(
      access.url('med_123', cover: true),
      contains('/med_123/cover?viewer=alice'),
    );
    access.configure(
      owner: owner,
      apiBaseUrl: 'https://im.example.com',
      userId: 'alice',
      token: 'rotated-secret',
    );
    expect(access.url('med_123'), first);
    expect(first, isNot(contains('secret')));
  });
  test('authenticate only the exact origin, media path and current viewer', () {
    expect(access.headersFor(access.url('med_1')!), {
      'Authorization': 'Media media-only-secret',
    });
    for (final source in [
      'https://storage.example.com/v2/media/med_1/content?viewer=alice',
      'http://im.example.com/v2/media/med_1/content?viewer=alice',
      'https://im.example.com:444/v2/media/med_1/content?viewer=alice',
      'https://im.example.com/v2/users/me?viewer=alice',
      'https://im.example.com/v2/media/med_1/content?viewer=bob',
      'blob:local',
      'data:image/png;base64,AA==',
    ]) {
      expect(access.headersFor(source), isEmpty, reason: source);
    }
  });
  test('local draft files and blob previews are preserved', () {
    for (final source in [
      'blob:local',
      'C:/draft.png',
      '/tmp/draft.png',
      'assets/a.png',
      'data:image/png;base64,AA==',
    ]) {
      expect(access.source('med_1', source), source);
    }
    expect(access.url('https://external.example/image.jpg'), isNull);
    expect(access.url('../session'), isNull);
  });
  test(
    'logout removes credentials; closing an old owner cannot clear a new login',
    () {
      final old = access.url('med_1')!;
      access.clear(owner);
      expect(access.headersFor(old), isEmpty);
      expect(access.url('med_1'), isNull);
      final second = Object();
      access.configure(
        owner: second,
        apiBaseUrl: 'https://im.example.com',
        userId: 'bob',
        token: 'bob-secret',
      );
      access.clear(owner);
      expect(access.headersFor(old), isEmpty);
      expect(access.headersFor(access.url('med_1')!), {
        'Authorization': 'Media bob-secret',
      });
    },
  );
}
