// Xcode invokes the existing Flutter Dart runtime; no extra build dependency.
import 'dart:convert';
import 'dart:io';

bool nativeTenantMode(String defines) {
  final values = <String>[];
  for (final encoded in defines.split(',').where((entry) => entry.isNotEmpty)) {
    final decoded = utf8.decode(base64.decode(encoded));
    if (decoded.startsWith('PLATFORM_AUTH_URL=')) {
      values.add(decoded.substring('PLATFORM_AUTH_URL='.length));
    }
  }
  if (values.length > 1) {
    throw const FormatException('ambiguous native auth mode');
  }
  return values.isNotEmpty && values.single.trim().isNotEmpty;
}

Future<void> main(List<String> arguments) async {
  try {
    if (arguments.length != 1 ||
        !arguments.single.endsWith('/linli_call_mode.json')) {
      throw const FormatException('invalid native mode output');
    }
    final enabled = nativeTenantMode(
      Platform.environment['DART_DEFINES'] ?? '',
    );
    final file = File(arguments.single);
    await file.parent.create(recursive: true);
    await file.writeAsString(
      jsonEncode({'schema': 1, 'managed': enabled}),
      flush: true,
    );
  } catch (_) {
    // Never echo DART_DEFINES: unrelated build values may be sensitive.
    stderr.writeln(
      'Native call mode generation failed. Check the build configuration.',
    );
    exitCode = 1;
  }
}
