import 'image_clipboard_contract.dart';

Future<void> copyImageSourceToClipboard(
  Future<String> Function() resolveSource, {
  required int maxBytes,
}) async {
  throw const ImageClipboardException('当前平台不支持复制图片');
}
