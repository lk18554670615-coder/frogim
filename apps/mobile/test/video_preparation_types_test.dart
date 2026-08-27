import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/video_preparation_types.dart';

void main() {
  group('video preparation contract', () {
    test('converts millisecond duration using a safe ceiling', () {
      expect(videoDurationSeconds(null), 0);
      expect(videoDurationSeconds(0), 0);
      expect(videoDurationSeconds(1), 1);
      expect(videoDurationSeconds(1000), 1);
      expect(videoDurationSeconds(1001), 2);
    });

    test('normalizes compressed output to an MP4 name', () {
      expect(mp4FileName('clip.mov'), 'clip.mp4');
      expect(mp4FileName('release.preview.M4V'), 'release.preview.mp4');
      expect(mp4FileName(''), 'video.mp4');
    });

    test('rejects duration and compressed size outside product limits', () {
      expect(
        () => validatePreparedVideo(
          byteLength: 10,
          maxBytes: 20,
          durationSeconds: 301,
        ),
        throwsA(isA<FormatException>()),
      );
      expect(
        () => validatePreparedVideo(
          byteLength: 21,
          maxBytes: 20,
          durationSeconds: 10,
        ),
        throwsA(isA<FormatException>()),
      );
      expect(
        () => validatePreparedVideo(
          byteLength: 20,
          maxBytes: 20,
          durationSeconds: 300,
        ),
        returnsNormally,
      );
    });
  });
}
