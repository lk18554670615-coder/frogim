import 'dart:async';
import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';
import '../../core/video_player_controller_factory.dart';
import '../../core/video_fullscreen.dart';
import '../../data/live_repository.dart' show ImApiException;
import '../../im/business_repository.dart' show BusinessApiException;
import 'media_send_widgets.dart' show VideoPreviewResult;

class VideoPlayerScreen extends StatefulWidget {
  const VideoPlayerScreen({
    super.key,
    required this.source,
    required this.title,
    this.sendMode = false,
    this.resolveSource,
    this.visibilityChanges,
    this.isVisible,
  });
  final String source, title;
  final bool sendMode;
  final Future<String> Function()? resolveSource;
  final Listenable? visibilityChanges;
  final bool Function()? isVisible;
  @override
  State<VideoPlayerScreen> createState() => _VideoPlayerScreenState();
}

class _VideoPlayerScreenState extends State<VideoPlayerScreen>
    with WidgetsBindingObserver {
  VideoPlayerController? player;
  String? error;
  bool loading = true, autoplayDenied = false, fullScreen = false;
  bool refreshedAfterError = false, confirming = false;
  int generation = 0;
  VoidCallback? cancelLoad;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.visibilityChanges?.addListener(_checkVisibility);
    unawaited(_initialize());
  }

  void _checkVisibility() {
    if (!mounted || widget.isVisible?.call() != false) return;
    unawaited(player?.pause());
    cancelLoad?.call();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.of(context).removeRoute(ModalRoute.of(context)!);
    });
  }

  Future<void> _initialize() async {
    final attempt = ++generation;
    cancelLoad?.call();
    setState(() {
      loading = true;
      error = null;
      autoplayDenied = false;
    });
    final previous = player;
    player = null;
    previous?.removeListener(_changed);
    // Some platform implementations never complete creation after a decode
    // error. Disposal must not leave the next attempt waiting indefinitely.
    if (previous != null) unawaited(_release(previous));
    try {
      final source = await _bounded(
        widget.resolveSource?.call() ?? Future.value(widget.source),
      );
      if (!mounted || generation != attempt) return;
      if (source.isEmpty) throw const FormatException('视频地址暂不可用');
      final candidate = createVideoPlayerController(source);
      player = candidate;
      await _bounded(candidate.initialize());
      if (!mounted || generation != attempt) return;
      candidate.addListener(_changed);
      await candidate.setLooping(false);
      setState(() => loading = false);
      try {
        await candidate.play();
      } catch (_) {
        if (mounted && generation == attempt) {
          setState(() => autoplayDenied = true);
        }
      }
    } catch (cause) {
      if (!mounted || generation != attempt) return;
      _failed(switch (cause) {
        BusinessApiException(:final statusCode, :final code, :final message) =>
          '$statusCode $code $message',
        ImApiException(:final statusCode, :final code, :final message) =>
          '$statusCode $code $message',
        _ => cause.toString(),
      });
    }
  }

  Future<T> _bounded<T>(Future<T> operation) {
    final result = Completer<T>();
    final timer = Timer(const Duration(seconds: 20), () {
      if (!result.isCompleted) {
        result.completeError(TimeoutException('video load timeout'));
      }
    });
    void cancel() {
      timer.cancel();
      if (!result.isCompleted) {
        result.completeError(StateError('video player closed'));
      }
    }

    cancelLoad = cancel;
    operation.then(
      (value) {
        if (!result.isCompleted) result.complete(value);
      },
      onError: (Object error, StackTrace stack) {
        if (!result.isCompleted) result.completeError(error, stack);
      },
    );
    return result.future.whenComplete(() {
      timer.cancel();
      if (identical(cancelLoad, cancel)) cancelLoad = null;
    });
  }

  Future<void> _release(VideoPlayerController controller) async {
    unawaited(controller.pause().catchError((Object _) {}));
    try {
      await controller.dispose();
    } catch (_) {}
  }

  void _failed(String message) {
    if (!refreshedAfterError &&
        widget.resolveSource != null &&
        !message.contains('404') &&
        !message.contains('401') &&
        !message.contains('NOT_FOUND') &&
        !message.contains('FORBIDDEN')) {
      refreshedAfterError = true;
      unawaited(_initialize());
      return;
    }
    setState(() {
      loading = false;
      error = videoPlaybackError(message);
    });
  }

  void _changed() {
    if (!mounted || loading) return;
    final value = player?.value;
    if (value?.hasError == true && error == null) {
      _failed(value!.errorDescription ?? '');
    } else {
      setState(() {});
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) unawaited(player?.pause());
  }

  @override
  void dispose() {
    widget.visibilityChanges?.removeListener(_checkVisibility);
    generation++;
    cancelLoad?.call();
    WidgetsBinding.instance.removeObserver(this);
    player?.removeListener(_changed);
    if (player case final current?) unawaited(_release(current));
    if (fullScreen) unawaited(setVideoFullscreen(false));
    super.dispose();
  }

  String _time(Duration duration) {
    final seconds = duration.inSeconds.clamp(0, 35999);
    return '${seconds ~/ 60}:${(seconds % 60).toString().padLeft(2, '0')}';
  }

  Future<void> _toggle() async {
    final current = player;
    if (current == null || !current.value.isInitialized) return;
    try {
      if (current.value.isPlaying) {
        await current.pause();
      } else {
        await current.play();
      }
      if (mounted) setState(() => autoplayDenied = false);
    } catch (_) {
      if (mounted) setState(() => autoplayDenied = true);
    }
  }

  void _confirm({bool asFile = false}) {
    if (confirming) return;
    confirming = true;
    Navigator.pop(
      context,
      VideoPreviewResult(
        durationSeconds: ((player?.value.duration.inMilliseconds ?? 0) / 1000)
            .ceil(),
        asFile: asFile,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final current = player;
    final ready =
        !loading && error == null && current?.value.isInitialized == true;
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(
          widget.sendMode ? '预览视频' : widget.title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            tooltip: fullScreen ? '退出全屏' : '全屏',
            icon: Icon(fullScreen ? Icons.fullscreen_exit : Icons.fullscreen),
            onPressed: () async {
              final next = !fullScreen;
              try {
                await setVideoFullscreen(next);
                if (mounted) setState(() => fullScreen = next);
              } catch (_) {}
            },
          ),
        ],
      ),
      body: SafeArea(
        child: Column(
          children: [
            Expanded(
              child: Center(
                child: error != null
                    ? Padding(
                        padding: const EdgeInsets.all(24),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            const Icon(
                              Icons.error_outline,
                              color: Colors.white,
                            ),
                            const SizedBox(height: 12),
                            Text(
                              error!,
                              style: const TextStyle(color: Colors.white),
                              textAlign: TextAlign.center,
                            ),
                            TextButton(
                              onPressed: () {
                                refreshedAfterError = false;
                                unawaited(_initialize());
                              },
                              child: const Text('重试'),
                            ),
                            if (widget.sendMode)
                              TextButton(
                                key: const Key('video-fallback-file'),
                                onPressed: () => _confirm(asFile: true),
                                child: const Text('作为文件发送'),
                              ),
                          ],
                        ),
                      )
                    : !ready
                    ? const CircularProgressIndicator(color: Colors.white)
                    : GestureDetector(
                        key: const Key('video-player-surface'),
                        onTap: _toggle,
                        child: Stack(
                          alignment: Alignment.center,
                          children: [
                            AspectRatio(
                              aspectRatio: current!.value.aspectRatio > 0
                                  ? current.value.aspectRatio
                                  : 16 / 9,
                              child: VideoPlayer(current),
                            ),
                            if (current.value.isBuffering)
                              const CircularProgressIndicator(
                                color: Colors.white,
                              ),
                            if (!current.value.isPlaying)
                              const Icon(
                                Icons.play_circle_fill,
                                color: Colors.white,
                                size: 64,
                              ),
                          ],
                        ),
                      ),
              ),
            ),
            if (autoplayDenied)
              const Text('点击播放按钮开始播放', style: TextStyle(color: Colors.white70)),
            if (ready)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
                child: Row(
                  children: [
                    IconButton(
                      color: Colors.white,
                      onPressed: _toggle,
                      tooltip: current!.value.isPlaying ? '暂停' : '播放',
                      icon: Icon(
                        current.value.isPlaying
                            ? Icons.pause
                            : Icons.play_arrow,
                      ),
                    ),
                    Expanded(
                      child: VideoProgressIndicator(
                        current,
                        allowScrubbing: true,
                        colors: const VideoProgressColors(
                          playedColor: Colors.white,
                          bufferedColor: Colors.white38,
                          backgroundColor: Colors.white24,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      '${_time(current.value.position)} / ${_time(current.value.duration)}',
                      style: const TextStyle(color: Colors.white70),
                    ),
                  ],
                ),
              ),
            if (widget.sendMode)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 6, 16, 18),
                child: Row(
                  children: [
                    Expanded(
                      child: TextButton(
                        key: const Key('cancel-video-send'),
                        onPressed: () => Navigator.pop(context),
                        child: const Text('取消'),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: FilledButton(
                        key: const Key('confirm-video-send'),
                        onPressed: ready && !confirming ? _confirm : null,
                        child: const Text('使用视频'),
                      ),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

String videoPlaybackError(String cause) {
  final text = cause.toLowerCase();
  if (text.contains('404') ||
      text.contains('not_found') ||
      text.contains('forbidden') ||
      text.contains('unavailable')) {
    return '视频已不可用或无权访问';
  }
  if (text.contains('401') || text.contains('登录')) return '登录已失效，请重新登录后播放';
  if (text.contains('403') || text.contains('expired')) return '视频地址已失效，请重试';
  if (text.contains('network') ||
      text.contains('timeout') ||
      text.contains('connection')) {
    return '视频加载失败，请检查网络后重试';
  }
  return '当前设备无法播放此视频，可能是格式不支持或文件损坏';
}
