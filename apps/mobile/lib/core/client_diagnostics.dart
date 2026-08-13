import 'dart:async';
import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../data/im_repository.dart';

class ClientDiagnostics {
  ClientDiagnostics._();

  static final ClientDiagnostics instance = ClientDiagnostics._();

  final List<_PendingDiagnostic> _pending = [];
  final Set<String> _capturedFingerprints = {};
  ImRepository? _repository;
  bool _flushing = false;
  String? _appVersion;

  void attach(ImRepository repository) {
    _repository = repository;
    unawaited(flush());
  }

  void captureError(String source, Object error, StackTrace stackTrace) {
    final stackShape = stackTrace
        .toString()
        .split('\n')
        .where((line) => line.trim().isNotEmpty)
        .take(8)
        .join('\n');
    _capture(
      kind: 'crash',
      name: source,
      fingerprintSource: '$source|${error.runtimeType}|$stackShape',
    );
  }

  void captureOperational({
    required String kind,
    required String name,
    Duration? duration,
  }) {
    _capture(
      kind: kind,
      name: name,
      fingerprintSource: '$kind|$name|${_platform()}',
      durationMs: duration?.inMilliseconds,
    );
  }

  void recordStartup(Duration elapsed) {
    _capture(
      kind: 'performance',
      name: 'app_start',
      fingerprintSource: 'performance|app_start|${_platform()}',
      durationMs: elapsed.inMilliseconds,
    );
  }

  void _capture({
    required String kind,
    required String name,
    required String fingerprintSource,
    int? durationMs,
  }) {
    final fingerprint = sha256
        .convert(utf8.encode(fingerprintSource))
        .toString();
    if (!_capturedFingerprints.add(fingerprint)) return;
    if (_pending.length >= 10) _pending.removeAt(0);
    _pending.add(
      _PendingDiagnostic(
        kind: kind,
        name: name,
        fingerprint: fingerprint,
        durationMs: durationMs,
      ),
    );
    unawaited(flush());
  }

  Future<void> flush([ImRepository? repository]) async {
    if (repository != null) _repository = repository;
    final target = _repository;
    if (target == null || _flushing || _pending.isEmpty) return;
    _flushing = true;
    try {
      _appVersion ??= await _loadAppVersion();
      while (_pending.isNotEmpty) {
        final item = _pending.first;
        try {
          await target.reportClientDiagnostic(
            kind: item.kind,
            name: item.name,
            fingerprint: item.fingerprint,
            platform: _platform(),
            appVersion: _appVersion!,
            durationMs: item.durationMs,
          );
          _pending.removeAt(0);
        } catch (_) {
          break;
        }
      }
    } finally {
      _flushing = false;
    }
  }

  Future<String> _loadAppVersion() async {
    try {
      final info = await PackageInfo.fromPlatform();
      return '${info.version}+${info.buildNumber}';
    } catch (_) {
      return 'unknown';
    }
  }

  String _platform() {
    if (kIsWeb) return 'web';
    return switch (defaultTargetPlatform) {
      TargetPlatform.android => 'android',
      TargetPlatform.iOS => 'ios',
      TargetPlatform.macOS => 'macos',
      _ => 'unknown',
    };
  }
}

class _PendingDiagnostic {
  const _PendingDiagnostic({
    required this.kind,
    required this.name,
    required this.fingerprint,
    this.durationMs,
  });

  final String kind;
  final String name;
  final String fingerprint;
  final int? durationMs;
}
