import 'dart:async';
import 'package:flutter/material.dart';
import '../../core/models.dart';
import '../../core/media_access.dart';
import 'media_send_widgets.dart';

class VideoSendProgress extends StatelessWidget {
  const VideoSendProgress({
    super.key,
    required this.progress,
    required this.clientMessageId,
  });
  final double progress;
  final String clientMessageId;
  @override
  Widget build(BuildContext context) {
    final label = progress >= 1
        ? '等待消息确认'
        : '上传 ${(progress.clamp(0, 1) * 100).round()}%';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        LinearProgressIndicator(
          key: Key('media-upload-progress-$clientMessageId'),
          value: progress.clamp(0, 1),
          minHeight: 3,
        ),
        const SizedBox(height: 4),
        Text(label, style: const TextStyle(fontSize: 11)),
      ],
    );
  }
}

class VideoMessageCard extends StatefulWidget {
  const VideoMessageCard({
    super.key,
    required this.message,
    required this.color,
    this.resolveMedia,
    this.visibilityChanges,
    this.isVisible,
  });
  final ChatMessage message;
  final Color color;
  final Future<ChatMessage> Function(ChatMessage)? resolveMedia;
  final Listenable? visibilityChanges;
  final bool Function()? isVisible;
  @override
  State<VideoMessageCard> createState() => _VideoMessageCardState();
}

class _VideoMessageCardState extends State<VideoMessageCard> {
  String? refreshedCover;
  bool attemptedCoverRefresh = false, opening = false;
  @override
  void didUpdateWidget(VideoMessageCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.message.coverUrl != widget.message.coverUrl ||
        oldWidget.message.id != widget.message.id) {
      refreshedCover = null;
      attemptedCoverRefresh = false;
    }
  }

  void _refreshCover() {
    if (mediaAccess.url(widget.message.mediaId, cover: true) != null) return;
    if (attemptedCoverRefresh ||
        widget.resolveMedia == null ||
        widget.message.mediaId == null) {
      return;
    }
    attemptedCoverRefresh = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      final id = widget.message.id;
      try {
        final current = await widget.resolveMedia!(widget.message);
        if (mounted && widget.message.id == id) {
          setState(() => refreshedCover = current.coverUrl);
        }
      } catch (_) {
        /* A missing poster must not prevent opening the player. */
      }
    });
  }

  Future<void> _open() async {
    if (opening) return;
    opening = true;
    try {
      final source =
          mediaAccess.source(widget.message.mediaId, widget.message.mediaUrl) ??
          '';
      await showMessageVideo(
        context,
        source: source,
        title: widget.message.fileName ?? '视频消息',
        visibilityChanges: widget.visibilityChanges,
        isVisible: widget.isVisible,
        resolveSource:
            !mediaAccess.owns(source) &&
                widget.message.mediaId != null &&
                widget.resolveMedia != null
            ? () async =>
                  (await widget.resolveMedia!(widget.message)).mediaUrl ?? ''
            : null,
      );
    } finally {
      opening = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final message = widget.message;
    final cover =
        mediaAccess.url(message.mediaId, cover: true) ??
        refreshedCover ??
        message.coverUrl;
    final playable =
        message.mediaId?.isNotEmpty == true ||
        message.mediaUrl?.isNotEmpty == true;
    final seconds = message.durationSeconds ?? 0;
    final width = message.mediaWidth ?? 16, height = message.mediaHeight ?? 9;
    final ratio = (width / (height > 0 ? height : 9)).clamp(.65, 1.8);
    Widget placeholder() => ColoredBox(
      color: widget.color.withValues(alpha: .10),
      child: const Center(child: Icon(Icons.videocam_outlined, size: 38)),
    );
    return Semantics(
      button: playable,
      label: '视频消息，$seconds 秒，点击播放',
      child: InkWell(
        key: Key('play-video-${message.id}'),
        onTap: playable ? _open : null,
        borderRadius: BorderRadius.circular(12),
        child: SizedBox(
          width: 240,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(12),
                child: AspectRatio(
                  aspectRatio: ratio,
                  child: Stack(
                    fit: StackFit.expand,
                    children: [
                      if (cover?.isNotEmpty == true)
                        Image.network(
                          cover!,
                          headers: mediaAccess.headersFor(cover),
                          fit: BoxFit.cover,
                          errorBuilder: (_, _, _) {
                            _refreshCover();
                            return placeholder();
                          },
                        )
                      else
                        placeholder(),
                      const Center(
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            color: Colors.black45,
                            shape: BoxShape.circle,
                          ),
                          child: Padding(
                            padding: EdgeInsets.all(8),
                            child: Icon(
                              Icons.play_arrow,
                              color: Colors.white,
                              size: 34,
                            ),
                          ),
                        ),
                      ),
                      Positioned(
                        right: 8,
                        bottom: 8,
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            color: Colors.black54,
                            borderRadius: BorderRadius.circular(5),
                          ),
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 6,
                              vertical: 2,
                            ),
                            child: Text(
                              seconds > 0
                                  ? '${seconds ~/ 60}:${(seconds % 60).toString().padLeft(2, '0')}'
                                  : '视频',
                              style: const TextStyle(
                                color: Colors.white,
                                fontSize: 12,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              if (message.fileName?.isNotEmpty == true)
                Padding(
                  padding: const EdgeInsets.only(top: 5),
                  child: Text(
                    message.fileName!,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: widget.color.withValues(alpha: .65),
                      fontSize: 11,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
