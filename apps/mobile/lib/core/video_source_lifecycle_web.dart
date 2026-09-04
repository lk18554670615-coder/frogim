import 'package:web/web.dart' as web;

void registerVideoTemporarySource(String source) {}

void releaseVideoSource(String? source) {
  if (source?.startsWith('blob:') == true) web.URL.revokeObjectURL(source!);
}
