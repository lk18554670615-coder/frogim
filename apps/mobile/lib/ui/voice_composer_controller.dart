import 'dart:async';
import 'dart:io';

import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';
import 'package:record/record.dart';

import '../core/models.dart';

enum VoiceComposerPhase {
  idle,
  preparing,
  recording,
  canceling,
  processing,
  preview,
  submitting,
}

enum VoiceComposerNotice {
  permissionDenied,
  startFailed,
  saveFailed,
  tooShort,
  previewFailed,
  sendFailed,
}

bool voiceRecordingShouldCancel(double offset) => offset <= -64;
bool voiceRecordingIsTooShort(Duration duration) =>
    duration < const Duration(milliseconds: 800);
int voiceRecordingDurationSeconds(Duration duration) =>
    ((duration.inMilliseconds + 999) ~/ 1000).clamp(1, 60);

double normalizedVoiceAmplitude(double decibels) =>
    decibels.isFinite ? ((decibels + 60) / 60).clamp(0.0, 1.0) : 0;

/// Owns a recording until it is explicitly handed to the message queue.
/// Waveform updates have their own notifier and never rebuild the composer.
class VoiceComposerController extends ChangeNotifier {
  VoiceComposerController({
    AudioRecorder? recorder,
    AudioPlayer? player,
    DateTime Function()? now,
    Future<Uint8List> Function(String)? readBytes,
  }) : _recorder = recorder ?? AudioRecorder(),
       _player = player ?? AudioPlayer(),
       _now = now ?? DateTime.now,
       _readBytes = readBytes ?? ((path) => File(path).readAsBytes()) {
    _playerComplete = _player.onPlayerComplete.listen((_) {
      if (_disposed) return;
      playing = false;
      notifyListeners();
    });
  }

  final AudioRecorder _recorder;
  final AudioPlayer _player;
  final DateTime Function() _now;
  final Future<Uint8List> Function(String) _readBytes;
  final samples = ValueNotifier<List<double>>(const []);
  late final StreamSubscription<void> _playerComplete;
  VoiceComposerPhase phase = VoiceComposerPhase.idle;
  VoiceComposerNotice? _notice;
  Timer? _clockTimer;
  Timer? _amplitudeTimer;
  DateTime? _startedAt;
  String? _activePath;
  String? _draftPath;
  bool _pressing = false;
  bool _disposed = false;
  bool _sampling = false;
  bool _previewBusy = false;
  bool _discarding = false;
  int _generation = 0;
  int seconds = 0;
  int draftSeconds = 0;
  bool playing = false;

  bool get recording =>
      phase == VoiceComposerPhase.recording ||
      phase == VoiceComposerPhase.canceling;
  bool get canceling => phase == VoiceComposerPhase.canceling;
  bool get hasDraft => _draftPath != null;
  bool get busy =>
      phase == VoiceComposerPhase.preparing ||
      phase == VoiceComposerPhase.processing ||
      phase == VoiceComposerPhase.submitting;
  bool get previewBusy => _previewBusy || _discarding;

  VoiceComposerNotice? takeNotice() {
    final notice = _notice;
    _notice = null;
    return notice;
  }

  void _setPhase(VoiceComposerPhase value, [VoiceComposerNotice? notice]) {
    if (_disposed) return;
    phase = value;
    _notice = notice;
    notifyListeners();
  }

