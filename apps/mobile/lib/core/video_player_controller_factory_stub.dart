import 'package:video_player/video_player.dart';

VideoPlayerController createVideoPlayerController(String source) =>
    VideoPlayerController.networkUrl(Uri.parse(source));
