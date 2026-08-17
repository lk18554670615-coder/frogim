import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/client_device.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'stable installation id is reused by device and upgrade reporting',
    () async {
      SharedPreferences.setMockInitialValues({});
      final first = await ClientInstallationIdentity.getOrCreate();
      final second = await ClientInstallationIdentity.getOrCreate();
      expect(first, hasLength(greaterThanOrEqualTo(8)));
      expect(second, first);
    },
  );

  test('device report maps all four supported client platforms', () async {
    final package = PackageInfo(
      appName: '青蛙呱呱',
      packageName: 'com.qingwaguagua.im',
      version: '1.2.3',
      buildNumber: '45',
    );
    for (final platform in ['android', 'ios', 'web', 'macos']) {
      final report = await ClientDeviceReporter.collect(
        packageInfo: package,
        installationId: 'stable-installation-id',
        platformOverride: platform,
        deviceDetails: (requestedPlatform) async => {
          'deviceName': '$requestedPlatform device',
          'deviceModel': '$requestedPlatform model',
          'osVersion': '$requestedPlatform os',
        },
      );
      expect(report.installationId, 'stable-installation-id');
      expect(report.platform, platform);
      expect(report.deviceName, '$platform device');
      expect(report.deviceModel, '$platform model');
      expect(report.osVersion, '$platform os');
      expect(report.appVersion, '1.2.3');
    }
  });
}