  Future<void> beginPress() async {
    if (_disposed || phase != VoiceComposerPhase.idle) return;
    _pressing = true;
    final generation = ++_generation;
    _setPhase(VoiceComposerPhase.preparing);
    try {
      final allowed = await _recorder.hasPermission();
      if (_disposed || generation != _generation) return;
      if (!allowed) {
        _pressing = false;
        _setPhase(
          VoiceComposerPhase.idle,
          VoiceComposerNotice.permissionDenied,
        );
        return;
      }
      if (!_pressing) {
        _setPhase(VoiceComposerPhase.idle);
        return;
      }
      final path =
          '${Directory.systemTemp.path}/linli-im-voice-${_now().microsecondsSinceEpoch}-$generation.m4a';
      _activePath = path;
      await _recorder.start(
        const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 64000),
        path: path,
      );
      if (_disposed || !_pressing || generation != _generation) {
        await _cancelCapture(path);
        _activePath = null;
        _setPhase(VoiceComposerPhase.idle);
        return;
      }
      _startedAt = _now();
      seconds = 0;
      samples.value = const [];
      _setPhase(VoiceComposerPhase.recording);
      _clockTimer = Timer.periodic(const Duration(seconds: 1), (_) {
        if (_disposed || !recording) return;
        final elapsed = _now().difference(_startedAt!);
        final next = elapsed.inSeconds.clamp(0, 60);
        if (seconds != next) {
          seconds = next;
          notifyListeners();
        }
        if (elapsed >= const Duration(seconds: 60)) unawaited(endPress());
      });
      // record's onAmplitudeChanged uses an async Timer callback without an
      // error handler. Poll the same public API here so unsupported meters or
      // platform errors degrade to a static indicator, not uncaught errors.
      _amplitudeTimer = Timer.periodic(
        const Duration(milliseconds: 100),
        (_) => unawaited(_sample(generation)),
      );
    } catch (_) {
      await _cancelCapture(_activePath);
      _activePath = null;
      _pressing = false;
      _stopTimers();
      _setPhase(VoiceComposerPhase.idle, VoiceComposerNotice.startFailed);
    }
  }

  Future<void> _sample(int generation) async {
    if (_sampling || _disposed || !recording) return;
    _sampling = true;
    try {
      final amplitude = await _recorder.getAmplitude();
      if (_disposed || !recording || generation != _generation) return;
      final previous = samples.value;
      final target = normalizedVoiceAmplitude(amplitude.current);
      final smoothed = previous.isEmpty
          ? target
          : previous.last * .45 + target * .55;
      samples.value = [
        ...previous.skip(previous.length >= 24 ? 1 : 0),
        smoothed,
      ];
    } catch (_) {
      if (!_disposed && recording && generation == _generation) {
        samples.value = const [];
        _amplitudeTimer?.cancel();
      }
    } finally {
      _sampling = false;
    }
  }

  bool updateDrag(double offset) {
    if (!recording) return false;
    final next = voiceRecordingShouldCancel(offset)
        ? VoiceComposerPhase.canceling
        : VoiceComposerPhase.recording;
    if (phase == next) return false;
    _setPhase(next);
    return true;
  }

  Future<void> endPress({bool forceCancel = false}) async {
    _pressing = false;
    if (_disposed || !recording) return;
    final cancel = forceCancel || canceling;
    // Freeze at release, not after the platform finishes saving the file.
    final elapsed = _now().difference(_startedAt!);
    final fallbackPath = _activePath;
    _stopTimers();
    _setPhase(VoiceComposerPhase.processing);
    String? path;
    try {
      path = await _recorder.stop();
      path ??= fallbackPath;
      if (path == null ||
          !await File(path).exists() ||
          await File(path).length() == 0) {
        throw const FileSystemException('Recording file is unavailable');
      }
      if (_disposed || cancel || voiceRecordingIsTooShort(elapsed)) {
        await _deleteFile(path);
        _setPhase(
          VoiceComposerPhase.idle,
          !cancel && !_disposed ? VoiceComposerNotice.tooShort : null,
        );
      } else {
        _draftPath = path;
        draftSeconds = voiceRecordingDurationSeconds(elapsed);
        _setPhase(VoiceComposerPhase.preview);
      }
    } catch (_) {
      await _deleteFile(path ?? fallbackPath);
      _setPhase(
        VoiceComposerPhase.idle,
        cancel ? null : VoiceComposerNotice.saveFailed,
      );
    } finally {
      _activePath = null;
      _startedAt = null;
    }
  }

  Future<void> togglePreview() async {
    if (_disposed || phase != VoiceComposerPhase.preview || previewBusy) return;
    _previewBusy = true;
    notifyListeners();
    try {
      if (playing) {
        await _player.stop();
        playing = false;
      } else {
        await _player.play(DeviceFileSource(_draftPath!));
        playing = true;
      }
    } catch (_) {
      playing = false;
      _notice = VoiceComposerNotice.previewFailed;
    } finally {
      _previewBusy = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> discard() async {
    if (_disposed || phase != VoiceComposerPhase.preview || previewBusy) return;
    _discarding = true;
    notifyListeners();
    final path = _draftPath;
    try {
      await _player.stop();
    } catch (_) {
      // Discarding must still work when the preview player fails.
    }
    await _deleteFile(path);
    _draftPath = null;
    draftSeconds = 0;
    playing = false;
    _discarding = false;
    _setPhase(VoiceComposerPhase.idle);
  }

  Future<void> send(FutureOr<void> Function(MediaUpload) onReady) async {
    if (_disposed || phase != VoiceComposerPhase.preview || previewBusy) return;
    final path = _draftPath!;
    _setPhase(VoiceComposerPhase.submitting);
    try {
      final bytes = await _readBytes(path);
      if (bytes.isEmpty) throw const FileSystemException('Empty recording');
      await _player.stop();
      if (_disposed) {
        await _deleteFile(path);
        return;
      }
      await onReady(
        MediaUpload(
          bytes: bytes,
          fileName: path.replaceAll('\\', '/').split('/').last,
          mimeType: 'audio/mp4',
          kind: MessageContentKind.voice,
          localPath: path,
          durationSeconds: draftSeconds,
        ),
      );
      // Ownership now belongs to the existing pending-media/retry pipeline.
      _draftPath = null;
      draftSeconds = 0;
      playing = false;
      _setPhase(VoiceComposerPhase.idle);
    } catch (_) {
      if (_disposed) {
        await _deleteFile(path);
      } else {
        playing = false;
        _setPhase(VoiceComposerPhase.preview, VoiceComposerNotice.sendFailed);
      }
    }
  }

  void _stopTimers() {
    _clockTimer?.cancel();
    _amplitudeTimer?.cancel();
  }

  Future<void> _deleteFile(String? path) async {
    if (path == null) return;
    try {
      final file = File(path);
      if (await file.exists()) await file.delete();
    } catch (_) {
      // Temporary-file cleanup must never interrupt the UI or send handoff.
    }
  }

  Future<void> _cancelCapture(String? path) async {
    try {
      await _recorder.cancel();
    } catch (_) {}
    await _deleteFile(path);
  }

  @override
  void dispose() {
    _disposed = true;
    _pressing = false;
    _stopTimers();
    unawaited(_playerComplete.cancel());
    // A pending submission decides whether the queue or cleanup owns the file.
    if (phase != VoiceComposerPhase.submitting) {
      unawaited(_deleteFile(_draftPath));
    }
    unawaited(_disposeCapture());
    unawaited(_player.dispose());
    samples.dispose();
    super.dispose();
  }

  Future<void> _disposeCapture() async {
    await _cancelCapture(_activePath);
    await _recorder.dispose();
  }
}
