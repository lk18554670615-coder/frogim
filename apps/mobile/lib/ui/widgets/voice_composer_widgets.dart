import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../../core/app_theme.dart';
import '../voice_composer_controller.dart';

class VoiceRecordingOverlay extends StatelessWidget {
  const VoiceRecordingOverlay({
    super.key,
    required this.phase,
    required this.seconds,
    required this.samples,
  });

  final VoiceComposerPhase phase;
  final int seconds;
  final ValueListenable<List<double>> samples;

  @override
  Widget build(BuildContext context) {
    final visible = switch (phase) {
      VoiceComposerPhase.preparing ||
      VoiceComposerPhase.recording ||
      VoiceComposerPhase.canceling ||
      VoiceComposerPhase.processing => true,
      _ => false,
    };
    final canceling = phase == VoiceComposerPhase.canceling;
    final active = phase == VoiceComposerPhase.recording || canceling;
    final reduced = MediaQuery.disableAnimationsOf(context);
    final color = canceling
        ? (Theme.of(context).brightness == Brightness.dark
              ? const Color(0xFFFF758B)
              : LinliColors.systemRed)
        : Theme.of(context).brightness == Brightness.dark
        ? LinliColors.brandYellow
        : LinliColors.brandInk;
    final title = switch (phase) {
      VoiceComposerPhase.preparing => '正在准备录音',
      VoiceComposerPhase.processing => '正在处理语音',
      VoiceComposerPhase.canceling => '松开取消',
      _ => '正在录音',
    };
    return IgnorePointer(
      child: AnimatedSwitcher(
        duration: nexaMotionDuration(context),
        transitionBuilder: (child, animation) => FadeTransition(
          opacity: animation,
          child: SlideTransition(
            position:
                Tween<Offset>(
                  begin: reduced ? Offset.zero : const Offset(0, .06),
                  end: Offset.zero,
                ).animate(
                  CurvedAnimation(
                    parent: animation,
                    curve: Curves.easeOutCubic,
                  ),
                ),
            child: child,
          ),
        ),
        child: !visible
            ? const SizedBox.shrink(key: ValueKey('voice-overlay-hidden'))
            : AnimatedContainer(
                key: const Key('voice-recording-overlay'),
                duration: nexaMotionDuration(context),
                constraints: const BoxConstraints(maxWidth: 360),
                padding: const EdgeInsets.symmetric(
                  horizontal: 18,
                  vertical: 14,
                ),
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surface,
                  borderRadius: BorderRadius.circular(20),
                  border: Border.all(color: color.withValues(alpha: .25)),
                  boxShadow: [
                    BoxShadow(
                      color: Colors.black.withValues(alpha: .10),
                      blurRadius: 20,
                      offset: const Offset(0, 6),
                    ),
                  ],
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Row(
                      children: [
                        if (active || reduced)
                          Icon(
                            canceling
                                ? CupertinoIcons.xmark_circle_fill
                                : CupertinoIcons.mic_fill,
                            color: color,
                            size: 22,
                          )
                        else
                          CupertinoActivityIndicator(color: color, radius: 10),
                        const SizedBox(width: 10),
                        Expanded(
                          child: Text(
                            title,
                            style: Theme.of(
                              context,
                            ).textTheme.titleSmall?.copyWith(color: color),
                          ),
                        ),
                        if (active) ...[
                          const SizedBox(width: 8),
                          Text(
                            '${seconds.toString().padLeft(2, '0')}″',
                            key: const Key('voice-recording-duration'),
                            style: Theme.of(
                              context,
                            ).textTheme.titleSmall?.copyWith(color: color),
                          ),
                        ],
                      ],
                    ),
                    if (active) ...[
                      const SizedBox(height: 10),
                      RepaintBoundary(
                        child: VoiceWaveform(samples: samples, color: color),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        canceling ? '松开手指，取消这段录音' : '上滑取消 · 松开后可试听',
                        textAlign: TextAlign.center,
                        style: Theme.of(
                          context,
                        ).textTheme.bodySmall?.copyWith(color: color),
                      ),
                    ],
                  ],
                ),
              ),
      ),
    );
  }
}

