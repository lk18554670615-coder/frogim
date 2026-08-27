import 'dart:io';
import 'dart:typed_data';

import 'package:path_provider/path_provider.dart';

Future<String> persistEditedImage(Uint8List bytes) async {
  return persistImageBytes(bytes, mime: 'image/jpeg', extension: '.jpg');
}

Future<String> persistImageBytes(
  Uint8List bytes, {
  required String mime,
  required String extension,
}) async {
  final directory = await getTemporaryDirectory();
  final safeExtension = extension.toLowerCase() == '.gif' ? '.gif' : '.jpg';
  final file = File(
    '${directory.path}${Platform.pathSeparator}'
    'linli-image-${DateTime.now().microsecondsSinceEpoch}$safeExtension',
  );
  await file.writeAsBytes(bytes, flush: true);
  return file.path;
}
