import 'dart:io';

import 'package:video_player/video_player.dart';
import 'media_access.dart';

VideoPlayerController createVideoPlayerController(String source) {
  final uri = Uri.tryParse(source);
  if (uri != null && (uri.scheme == 'http' || uri.scheme == 'https')) {
    return VideoPlayerController.networkUrl(
      uri,
      httpHeaders: mediaAccess.headersFor(source),
    );
  }
  return VideoPlayerController.file(File(source));
}
