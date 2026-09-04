import 'package:flutter/services.dart';

Future<void> setVideoFullscreen(bool enabled) =>
    SystemChrome.setEnabledSystemUIMode(
      enabled ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
    );