class VoiceWaveform extends StatelessWidget {
  const VoiceWaveform({super.key, required this.samples, required this.color});
  final ValueListenable<List<double>> samples;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final reduced = MediaQuery.disableAnimationsOf(context);
    return ExcludeSemantics(
      child: SizedBox(
        key: const Key('voice-waveform'),
        height: 28,
        child: ValueListenableBuilder<List<double>>(
          valueListenable: samples,
          builder: (context, values, _) => Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              for (var index = 0; index < 24; index++)
                Flexible(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 2),
                    child: AnimatedContainer(
                      key: ValueKey('voice-level-$index'),
                      duration: reduced
                          ? Duration.zero
                          : const Duration(milliseconds: 100),
                      curve: Curves.easeOut,
                      width: 4,
                      height:
                          3 +
                          (reduced || index < 24 - values.length
                              ? 0
                              : values[index - (24 - values.length)].clamp(
                                      0,
                                      1,
                                    ) *
                                    25),
                      decoration: BoxDecoration(
                        color: color.withValues(alpha: .8),
                        borderRadius: BorderRadius.circular(4),
                      ),
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

class VoiceRecordingButton extends StatelessWidget {
  const VoiceRecordingButton({
    super.key,
    required this.phase,
    required this.onStart,
    required this.onMove,
    required this.onEnd,
    required this.onCancel,
  });
  final VoiceComposerPhase phase;
  final GestureLongPressStartCallback onStart;
  final GestureLongPressMoveUpdateCallback onMove;
  final GestureLongPressEndCallback onEnd;
  final VoidCallback onCancel;

  @override
  Widget build(BuildContext context) {
    final canceling = phase == VoiceComposerPhase.canceling;
    final recording = phase == VoiceComposerPhase.recording || canceling;
    final text = switch (phase) {
      VoiceComposerPhase.preparing => '正在准备…',
      VoiceComposerPhase.processing => '正在处理…',
      VoiceComposerPhase.canceling => '松开取消',
      VoiceComposerPhase.recording => '松开试听',
      _ => '按住说话',
    };
    return Semantics(
      button: true,
      label: text,
      child: GestureDetector(
        // Never replace this gesture target during an active long press.
        key: const Key('hold-to-talk'),
        behavior: HitTestBehavior.opaque,
        onLongPressStart: onStart,
        onLongPressMoveUpdate: onMove,
        onLongPressEnd: onEnd,
        onLongPressCancel: onCancel,
        child: AnimatedContainer(
          duration: nexaMotionDuration(context),
          constraints: const BoxConstraints(minHeight: 44),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: canceling
                ? LinliColors.systemRed.withValues(alpha: .12)
                : recording
                ? LinliColors.brandYellow.withValues(alpha: .16)
                : Theme.of(context).colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(14),
          ),
          child: Text(
            text,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.labelLarge?.copyWith(
              color: canceling ? LinliColors.systemRed : null,
            ),
          ),
        ),
      ),
    );
  }
}

class VoiceDraftControl extends StatelessWidget {
  const VoiceDraftControl({
    super.key,
    required this.seconds,
    required this.playing,
    required this.busy,
    required this.previewBusy,
    required this.onPreview,
    required this.onDiscard,
    required this.onSend,
  });
  final int seconds;
  final bool playing;
  final bool busy;
  final bool previewBusy;
  final VoidCallback onPreview;
  final VoidCallback onDiscard;
  final VoidCallback onSend;

  @override
  Widget build(BuildContext context) {
    final disabled = busy || previewBusy;
    final preview = IconButton(
      key: const Key('preview-voice-button'),
      tooltip: playing ? '暂停试听' : '试听',
      onPressed: disabled ? null : onPreview,
      icon: AnimatedSwitcher(
        duration: nexaMotionDuration(context),
        child: Icon(
          playing ? CupertinoIcons.pause_fill : CupertinoIcons.play_fill,
          key: ValueKey(playing),
          size: 18,
        ),
      ),
    );
    final discard = IconButton(
      key: const Key('discard-voice-button'),
      tooltip: '丢弃语音',
      onPressed: disabled ? null : onDiscard,
      icon: const Icon(CupertinoIcons.xmark, size: 18),
    );
    final send = _VoiceSendButton(
      busy: busy,
      onPressed: disabled ? null : onSend,
    );
    final label = Text(
      busy ? '正在准备…' : '$seconds 秒语音',
      style: Theme.of(context).textTheme.labelLarge,
    );
    return Container(
      key: const Key('voice-draft'),
      padding: const EdgeInsets.symmetric(horizontal: 6),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(14),
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final compact =
              constraints.maxWidth < 220 ||
              MediaQuery.textScalerOf(context).scale(14) > 20;
          if (compact) {
            return Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    preview,
                    Expanded(child: label),
                  ],
                ),
                Row(
                  mainAxisAlignment: MainAxisAlignment.end,
                  children: [discard, send],
                ),
              ],
            );
          }
          return Row(
            children: [
              preview,
              Expanded(child: label),
              discard,
              send,
            ],
          );
        },
      ),
    );
  }
}

class _VoiceSendButton extends StatefulWidget {
  const _VoiceSendButton({required this.busy, this.onPressed});
  final bool busy;
  final VoidCallback? onPressed;
  @override
  State<_VoiceSendButton> createState() => _VoiceSendButtonState();
}

