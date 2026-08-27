import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/bundled_licenses.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('bundled fonts expose their upstream OFL licenses', () async {
    registerBundledLicenses();
    final packages = <String>{};
    await for (final entry in LicenseRegistry.licenses) {
      packages.addAll(entry.packages);
    }

    expect(packages, contains('Noto Sans SC'));
    expect(packages, contains('Noto Color Emoji'));
  });
}
