import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'browser_download_web.dart';
import 'models.dart';

Future<String?> openMessageMedia(
  ChatMessage message, {
  required int maxBytes,
}) async {
  final source = message.mediaUrl?.trim();
  if (source == null || source.isEmpty) return '文件下载地址暂不可用';
  final uri = Uri.tryParse(source);
  if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
    return '网页端无法打开此本地文件';
  }
  final client = http.Client();
  try {
    final response = await client
        .send(http.Request('GET', uri))
        .timeout(const Duration(seconds: 45));
    if (response.statusCode < 200 || response.statusCode >= 300) {
      return '文件下载失败（${response.statusCode}）';
    }
    final declaredLength = response.contentLength;
    if (declaredLength != null && declaredLength > maxBytes) {
      return '文件超过允许下载的大小';
    }
    final buffer = BytesBuilder(copy: false);
    var received = 0;
    await for (final chunk in response.stream) {
      received += chunk.length;
      if (received > maxBytes) return '文件超过允许下载的大小';
      buffer.add(chunk);
    }
    if (received == 0) return '下载到的文件为空';
    downloadBytesInBrowser(
      buffer.takeBytes(),
      fileName: _safeDownloadName(message),
      mimeType: message.mimeType?.trim().isNotEmpty == true
          ? message.mimeType!.trim()
          : 'application/octet-stream',
    );
    return null;
  } on TimeoutException {
    return '文件下载超时，请检查网络后重试';
  } on Exception {
    return '文件下载失败，请检查网络或浏览器下载权限';
  } finally {
    client.close();
  }
}

String _safeDownloadName(ChatMessage message) {
  final source = message.fileName?.trim() ?? '';
  final cleaned = source
      .replaceAll(RegExp(r'[\\/:*?"<>|\u0000-\u001f]'), '_')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();
  if (cleaned.isNotEmpty && cleaned != '.' && cleaned != '..') {
    return cleaned.length <= 120
        ? cleaned
        : cleaned.substring(cleaned.length - 120);
  }
  final extension = switch (message.mimeType?.toLowerCase()) {
    'video/mp4' => '.mp4',
    'audio/mp4' => '.m4a',
    'application/pdf' => '.pdf',
    'image/jpeg' => '.jpg',
    'image/png' => '.png',
    _ => '',
  };
  final stable = (message.mediaId ?? message.id).replaceAll(
    RegExp(r'[^a-zA-Z0-9_-]'),
    '_',
  );
  return 'qingwaguagua-${stable.isEmpty ? 'file' : stable}$extension';
}

Future<int> messageMediaCacheBytes() async => 0;

Future<void> clearMessageMediaCache() async {}
