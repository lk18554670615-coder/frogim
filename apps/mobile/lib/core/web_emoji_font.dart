import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

/// Web packages omit this font from the engine's blocking startup manifest.
/// Register it after the first frame; native builds keep their existing fonts.
class WebEmojiFont {
  WebEmojiFont({bool? isWeb, Future<void> Function()? loadFont})
    : _isWeb = isWeb ?? kIsWeb,
      _loadFont = loadFont ?? _loadBundledFont;

  static final instance = WebEmojiFont();
  static const family = 'NotoColorEmoji';
  static const asset = 'assets/fonts/NotoColorEmoji.ttf';

  final bool _isWeb;
  final Future<void> Function() _loadFont;
  Future<void>? _pending;
  bool _loaded = false;
  bool _scheduled = false;

  void loadAfterFirstFrame() {
    if (!_isWeb || _scheduled || _loaded) return;
    _scheduled = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      // Yield the frame before starting the background asset request.
      Timer.run(() {
        _scheduled = false;
        unawaited(load());
      });
    });
    WidgetsBinding.instance.scheduleFrame();
  }

  Future<void> load() {
    if (!_isWeb || _loaded) return Future<void>.value();
    return _pending ??= _tryLoad();
  }

  Future<void> _tryLoad() async {
    try {
      await Future<void>.sync(_loadFont);
      _loaded = true;
    } catch (_) {
      // Keep the page usable; a later foreground resume can retry.
      debugPrint('Background emoji font loading failed; will retry on resume.');
    } finally {
      _pending = null;
    }
  }

  static Future<void> _loadBundledFont() =>
      (FontLoader(family)..addFont(rootBundle.load(asset))).load();
}
