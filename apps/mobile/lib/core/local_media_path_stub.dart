import 'dart:convert';
import 'dart:typed_data';

Future<String> persistEditedImage(Uint8List bytes) async =>
    'data:image/jpeg;base64,${base64Encode(bytes)}';

Future<String> persistImageBytes(
  Uint8List bytes, {
  required String mime,
  required String extension,
}) async => 'data:$mime;base64,${base64Encode(bytes)}';
