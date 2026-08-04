import 'dart:typed_data';

import 'package:flutter/widgets.dart';

class WebPickedFile {
  const WebPickedFile({
    required this.bytes,
    required this.name,
    required this.mimeType,
  });

  final Uint8List bytes;
  final String name;
  final String mimeType;
}

class WebDropPasteRegion extends StatelessWidget {
  const WebDropPasteRegion({
    super.key,
    required this.child,
    required this.onFiles,
    required this.onError,
  });

  final Widget child;
  final Future<void> Function(List<WebPickedFile> files) onFiles;
  final ValueChanged<String> onError;

  @override
  Widget build(BuildContext context) => child;
}
