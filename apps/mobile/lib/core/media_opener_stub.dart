import 'models.dart';

Future<String?> openMessageMedia(
  ChatMessage message, {
  required int maxBytes,
}) async => '当前平台暂不支持直接打开此文件';

Future<int> messageMediaCacheBytes() async => 0;

Future<void> clearMessageMediaCache() async {}
