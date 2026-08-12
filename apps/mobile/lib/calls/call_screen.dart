import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:livekit_client/livekit_client.dart';

import '../core/app_theme.dart';
import '../ui/widgets/linli_widgets.dart';
import 'call_controller.dart';
import 'call_media_engine.dart';
import 'call_models.dart';

class CallUiHost extends StatefulWidget {
  const CallUiHost({super.key, required this.controller, required this.child});

  final CallController? controller;
  final Widget child;

  @override
  State<CallUiHost> createState() => _CallUiHostState();
}

class _CallUiHostState extends State<CallUiHost> with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    widget.controller?.handleAppLifecycle(state);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.controller;
    if (controller == null) return widget.child;
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) => Stack(
        fit: StackFit.expand,
        children: [
          widget.child,
          if (controller.isVisible) CallScreen(controller: controller),
        ],
      ),
    );
  }
}

class CallScreen extends StatelessWidget {
  const CallScreen({super.key, required this.controller});

  final CallController controller;

  @override
  Widget build(BuildContext context) {
    final video = controller.isVideo;
    return Material(
      key: const Key('call-screen'),
      color: const Color(0xFF07101F),
      child: Stack(
        fit: StackFit.expand,
        children: [
          if (video) _VideoStage(controller: controller),
          if (!video) _AudioBackdrop(controller: controller),
          const DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                colors: [
                  Color(0x7A020617),
                  Color(0x00020617),
                  Color(0xB5020617),
                ],
                stops: [0, .48, 1],
              ),
            ),
          ),
          SafeArea(
            child: LayoutBuilder(
              builder: (context, constraints) => SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(24, 28, 24, 30),
                child: ConstrainedBox(
                  constraints: BoxConstraints(
                    minHeight: constraints.maxHeight - 58,
                  ),
                  child: IntrinsicHeight(
                    child: Column(
                      children: [
                        _CallHeading(controller: controller),
                        const Spacer(),
                        if (controller.phase == CallPhase.failed)
                          _CallFailurePanel(controller: controller)
                        else if (controller.phase == CallPhase.incoming)
                          _IncomingActions(controller: controller)
                        else
                          _OngoingActions(controller: controller),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _VideoStage extends StatelessWidget {
  const _VideoStage({required this.controller});
  final CallController controller;

  @override
  Widget build(BuildContext context) {
    final remotes = controller.remoteVideos;
    final local = controller.localVideoTrack;
    return Stack(
      fit: StackFit.expand,
      children: [
        if (remotes.isNotEmpty)
          _RemoteVideoStage(videos: remotes)
        else
          _AudioBackdrop(controller: controller),
        if (local != null && controller.cameraEnabled)
          Positioned(
            top: MediaQuery.paddingOf(context).top + 72,
            right: 18,
            width: 108,
            height: 156,
            child: ClipRRect(
              borderRadius: BorderRadius.circular(20),
              child: DecoratedBox(
                decoration: BoxDecoration(
                  border: Border.all(color: Colors.white24),
                  boxShadow: const [
                    BoxShadow(color: Colors.black38, blurRadius: 18),
                  ],
                ),
                child: VideoTrackRenderer(
                  local,
                  mirrorMode: VideoViewMirrorMode.mirror,
                  fit: VideoViewFit.cover,
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class _RemoteVideoStage extends StatelessWidget {
  const _RemoteVideoStage({required this.videos});

  final List<CallRemoteVideo> videos;

  @override
  Widget build(BuildContext context) {
    if (videos.length == 1) {
      return VideoTrackRenderer(videos.first.track, fit: VideoViewFit.cover);
    }
    final columns = videos.length <= 4 ? 2 : 3;
    return GridView.builder(
      physics: const NeverScrollableScrollPhysics(),
      padding: EdgeInsets.zero,
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: columns,
        childAspectRatio: 9 / 16,
        crossAxisSpacing: 2,
        mainAxisSpacing: 2,
      ),
      itemCount: videos.length,
      itemBuilder: (context, index) {
        final video = videos[index];
        return DecoratedBox(
          decoration: BoxDecoration(
            border: video.isActiveSpeaker
                ? Border.all(color: LinliColors.systemGreen, width: 2)
                : null,
          ),
          child: VideoTrackRenderer(video.track, fit: VideoViewFit.cover),
        );
      },
    );
  }
}

class _AudioBackdrop extends StatelessWidget {
  const _AudioBackdrop({required this.controller});
  final CallController controller;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: const BoxDecoration(
      gradient: RadialGradient(
        center: Alignment(0, -.3),
        radius: 1.05,
        colors: [Color(0xFF253954), Color(0xFF07101F)],
      ),
    ),
    child: Center(
      child: Transform.translate(
        offset: const Offset(0, -42),
        child: PersonAvatar(
          name:
              controller.peer?.name ?? controller.conversation?.title ?? '联系人',
          avatarUrl:
              controller.peer?.avatarUrl ?? controller.conversation?.avatarUrl,
          size: 108,
        ),
      ),
    ),
  );
}

class _CallHeading extends StatelessWidget {
  const _CallHeading({required this.controller});
  final CallController controller;

  @override
  Widget build(BuildContext context) {
    final status = switch (controller.phase) {
      CallPhase.incoming => controller.isVideo ? '邀请你视频通话' : '邀请你语音通话',
      CallPhase.outgoing => '正在等待对方接听…',
      CallPhase.connecting => '正在建立安全连接…',
      CallPhase.active =>
        controller.session?.isGroup == true
            ? '${controller.participantCount} 人 · ${_duration(controller.elapsed)}'
            : _duration(controller.elapsed),
      CallPhase.ended => controller.errorMessage ?? '通话结束',
      CallPhase.failed => '通话未接通',
      CallPhase.idle => '',
    };
    return Column(
      children: [
        Text(
          controller.peer?.name ?? controller.conversation?.title ?? '邻里联系人',
          textAlign: TextAlign.center,
          maxLines: 2,
          style: const TextStyle(
            color: Colors.white,
            fontSize: 25,
            height: 1.2,
            fontWeight: FontWeight.w600,
            decoration: TextDecoration.none,
          ),
        ),
        const SizedBox(height: 10),
        Text(
          status,
          key: const Key('call-status'),
          textAlign: TextAlign.center,
          maxLines: 3,
          style: const TextStyle(
            color: Color(0xCFFFFFFF),
            fontSize: 14,
            fontWeight: FontWeight.w400,
            decoration: TextDecoration.none,
          ),
        ),
      ],
    );
  }

  static String _duration(Duration duration) {
    final minutes = duration.inMinutes.remainder(60).toString().padLeft(2, '0');
    final seconds = duration.inSeconds.remainder(60).toString().padLeft(2, '0');
    final hours = duration.inHours;
    return hours > 0 ? '$hours:$minutes:$seconds' : '$minutes:$seconds';
  }
}

class _IncomingActions extends StatelessWidget {
  const _IncomingActions({required this.controller});
  final CallController controller;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      Expanded(
        child: _RoundAction(
          key: const Key('reject-call'),
          icon: CupertinoIcons.phone_down_fill,
          label: '拒绝',
          color: const Color(0xFFFF453A),
          onTap: controller.reject,
        ),
      ),
      Expanded(
        child: _RoundAction(
          key: const Key('accept-call'),
          icon: controller.isVideo
              ? CupertinoIcons.video_camera_solid
              : CupertinoIcons.phone_fill,
          label: '接听',
          color: LinliColors.systemGreen,
          onTap: controller.accept,
        ),
      ),
    ],
  );
}

class _CallFailurePanel extends StatelessWidget {
  const _CallFailurePanel({required this.controller});

  final CallController controller;

  @override
  Widget build(BuildContext context) => Center(
    child: ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 420),
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: const Color(0xE6172438),
          borderRadius: BorderRadius.circular(24),
          border: Border.all(color: const Color(0x2EFFFFFF)),
        ),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 22, 20, 18),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                CupertinoIcons.exclamationmark_triangle_fill,
                color: Color(0xFFFFD60A),
                size: 30,
              ),
              const SizedBox(height: 12),
              Text(
                controller.errorMessage ?? '音视频通话暂时不可用',
                key: const Key('call-failure-message'),
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 15,
                  height: 1.45,
                  fontWeight: FontWeight.w500,
                  decoration: TextDecoration.none,
                ),
              ),
              const SizedBox(height: 18),
              SizedBox(
                width: double.infinity,
                height: 48,
                child: FilledButton(
                  key: const Key('close-call-failure'),
                  onPressed: controller.dismissFailure,
                  style: FilledButton.styleFrom(
                    backgroundColor: Colors.white,
                    foregroundColor: const Color(0xFF07101F),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(14),
                    ),
                  ),
                  child: const Text('知道了'),
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _OngoingActions extends StatelessWidget {
  const _OngoingActions({required this.controller});
  final CallController controller;

  @override
  Widget build(BuildContext context) => Column(
    children: [
      if (controller.phase != CallPhase.outgoing) ...[
        Row(
          children: [
            Expanded(
              child: _RoundAction(
                icon: controller.muted
                    ? CupertinoIcons.mic_slash_fill
                    : CupertinoIcons.mic_fill,
                label: controller.muted ? '取消静音' : '静音',
                selected: controller.muted,
                onTap: controller.toggleMute,
              ),
            ),
            Expanded(
              child: _RoundAction(
                icon: controller.speakerEnabled
                    ? CupertinoIcons.speaker_2_fill
                    : CupertinoIcons.speaker_fill,
                label: '扬声器',
                selected: controller.speakerEnabled,
                onTap: controller.toggleSpeaker,
              ),
            ),
            if (controller.isVideo)
              Expanded(
                child: _RoundAction(
                  icon: controller.cameraEnabled
                      ? CupertinoIcons.video_camera_solid
                      : CupertinoIcons.video_camera,
                  label: '摄像头',
                  selected: !controller.cameraEnabled,
                  onTap: controller.toggleCamera,
                ),
              ),
          ],
        ),
        if (controller.isVideo) ...[
          const SizedBox(height: 20),
          TextButton.icon(
            onPressed: controller.switchCamera,
            style: TextButton.styleFrom(foregroundColor: Colors.white),
            icon: const Icon(CupertinoIcons.camera_rotate, size: 19),
            label: const Text('切换摄像头'),
          ),
        ],
        if (controller.supportsScreenShare) ...[
          const SizedBox(height: 12),
          TextButton.icon(
            key: const Key('toggle-screen-share'),
            onPressed: controller.toggleScreenShare,
            style: TextButton.styleFrom(foregroundColor: Colors.white),
            icon: Icon(
              controller.screenShareEnabled
                  ? CupertinoIcons.stop_circle
                  : CupertinoIcons.rectangle_on_rectangle,
              size: 19,
            ),
            label: Text(controller.screenShareEnabled ? '停止共享' : '共享屏幕'),
          ),
        ],
        const SizedBox(height: 30),
      ],
      _RoundAction(
        key: const Key('end-call'),
        icon: CupertinoIcons.phone_down_fill,
        label: controller.phase == CallPhase.outgoing ? '取消' : '挂断',
        color: const Color(0xFFFF453A),
        onTap: controller.end,
      ),
    ],
  );
}

class _RoundAction extends StatelessWidget {
  const _RoundAction({
    super.key,
    required this.icon,
    required this.label,
    required this.onTap,
    this.color,
    this.selected = false,
  });

  final IconData icon;
  final String label;
  final Future<void> Function() onTap;
  final Color? color;
  final bool selected;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    label: label,
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Material(
          color: color ?? (selected ? Colors.white : const Color(0x35FFFFFF)),
          shape: const CircleBorder(),
          child: InkWell(
            customBorder: const CircleBorder(),
            onTap: () => onTap(),
            child: SizedBox.square(
              dimension: 62,
              child: Icon(
                icon,
                color: selected && color == null
                    ? const Color(0xFF111827)
                    : Colors.white,
                size: 27,
              ),
            ),
          ),
        ),
        const SizedBox(height: 9),
        Text(
          label,
          textAlign: TextAlign.center,
          maxLines: 2,
          style: const TextStyle(
            color: Colors.white,
            fontSize: 12,
            fontWeight: FontWeight.w500,
            decoration: TextDecoration.none,
          ),
        ),
      ],
    ),
  );
}
