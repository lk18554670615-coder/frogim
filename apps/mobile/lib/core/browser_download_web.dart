import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

void downloadBytesInBrowser(
  Uint8List bytes, {
  required String fileName,
  required String mimeType,
}) {
  final blob = web.Blob(
    <JSUint8Array>[bytes.toJS].toJS,
    web.BlobPropertyBag(type: mimeType),
  );
  final objectUrl = web.URL.createObjectURL(blob);
  web.HTMLAnchorElement? anchor;
  try {
    anchor = web.HTMLAnchorElement()
      ..href = objectUrl
      ..download = fileName;
    web.document.body?.appendChild(anchor);
    anchor.click();
  } finally {
    anchor?.remove();
    web.URL.revokeObjectURL(objectUrl);
  }
}
