import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:open_filex/open_filex.dart';
import 'package:path_provider/path_provider.dart';

import 'models.dart';
import 'media_access.dart';

Future<String?> openMessageMedia(
  ChatMessage message, {
  required int maxBytes,
}) async {
  final source = mediaAccess.source(message.mediaId, message.mediaUrl)?.trim();
  if (source == null || source.isEmpty) return '文件下载地址暂不可用';
  final authenticated = mediaAccess.owns(source);
  var path = source;
  if (source.startsWith('https://') || source.startsWith('http://')) {
    final uri = Uri.tryParse(source);
    if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
      return '文件地址无效';
    }
    final client = http.Client();
    try {
      final request = http.Request('GET', uri);
      request.headers.addAll(mediaAccess.headersFor(source));
      final response = await client
          .send(request)
          .timeout(const Duration(seconds: 45));
      if (response.statusCode < 200 || response.statusCode >= 300) {
        return '文件下载失败（${response.statusCode}）';
      }
      final declaredLength = response.contentLength;
      if (declaredLength != null && declaredLength > maxBytes) {
        return '文件超过允许下载的大小';
      }
      final directory = await getTemporaryDirectory();
      final safeName = _safeFileName(
        message.fileName,
        message.mimeType,
        message.mediaId ?? message.id,
      );
      final file = File('${directory.path}/linli-media/$safeName');
      await file.parent.create(recursive: true);
      final sink = file.openWrite();
      var received = 0;
      var exceeded = false;
      var streamFailed = false;
      try {
        await for (final chunk in response.stream) {
          received += chunk.length;
          if (received > maxBytes) {
            exceeded = true;
            break;
          }
          sink.add(chunk);
        }
      } catch (_) {
        streamFailed = true;
        rethrow;
      } finally {
        await sink.close();
        if (streamFailed && await file.exists()) await file.delete();
      }
      if (exceeded || received == 0) {
        if (await file.exists()) await file.delete();
        return exceeded ? '文件超过允许下载的大小' : '下载到的文件为空';
      }
      path = file.path;
    } finally {
      client.close();
    }
  }
  final file = File(path);
  if (authenticated && !mediaAccess.owns(source)) return '登录账号已变化';
  if (!await file.exists()) return '本地文件已不可用';
  final result = await OpenFilex.open(path, type: message.mimeType);
  return result.type == ResultType.done ? null : result.message;
}

String _safeFileName(String? name, String? mime, String stableId) {
  final digest = sha256.convert(stableId.codeUnits).toString().substring(0, 20);
  final raw = (name ?? '').trim().replaceAll(RegExp(r'[^a-zA-Z0-9._-]'), '_');
  final extension = raw.contains('.')
      ? raw.substring(raw.lastIndexOf('.')).toLowerCase()
      : _extensionForMime(mime);
  return '$digest${extension.length <= 12 ? extension : ''}';
}

String _extensionForMime(String? mime) => switch (mime?.toLowerCase()) {
  'video/mp4' => '.mp4',
  'audio/mp4' => '.m4a',
  'application/pdf' => '.pdf',
  'image/jpeg' => '.jpg',
  'image/png' => '.png',
  _ => '',
};

Future<int> messageMediaCacheBytes() async {
  final directory = await getTemporaryDirectory();
  final root = Directory('${directory.path}/linli-media');
  if (!await root.exists()) return 0;
  var total = 0;
  await for (final entity in root.list(recursive: true, followLinks: false)) {
    if (entity is File) total += await entity.length();
  }
  return total;
}

Future<void> clearMessageMediaCache() async {
  final directory = await getTemporaryDirectory();
  final root = Directory('${directory.path}/linli-media');
  if (await root.exists()) await root.delete(recursive: true);
}
