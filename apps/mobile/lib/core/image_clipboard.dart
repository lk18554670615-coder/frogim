import 'image_clipboard_stub.dart'
    if (dart.library.js_interop) 'image_clipboard_web.dart'
    as platform;

export 'image_clipboard_contract.dart';

/// Copies the browser-decodable image returned by [resolveSource] as PNG.
///
/// The Web implementation starts the Clipboard API call before resolving the
/// source so the write remains attached to the user's click gesture while an
/// authenticated image is downloaded and decoded.
Future<void> copyImageSourceToClipboard(
  Future<String> Function() resolveSource, {
  required int maxBytes,
}) => platform.copyImageSourceToClipboard(resolveSource, maxBytes: maxBytes);
