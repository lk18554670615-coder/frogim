import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'image_source_bytes_contract.dart';
import 'image_source_bytes_stub.dart'
    if (dart.library.io) 'image_source_bytes_native.dart'
    as platform;

export 'image_source_bytes_contract.dart';

Future<Uint8List> loadImageSourceBytes(
  String source, {
  required int maxBytes,
}) async {
  final value = source.trim();
  if (value.isEmpty) throw const ImageSourceBytesException('图片地址暂不可用');
  if (value.startsWith('data:')) {
    final comma = value.indexOf(',');
    if (comma < 0) throw const ImageSourceBytesException('图片数据无效');
    try {
      final metadata = value.substring(0, comma);
      final payload = value.substring(comma + 1);
      final bytes = metadata.contains(';base64')
          ? base64Decode(payload)
          : Uint8List.fromList(utf8.encode(Uri.decodeComponent(payload)));
      _checkSize(bytes.length, maxBytes);
      return bytes;
    } on ImageSourceBytesException {
      rethrow;
    } catch (_) {
      throw const ImageSourceBytesException('图片数据无效');
    }
  }
  final uri = Uri.tryParse(value);
  if (uri?.scheme == 'http' || uri?.scheme == 'https') {
    final client = http.Client();
    try {
      final response = await client
          .send(http.Request('GET', uri!))
          .timeout(const Duration(seconds: 45));
      if (response.statusCode < 200 || response.statusCode >= 300) {
        throw ImageSourceBytesException('图片下载失败（${response.statusCode}）');
      }
      final declared = response.contentLength;
      if (declared != null) _checkSize(declared, maxBytes);
      final builder = BytesBuilder(copy: false);
      var received = 0;
      await for (final chunk in response.stream) {
        received += chunk.length;
        _checkSize(received, maxBytes);
        builder.add(chunk);
      }
      if (received == 0) throw const ImageSourceBytesException('下载到的图片为空');
      return builder.takeBytes();
    } on TimeoutException {
      throw const ImageSourceBytesException('图片下载超时，请检查网络后重试');
    } finally {
      client.close();
    }
  }
  final bytes = await platform.readLocalImageBytes(value);
  _checkSize(bytes.length, maxBytes);
  return bytes;
}

void _checkSize(int length, int maxBytes) {
  if (length > maxBytes) {
    throw const ImageSourceBytesException('图片过大，暂时无法保存或编辑');
  }
}
