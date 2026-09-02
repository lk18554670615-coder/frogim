import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/voice_composer_controller.dart';

import 'support/voice_fakes.dart';

void main() {
  late FakeVoiceRecorder recorder;
  late FakeVoicePlayer player;
  late VoiceComposerController voice;
  late DateTime now;
  bool disposed = false;

  setUp(() {
    recorder = FakeVoiceRecorder();
    player = FakeVoicePlayer();
    now = DateTime.now();
    disposed = false;
    voice = VoiceComposerController(
      recorder: recorder,
      player: player,
      now: () => now,
    );
  });
  tearDown(() async {
    if (!disposed) voice.dispose();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    final path = recorder.path;
    if (path != null && File(path).existsSync()) File(path).deleteSync();
  });

  Future<void> draft([int milliseconds = 1800]) async {
    await voice.beginPress();
    now = now.add(Duration(milliseconds: milliseconds));
    await voice.endPress();
  }

  test(
    'permission rejection never reports recording or creates a draft',
    () async {
      recorder.permission = false;
      await voice.beginPress();
      expect(voice.phase, VoiceComposerPhase.idle);
      expect(voice.takeNotice(), VoiceComposerNotice.permissionDenied);
      expect(recorder.starts, 0);
      expect(voice.hasDraft, isFalse);
    },
  );

  test(
    'release while waiting for permission does not start recording',
    () async {
      recorder.permissionGate = Completer<bool>();
      final start = voice.beginPress();
      expect(voice.phase, VoiceComposerPhase.preparing);
      await voice.endPress();
      recorder.permissionGate!.complete(true);
      await start;
      expect(recorder.starts, 0);
      expect(voice.phase, VoiceComposerPhase.idle);
    },
  );

  test('release during native start cancels and deletes the capture', () async {
    recorder.startGate = Completer<void>();
    final start = voice.beginPress();
    await Future<void>.delayed(Duration.zero);
    expect(voice.phase, VoiceComposerPhase.preparing);
    await voice.endPress();
    recorder.startGate!.complete();
    await start;
    expect(voice.phase, VoiceComposerPhase.idle);
    expect(recorder.cancels, 1);
    expect(File(recorder.path!).existsSync(), isFalse);
  });

  test('start and save failures leave no empty draft', () async {
    recorder.failStart = true;
    await voice.beginPress();
    expect(voice.takeNotice(), VoiceComposerNotice.startFailed);
    expect(voice.hasDraft, isFalse);
    recorder.failStart = false;
    recorder.failStop = true;
    await draft();
    expect(voice.takeNotice(), VoiceComposerNotice.saveFailed);
    expect(voice.phase, VoiceComposerPhase.idle);
    expect(File(recorder.path!).existsSync(), isFalse);
  });

  test(
    'real amplitude is normalized, smoothed and isolated from phase rebuilds',
    () async {
      var phaseChanges = 0;
      voice.addListener(() => phaseChanges++);
      await voice.beginPress();
      final initial = phaseChanges;
      await Future<void>.delayed(const Duration(milliseconds: 130));
      expect(voice.samples.value.last, 0);
      recorder.decibels = 0;
      await Future<void>.delayed(const Duration(milliseconds: 110));
      expect(voice.samples.value.last, closeTo(.55, .0001));
      expect(phaseChanges, initial);
      recorder.failAmplitude = true;
      await Future<void>.delayed(const Duration(milliseconds: 110));
      expect(voice.samples.value, isEmpty);
      final count = recorder.samples;
      await Future<void>.delayed(const Duration(milliseconds: 120));
      expect(recorder.samples, count);
      await voice.endPress(forceCancel: true);
    },
  );

  test(
    'cancel threshold, slide back, release and too short boundary',
    () async {
      await voice.beginPress();
      expect(voice.updateDrag(-63), isFalse);
      expect(voice.updateDrag(-64), isTrue);
      expect(voice.canceling, isTrue);
      expect(voice.updateDrag(-80), isFalse);
      expect(voice.updateDrag(-20), isTrue);
      now = now.add(const Duration(milliseconds: 799));
      await voice.endPress();
      expect(voice.takeNotice(), VoiceComposerNotice.tooShort);
      expect(voice.hasDraft, isFalse);
      await draft(800);
      expect(voice.phase, VoiceComposerPhase.preview);
      expect(voice.draftSeconds, 1);
      await voice.discard();
      await voice.beginPress();
      now = now.add(const Duration(seconds: 2));
      voice.updateDrag(-80);
      await voice.endPress();
      expect(voice.hasDraft, isFalse);
      expect(voice.takeNotice(), isNull);
    },
  );

  test(
    'processing waits for file and measures duration at finger release',
    () async {
      await voice.beginPress();
      now = now.add(const Duration(milliseconds: 1200));
      recorder.stopGate = Completer<void>();
      final stop = voice.endPress();
      expect(voice.phase, VoiceComposerPhase.processing);
      expect(voice.hasDraft, isFalse);
      now = now.add(const Duration(seconds: 8));
      recorder.stopGate!.complete();
      await stop;
      expect(voice.phase, VoiceComposerPhase.preview);
      expect(voice.draftSeconds, 2);
    },
  );

  test('60 second limit enters preview, never sends automatically', () async {
    await voice.beginPress();
    now = now.add(const Duration(seconds: 60));
    await Future<void>.delayed(const Duration(milliseconds: 1100));
    expect(voice.phase, VoiceComposerPhase.preview);
    expect(voice.draftSeconds, 60);
    expect(recorder.stops, 1);
    await voice.endPress();
    expect(recorder.stops, 1);
  });

  test('preview completion, replay and discard', () async {
    await draft();
    await voice.togglePreview();
    expect(voice.playing, isTrue);
    player.completed.add(null);
    await Future<void>.delayed(Duration.zero);
    expect(voice.playing, isFalse);
    await voice.togglePreview();
    await voice.togglePreview();
    expect(player.plays, 2);
    expect(voice.playing, isFalse);
    await voice.discard();
    expect(voice.phase, VoiceComposerPhase.idle);
    expect(File(recorder.path!).existsSync(), isFalse);
  });

  test(
    'read failure retains draft; duplicate clicks and queue file ownership',
    () async {
      voice.dispose();
      final readGate = Completer<Uint8List>();
      voice = VoiceComposerController(
        recorder: recorder,
        player: FakeVoicePlayer(),
        now: () => now,
        readBytes: (_) => readGate.future,
      );
      await draft();
      var sends = 0;
      final sending = voice.send((_) => sends++);
      await voice.send((_) => sends++);
      expect(voice.phase, VoiceComposerPhase.submitting);
      readGate.completeError(const FileSystemException('read failed'));
      await sending;
      expect(sends, 0);
      expect(voice.phase, VoiceComposerPhase.preview);
      expect(voice.takeNotice(), VoiceComposerNotice.sendFailed);
      expect(File(recorder.path!).existsSync(), isTrue);
    },
  );

  test(
    'queue rejection retains draft, successful handoff retains retry file after disposal',
    () async {
      await draft();
      await voice.send((_) => throw StateError('local persistence failed'));
      expect(voice.hasDraft, isTrue);
      expect(voice.takeNotice(), VoiceComposerNotice.sendFailed);
      final gate = Completer<void>();
      var sends = 0;
      MediaUpload? upload;
      final sending = voice.send((value) {
        sends++;
        upload = value;
        return gate.future;
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));
      await voice.send((_) => sends++);
      expect(sends, 1);
      expect(upload!.durationSeconds, 2);
      expect(upload!.fileName, isNot(contains('/')));
      expect(upload!.kind, MessageContentKind.voice);
      voice.dispose();
      disposed = true;
      gate.complete();
      await sending;
      expect(File(recorder.path!).existsSync(), isTrue);
    },
  );

  test('disposal during native start cancels capture and timers', () async {
    recorder.startGate = Completer<void>();
    final start = voice.beginPress();
    await Future<void>.delayed(Duration.zero);
    voice.dispose();
    disposed = true;
    recorder.startGate!.complete();
    await start;
    await Future<void>.delayed(const Duration(milliseconds: 30));
    expect(recorder.disposed, isTrue);
    expect(player.disposed, isTrue);
    expect(File(recorder.path!).existsSync(), isFalse);
  });
}