class _VoiceSendButtonState extends State<_VoiceSendButton> {
  bool pressed = false;
  @override
  Widget build(BuildContext context) => Listener(
    onPointerDown: (_) {
      if (widget.onPressed != null) setState(() => pressed = true);
    },
    onPointerUp: (_) => setState(() => pressed = false),
    onPointerCancel: (_) => setState(() => pressed = false),
    child: AnimatedScale(
      scale: pressed && !MediaQuery.disableAnimationsOf(context) ? .90 : 1,
      duration: nexaMotionDuration(context),
      child: IconButton(
        key: const Key('send-voice-button'),
        tooltip: '发送语音',
        onPressed: widget.onPressed,
        icon: widget.busy && !MediaQuery.disableAnimationsOf(context)
            ? const CupertinoActivityIndicator(radius: 10)
            : Icon(
                CupertinoIcons.arrow_up_circle_fill,
                color: Theme.of(context).brightness == Brightness.dark
                    ? LinliColors.brandYellow
                    : LinliColors.brandInk,
                size: 28,
              ),
      ),
    ),
  );
}

/// Only transitions on a real state change; progress updates retain their key.
class VoiceSendFeedback extends StatelessWidget {
  const VoiceSendFeedback({
    super.key,
    required this.animate,
    required this.statusKey,
    required this.child,
  });
  final bool animate;
  final Object statusKey;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    if (!animate) return child;
    return AnimatedSwitcher(
      duration: nexaMotionDuration(context),
      layoutBuilder: (current, previous) => Stack(
        alignment: Alignment.center,
        children: [
          for (final old in previous)
            IgnorePointer(child: ExcludeSemantics(child: old)),
          ?current,
        ],
      ),
      child: KeyedSubtree(key: ValueKey(statusKey), child: child),
    );
  }
}

/// Avoid a zero-duration AnimatedSize: Flutter can re-dirty it during layout.
/// Reduced motion should simply lay out the new size without an animation.
class VoiceSizeTransition extends StatelessWidget {
  const VoiceSizeTransition({
    super.key,
    this.alignment = Alignment.center,
    required this.child,
  });
  final AlignmentGeometry alignment;
  final Widget child;

  @override
  Widget build(BuildContext context) => MediaQuery.disableAnimationsOf(context)
      ? child
      : AnimatedSize(
          duration: nexaMotionDuration(context),
          alignment: alignment,
          child: child,
        );
}

class VoiceUploadProgress extends StatelessWidget {
  const VoiceUploadProgress({
    super.key,
    required this.progress,
    required this.clientMessageId,
  });
  final double? progress;
  final String clientMessageId;

  @override
  Widget build(BuildContext context) => VoiceSizeTransition(
    child: VoiceSendFeedback(
      animate: true,
      statusKey: progress == null,
      child: progress == null
          ? const SizedBox.shrink()
          : Padding(
              padding: const EdgeInsets.only(top: 6),
              child: SizedBox(
                width: 150,
                child: Semantics(
                  label: '上传 ${(progress! * 100).round()}%',
                  child: LinearProgressIndicator(
                    key: Key('media-upload-progress-$clientMessageId'),
                    value: progress,
                    minHeight: 3,
                    borderRadius: BorderRadius.circular(999),
                    color: LinliColors.brandYellow,
                    backgroundColor: Theme.of(
                      context,
                    ).colorScheme.surfaceContainerHighest,
                  ),
                ),
              ),
            ),
    ),
  );
}

class VoiceMessageEntrance extends StatefulWidget {
  const VoiceMessageEntrance({
    super.key,
    required this.animate,
    required this.child,
  });
  final bool animate;
  final Widget child;
  @override
  State<VoiceMessageEntrance> createState() => _VoiceMessageEntranceState();
}

class _VoiceMessageEntranceState extends State<VoiceMessageEntrance>
    with SingleTickerProviderStateMixin {
  late final AnimationController _animation = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 220),
    value: 1,
  );
  bool _initialized = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final reduced = MediaQuery.disableAnimationsOf(context);
    if (!_initialized) {
      _initialized = true;
      if (widget.animate && !reduced) _animation.forward(from: 0);
    } else if (reduced) {
      _animation.value = 1;
    }
  }

  @override
  void dispose() {
    _animation.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _animation,
    child: widget.child,
    builder: (context, child) {
      final value = Curves.easeOutCubic.transform(_animation.value);
      return Opacity(
        opacity: value,
        child: Transform.translate(
          offset: Offset(0, 8 * (1 - value)),
          child: child,
        ),
      );
    },
  );
}
