import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:js_interop_unsafe';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

import 'image_clipboard_contract.dart';
import 'image_source_bytes.dart';

Future<void> copyImageSourceToClipboard(
  Future<String> Function() resolveSource, {
  required int maxBytes,
}) async {
  if (!web.window.isSecureContext) {
    throw const ImageClipboardException('当前网页不是安全连接，浏览器无法复制图片');
  }
  if (!web.window.hasProperty('ClipboardItem'.toJS).toDart ||
      !web.window.navigator.hasProperty('clipboard'.toJS).toDart) {
    throw const ImageClipboardException('当前浏览器不支持复制图片，请使用“保存”下载图片');
  }

  Object? preparationError;
  final png = _preparePngBlob(resolveSource, maxBytes).catchError((
    Object error,
  ) {
    preparationError = error;
    throw error;
  });
  final data = <String, Object?>{}.jsify()! as JSObject;
  data.setProperty('image/png'.toJS, png.toJS);

  try {
    // Passing the pending PNG promise to ClipboardItem keeps the clipboard
    // write inside the transient user activation while media is fetched.
    final item = web.ClipboardItem(data);
    await web.window.navigator.clipboard
        .write(<web.ClipboardItem>[item].toJS)
        .toDart;
  } catch (_) {
    final error = preparationError;
    if (error is ImageClipboardException) throw error;
    if (error is ImageSourceBytesException) {
      throw ImageClipboardException(error.message);
    }
    throw const ImageClipboardException('复制失败，请允许浏览器访问剪贴板后重试');
  }
}

Future<web.Blob> _preparePngBlob(
  Future<String> Function() resolveSource,
  int maxBytes,
) async {
  var source = (await resolveSource()).trim();
  if (source.isEmpty) throw const ImageClipboardException('图片地址暂不可用');
  if (source.startsWith('/')) source = Uri.base.resolve(source).toString();

  final web.Blob original;
  if (source.startsWith('blob:')) {
    try {
      final response = await web.window
          .fetch(source.toJS)
          .toDart
          .timeout(const Duration(seconds: 30));
      if (!response.ok) {
        throw const ImageClipboardException('图片读取失败，请重新打开图片后重试');
      }
      original = await response.blob().toDart.timeout(
        const Duration(seconds: 30),
      );
      if (original.size == 0) {
        throw const ImageClipboardException('图片内容为空');
      }
      if (original.size > maxBytes) {
        throw const ImageClipboardException('图片过大，暂时无法复制');
      }
    } on ImageClipboardException {
      rethrow;
    } on TimeoutException {
      throw const ImageClipboardException('图片读取超时，请重试');
    } catch (_) {
      throw const ImageClipboardException('图片读取失败，请重新打开图片后重试');
    }
  } else {
    final bytes = await loadImageSourceBytes(source, maxBytes: maxBytes);
    original = web.Blob(
      <web.BlobPart>[bytes.toJS].toJS,
      web.BlobPropertyBag(type: _sourceMimeType(bytes)),
    );
  }

  final objectUrl = web.URL.createObjectURL(original);
  final image = web.HTMLImageElement();
  try {
    image.src = objectUrl;
    await image.decode().toDart.timeout(const Duration(seconds: 20));
    if (image.naturalWidth <= 0 || image.naturalHeight <= 0) {
      throw const ImageClipboardException('当前图片格式无法复制');
    }
    final canvas = web.HTMLCanvasElement()
      ..width = image.naturalWidth
      ..height = image.naturalHeight;
    final context = canvas.getContext('2d') as web.CanvasRenderingContext2D?;
    if (context == null) {
      throw const ImageClipboardException('浏览器无法处理这张图片');
    }
    context.drawImage(image, 0, 0);
    final dataUrl = canvas.toDataURL('image/png');
    final comma = dataUrl.indexOf(',');
    if (comma < 0) throw const ImageClipboardException('图片转换失败，请重试');
    final pngBytes = base64Decode(dataUrl.substring(comma + 1));
    return web.Blob(
      <web.BlobPart>[pngBytes.toJS].toJS,
      web.BlobPropertyBag(type: 'image/png'),
    );
  } on ImageClipboardException {
    rethrow;
  } on TimeoutException {
    throw const ImageClipboardException('图片解码超时，请重试');
  } catch (_) {
    throw const ImageClipboardException('当前图片格式无法复制');
  } finally {
    image.removeAttribute('src');
    web.URL.revokeObjectURL(objectUrl);
  }
}

String _sourceMimeType(Uint8List bytes) {
  if (bytes.length >= 8 &&
      bytes[0] == 0x89 &&
      bytes[1] == 0x50 &&
      bytes[2] == 0x4e &&
      bytes[3] == 0x47) {
    return 'image/png';
  }
  if (bytes.length >= 3 &&
      bytes[0] == 0xff &&
      bytes[1] == 0xd8 &&
      bytes[2] == 0xff) {
    return 'image/jpeg';
  }
  if (bytes.length >= 6 &&
      ascii.decode(bytes.sublist(0, 3), allowInvalid: true) == 'GIF') {
    return 'image/gif';
  }
  if (bytes.length >= 12 &&
      ascii.decode(bytes.sublist(0, 4), allowInvalid: true) == 'RIFF' &&
      ascii.decode(bytes.sublist(8, 12), allowInvalid: true) == 'WEBP') {
    return 'image/webp';
  }
  return 'application/octet-stream';
}
