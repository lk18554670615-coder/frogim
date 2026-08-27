import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

void registerBundledLicenses() {
  LicenseRegistry.addLicense(() async* {
    final license = await rootBundle.loadString('assets/fonts/OFL.txt');
    yield LicenseEntryWithLineBreaks(const ['Noto Sans SC'], license);
    final emojiLicense = await rootBundle.loadString(
      'assets/fonts/NotoColorEmoji-OFL.txt',
    );
    yield LicenseEntryWithLineBreaks(const ['Noto Color Emoji'], emojiLicense);
  });
}
