import 'package:video_player/video_player.dart';
import 'media_access.dart';

VideoPlayerController createVideoPlayerController(String source) =>
    VideoPlayerController.networkUrl(
      Uri.parse(source),
      httpHeaders: mediaAccess.headersFor(source),
    );
