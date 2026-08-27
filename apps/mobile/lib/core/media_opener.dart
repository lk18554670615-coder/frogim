export 'media_opener_stub.dart'
    if (dart.library.io) 'media_opener_native.dart'
    if (dart.library.js_interop) 'media_opener_web.dart';
