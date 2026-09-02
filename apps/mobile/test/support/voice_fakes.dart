import 'dart:async';
import 'dart:io';

import 'package:audioplayers/audioplayers.dart';
import 'package:record/record.dart';

class FakeVoiceRecorder implements AudioRecorder {
  bool permission = true;
  bool failStart = false;
  bool failStop = false;
  bool failAmplitude = false;
  double decibels = -60;
  Completer<bool>? permissionGate;
  Completer<void>? startGate;
  Completer<void>? stopGate;
  String? path;
  int starts = 0;
  int stops = 0;
  int cancels = 0;
  int samples = 0;
  bool disposed = false;

  @override
  Future<bool> hasPermission({bool request = true}) async =>
      permissionGate == null ? permission : await permissionGate!.future;

  @override
  Future<void> start(RecordConfig config, {required String path}) async {
    starts++;
    this.path = path;
    File(path).writeAsBytesSync([1, 2, 3, 4]);
    if (startGate != null) await startGate!.future;
    if (failStart) throw StateError('recorder start failed');
  }

  @override
  Future<String?> stop() async {
    stops++;
    if (stopGate != null) await stopGate!.future;
    if (failStop) throw StateError('recorder save failed');
    return path;
  }

  @override
  Future<void> cancel() async => cancels++;

  @override
  Future<Amplitude> getAmplitude() async {
    samples++;
    if (failAmplitude) throw UnsupportedError('no meter');
    return Amplitude(current: decibels, max: decibels);
  }

  @override
  Future<void> dispose() async => disposed = true;

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class FakeVoicePlayer implements AudioPlayer {
  final completed = StreamController<void>.broadcast();
  int plays = 0;
  int stops = 0;
  bool disposed = false;

  @override
  Stream<void> get onPlayerComplete => completed.stream;

  @override
  Future<void> play(
    Source source, {
    double? volume,
    double? balance,
    AudioContext? ctx,
    Duration? position,
    PlayerMode? mode,
  }) async => plays++;

  @override
  Future<void> stop() async => stops++;

  @override
  Future<void> dispose() async {
    disposed = true;
    await completed.close();
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
