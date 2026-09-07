import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';

/// Retains strict comparison; only baseline writes differ on Windows.
class AtomicGoldenComparator extends LocalFileComparator {
  AtomicGoldenComparator(super.testFile);

  @override
  Future<void> update(Uri golden, Uint8List imageBytes) async {
    // Windows image previews can memory-map a golden. Replace it atomically
    // rather than attempting to truncate the mapped file in place.
    final target = File.fromUri(basedir.resolveUri(golden));
    await target.parent.create(recursive: true);
    final temporary = File('${target.path}.$pid.tmp');
    try {
      await temporary.writeAsBytes(imageBytes, flush: true);
      await temporary.rename(target.path);
    } finally {
      if (await temporary.exists()) await temporary.delete();
    }
  }
}
