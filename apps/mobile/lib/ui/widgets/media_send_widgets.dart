import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:video_player/video_player.dart';

import '../../core/models.dart';
import '../../core/video_player_controller_factory.dart';
import '../../core/video_preparation.dart';

class VideoPreviewResult {
  const VideoPreviewResult({required this.durationSeconds});

  final int durationSeconds;
}

Future<ImageSource?> chooseVideoSource(BuildContext context) =>
    showModalBottomSheet<ImageSource>(
      context: context,
      useSafeArea: true,
      showDragHandle: true,
      builder: (context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 22),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('选择视频', style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 12),
            ListTile(
              key: const Key('record-short-video'),
              leading: const Icon(CupertinoIcons.video_camera_solid),
              title: const Text('拍小视频'),
              subtitle: const Text('最长 5 分钟，发送前可预览'),
              onTap: () => Navigator.pop(context, ImageSource.camera),
            ),
            ListTile(
              key: const Key('choose-gallery-video'),
              leading: const Icon(CupertinoIcons.photo_on_rectangle),
              title: const Text('从相册选择'),
              subtitle: const Text('发送前自动压缩为兼容格式'),
              onTap: () => Navigator.pop(context, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );

Future<VideoPreviewResult?> showVideoSendPreview(
  BuildContext context, {
  required String source,
  required String title,
}) => Navigator.of(context).push<VideoPreviewResult>(
  MaterialPageRoute(
    fullscreenDialog: true,
    builder: (_) =>
        VideoPlayerScreen(source: source, title: title, sendMode: true),
  ),
);

Future<void> showMessageVideo(
  BuildContext context, {
  required String source,
  required String title,
}) => Navigator.of(context).push<void>(
  MaterialPageRoute(
    fullscreenDialog: true,
    builder: (_) => VideoPlayerScreen(source: source, title: title),
  ),
);

Future<MediaUpload?> prepareVideoUploadWithDialog(
  BuildContext context, {
  required XFile file,
  required int maxBytes,
  required int previewDurationSeconds,
}) => showDialog<MediaUpload>(
  context: context,
  barrierDismissible: false,
  builder: (_) => _VideoPreparationDialog(
    file: file,
    maxBytes: maxBytes,
    previewDurationSeconds: previewDurationSeconds,
  ),
);

class _VideoPreparationDialog extends StatefulWidget {
  const _VideoPreparationDialog({
    required this.file,
    required this.maxBytes,
    required this.previewDurationSeconds,
  });

  final XFile file;
  final int maxBytes;
  final int previewDurationSeconds;

  @override
  State<_VideoPreparationDialog> createState() =>
      _VideoPreparationDialogState();
}

class _VideoPreparationDialogState extends State<_VideoPreparationDialog> {
  double progress = 0;
  bool cancelling = false;

  @override
  void initState() {
    super.initState();
    unawaited(_prepare());
  }

  Future<void> _prepare() async {
    try {
      final prepared = await prepareVideoForSending(
        path: widget.file.path,
        fileName: widget.file.name,
        readOriginalBytes: widget.file.readAsBytes,
        maxBytes: widget.maxBytes,
        previewDurationSeconds: widget.previewDurationSeconds,
        onProgress: (value) {
          if (mounted && !cancelling) setState(() => progress = value);
        },
      );
      if (!mounted || cancelling) return;
      Navigator.of(context).pop(
        MediaUpload(
          bytes: prepared.bytes,
          fileName: prepared.fileName,
          mimeType: prepared.mimeType,
          kind: MessageContentKind.video,
          localPath: prepared.path,
          durationSeconds: prepared.durationSeconds,
        ),
      );
    } catch (error) {
      if (!mounted || cancelling) return;
      final message = error
          .toString()
          .replaceFirst('FormatException: ', '')
          .replaceFirst('VideoPreparationCancelled: ', '');
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
      Navigator.of(context).pop();
    }
  }

  Future<void> _cancel() async {
    if (cancelling) return;
    setState(() => cancelling = true);
    await cancelVideoPreparation();
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(supportsNativeVideoCompression ? '正在压缩视频' : '正在准备视频'),
    content: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        LinearProgressIndicator(value: progress == 0 ? null : progress),
        const SizedBox(height: 12),
        Text(
          cancelling
              ? '正在取消…'
              : supportsNativeVideoCompression
              ? '${(progress * 100).round()}% · 保留声音，最长 5 分钟'
              : '当前平台不提供原生压缩，将检查文件大小后发送',
        ),
      ],
    ),
    actions: [
      TextButton(
        key: const Key('cancel-video-compression'),
        onPressed: cancelling ? null : _cancel,
        child: const Text('取消'),
      ),
    ],
  );
}

