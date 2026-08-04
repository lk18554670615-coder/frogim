import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:open_filex/open_filex.dart';
import 'package:path_provider/path_provider.dart';

import 'models.dart';

Future<String?> openMessageMedia(
  ChatMessage message, {
  required int maxBytes,
}) async {
  final source = message.mediaUrl?.trim();
  if (source == null || source.isEmpty) return '文件下载地址暂不可用';
  var path = source;
  if (source.startsWith('https://') || source.startsWith('http://')) {
    final uri = Uri.tryParse(source);
    if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
      return '文件地址无效';
    }
    final request = http.Request('GET', uri);
    final response = await http.Client()
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
    try {
      await for (final chunk in response.stream) {
        received += chunk.length;
        if (received > maxBytes) return '文件超过允许下载的大小';
        sink.add(chunk);
      }
    } finally {
      await sink.close();
    }
    if (received == 0) return '下载到的文件为空';
    path = file.path;
  }
  final file = File(path);
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
  try {
    final directory = await getTemporaryDirectory();
    final root = Directory('${directory.path}/linli-media');
    if (!await root.exists()) return 0;
    var total = 0;
    await for (final entity in root.list(recursive: true, followLinks: false)) {
      if (entity is File) total += await entity.length();
    }
    return total;
  } on Exception {
    // 桌面预览、单元测试或系统目录暂不可用时，缓存统计应安全降级，
    // 不能影响设置页及聊天主流程。
    return 0;
  }
}

Future<void> clearMessageMediaCache() async {
  try {
    final directory = await getTemporaryDirectory();
    final root = Directory('${directory.path}/linli-media');
    if (await root.exists()) await root.delete(recursive: true);
  } on Exception {
    // 目录插件不可用等非业务错误按“无缓存可清理”处理。
  }
}
