export 'video_source_lifecycle_stub.dart'
    if (dart.library.io) 'video_source_lifecycle_native.dart'
    if (dart.library.js_interop) 'video_source_lifecycle_web.dart';