class VideoPlayerScreen extends StatefulWidget {
  const VideoPlayerScreen({
    super.key,
    required this.source,
    required this.title,
    this.sendMode = false,
  });

  final String source;
  final String title;
  final bool sendMode;

  @override
  State<VideoPlayerScreen> createState() => _VideoPlayerScreenState();
}

class _VideoPlayerScreenState extends State<VideoPlayerScreen> {
  late final VideoPlayerController player;
  Object? error;

  @override
  void initState() {
    super.initState();
    player = createVideoPlayerController(widget.source);
    unawaited(_initialize());
  }

  Future<void> _initialize() async {
    try {
      await player.initialize();
      await player.setLooping(true);
      if (mounted) setState(() {});
    } catch (cause) {
      if (mounted) setState(() => error = cause);
    }
  }

  @override
  void dispose() {
    unawaited(player.dispose());
    super.dispose();
  }

  int get _durationSeconds => player.value.isInitialized
      ? player.value.duration.inMilliseconds / 1000 == 0
            ? 0
            : (player.value.duration.inMilliseconds / 1000).ceil()
      : 0;

  String _time(Duration duration) {
    final seconds = duration.inSeconds.clamp(0, 35999);
    final minutes = seconds ~/ 60;
    final remainder = seconds % 60;
    return '$minutes:${remainder.toString().padLeft(2, '0')}';
  }

  Future<void> _toggle() async {
    if (!player.value.isInitialized) return;
    player.value.isPlaying ? await player.pause() : await player.play();
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      backgroundColor: Colors.black,
      foregroundColor: Colors.white,
      title: Text(widget.sendMode ? '预览视频' : widget.title),
    ),
    body: SafeArea(
      child: Column(
        children: [
          Expanded(
            child: Center(
              child: error != null
                  ? const _VideoLoadFailure()
                  : !player.value.isInitialized
                  ? const CircularProgressIndicator(color: Colors.white)
                  : GestureDetector(
                      key: const Key('video-player-surface'),
                      onTap: _toggle,
                      child: Stack(
                        alignment: Alignment.center,
                        children: [
                          AspectRatio(
                            aspectRatio: player.value.aspectRatio == 0
                                ? 16 / 9
                                : player.value.aspectRatio,
                            child: VideoPlayer(player),
                          ),
                          if (!player.value.isPlaying)
                            Container(
                              width: 64,
                              height: 64,
                              decoration: BoxDecoration(
                                color: Colors.black.withValues(alpha: .55),
                                shape: BoxShape.circle,
                              ),
                              child: const Icon(
                                CupertinoIcons.play_fill,
                                size: 32,
                                color: Colors.white,
                              ),
                            ),
                        ],
                      ),
                    ),
            ),
          ),
          if (player.value.isInitialized)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 10),
              child: Row(
                children: [
                  IconButton(
                    color: Colors.white,
                    onPressed: _toggle,
                    icon: Icon(
                      player.value.isPlaying
                          ? CupertinoIcons.pause_fill
                          : CupertinoIcons.play_fill,
                    ),
                  ),
                  Expanded(
                    child: VideoProgressIndicator(
                      player,
                      allowScrubbing: true,
                      colors: const VideoProgressColors(
                        playedColor: Colors.white,
                        bufferedColor: Colors.white38,
                        backgroundColor: Colors.white24,
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Text(
                    '${_time(player.value.position)} / '
                    '${_time(player.value.duration)}',
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
                    child: OutlinedButton(
                      key: const Key('cancel-video-send'),
                      style: OutlinedButton.styleFrom(
                        foregroundColor: Colors.white,
                        side: const BorderSide(color: Colors.white38),
                      ),
                      onPressed: () => Navigator.pop(context),
                      child: const Text('取消'),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: FilledButton(
                      key: const Key('confirm-video-send'),
                      onPressed: error != null || !player.value.isInitialized
                          ? null
                          : () => Navigator.pop(
                              context,
                              VideoPreviewResult(
                                durationSeconds: _durationSeconds,
                              ),
                            ),
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

class _VideoLoadFailure extends StatelessWidget {
  const _VideoLoadFailure();

  @override
  Widget build(BuildContext context) => const Padding(
    padding: EdgeInsets.all(28),
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(CupertinoIcons.exclamationmark_triangle, color: Colors.white),
        SizedBox(height: 12),
        Text('视频无法播放，请重新选择', style: TextStyle(color: Colors.white)),
      ],
    ),
  );
}
