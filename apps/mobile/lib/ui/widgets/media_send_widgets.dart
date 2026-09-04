import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/models.dart';
import '../../core/video_preparation.dart';
import '../../core/video_source_lifecycle.dart';
import 'video_player_screen.dart';
export 'video_player_screen.dart';

class VideoPreviewResult {
  const VideoPreviewResult({
    required this.durationSeconds,
    this.asFile = false,
  });

  final int durationSeconds;
  final bool asFile;
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
              subtitle: Text(
                supportsNativeVideoCompression
                    ? '发送前预览并压缩视频'
                    : '预览后按原格式发送，不进行转码',
              ),
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
    builder: (_) =>
        VideoPlayerScreen(source: source, title: title, sendMode: true),
  ),
);

Future<void> showMessageVideo(
  BuildContext context, {
  required String source,
  required String title,
  Future<String> Function()? resolveSource,
  Listenable? visibilityChanges,
  bool Function()? isVisible,
}) => Navigator.of(context).push<void>(
  MaterialPageRoute(
    builder: (_) => VideoPlayerScreen(
      source: source,
      title: title,
      resolveSource: resolveSource,
      visibilityChanges: visibilityChanges,
      isVisible: isVisible,
    ),
  ),
);

Future<MediaUpload?> prepareVideoUploadWithDialog(
  BuildContext context, {
  required XFile file,
  required int maxBytes,
  required int previewDurationSeconds,
  bool asFile = false,
}) => showDialog<MediaUpload>(
  context: context,
  barrierDismissible: false,
  builder: (_) => _VideoPreparationDialog(
    file: file,
    maxBytes: maxBytes,
    previewDurationSeconds: previewDurationSeconds,
    asFile: asFile,
  ),
);

class _VideoPreparationDialog extends StatefulWidget {
  const _VideoPreparationDialog({
    required this.file,
    required this.maxBytes,
    required this.previewDurationSeconds,
    required this.asFile,
  });

  final XFile file;
  final int maxBytes;
  final int previewDurationSeconds;
  final bool asFile;

  @override
  State<_VideoPreparationDialog> createState() =>
      _VideoPreparationDialogState();
}

class _VideoPreparationDialogState extends State<_VideoPreparationDialog> {
  double progress = 0;
  bool cancelling = false;
  bool busy = false;
  String? errorText;
  bool retryAsFile = false;

  @override
  void initState() {
    super.initState();
    unawaited(_prepare(asFile: widget.asFile));
  }

  Future<void> _prepare({bool asFile = false}) async {
    if (busy) return;
    retryAsFile = asFile;
    setState(() {
      busy = true;
      errorText = null;
    });
    try {
      if (asFile) {
        final upload = await originalVideoFile(widget.file, widget.maxBytes);
        if (mounted && !cancelling) Navigator.pop(context, upload);
        return;
      }
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
      if (!mounted || cancelling) {
        releaseVideoSource(prepared.path);
        return;
      }
      Navigator.of(context).pop(
        MediaUpload(
          bytes: prepared.bytes,
          fileName: prepared.fileName,
          mimeType: prepared.mimeType,
          kind: MessageContentKind.video,
          localPath: prepared.path,
          durationSeconds: prepared.durationSeconds,
          width: prepared.width,
          height: prepared.height,
          coverBytes: prepared.coverBytes,
        ),
      );
    } catch (error) {
      if (!mounted || cancelling) return;
      final message = error
          .toString()
          .replaceFirst('FormatException: ', '')
          .replaceFirst('VideoPreparationCancelled: ', '');
      setState(() => errorText = message);
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> _cancel() async {
    if (cancelling) return;
    setState(() => cancelling = true);
    try {
      await cancelVideoPreparation().timeout(const Duration(seconds: 3));
    } catch (_) {}
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy || cancelling,
    child: AlertDialog(
      title: Text(
        errorText != null
            ? '视频处理失败'
            : supportsNativeVideoCompression
            ? '正在压缩视频'
            : '正在准备视频',
      ),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (errorText == null)
            LinearProgressIndicator(value: progress == 0 ? null : progress),
          const SizedBox(height: 12),
          Text(
            errorText ??
                (cancelling
                    ? '正在取消…'
                    : supportsNativeVideoCompression
                    ? '${(progress * 100).round()}% · 保留声音，最长 5 分钟'
                    : '准备视频与封面，最长 5 分钟'),
          ),
        ],
      ),
      actions: [
        if (errorText != null) ...[
          TextButton(
            onPressed: busy ? null : () => _prepare(asFile: true),
            child: const Text('作为文件发送'),
          ),
          FilledButton(
            onPressed: busy ? null : () => _prepare(asFile: retryAsFile),
            child: const Text('重试'),
          ),
        ],
        TextButton(
          key: const Key('cancel-video-compression'),
          onPressed: cancelling ? null : _cancel,
          child: const Text('取消'),
        ),
      ],
    ),
  );
}

Future<MediaUpload> originalVideoFile(XFile file, int maxBytes) async {
  final bytes = await file.readAsBytes().timeout(const Duration(seconds: 30));
  if (bytes.isEmpty || bytes.length > maxBytes) {
    throw const FormatException('文件为空或超过大小限制');
  }
  return MediaUpload(
    bytes: bytes,
    fileName: file.name,
    // The explicit fallback preserves bytes without claiming they are a
    // decodable video (even a damaged container can be shared as a file).
    mimeType: 'application/octet-stream',
    kind: MessageContentKind.file,
    localPath: file.path,
  );
}

Future<MediaUpload?> prepareSelectedVideo(
  BuildContext context, {
  required XFile file,
  required int maxBytes,
}) async {
  MediaUpload? result;
  try {
    final preview = await showVideoSendPreview(
      context,
      source: file.path,
      title: file.name,
    );
    if (!context.mounted || preview == null) return null;
    result = await prepareVideoUploadWithDialog(
      context,
      file: file,
      maxBytes: maxBytes,
      previewDurationSeconds: preview.durationSeconds,
      asFile: preview.asFile,
    );
    return result;
  } finally {
    if (result == null) releaseVideoSource(file.path);
  }
}
