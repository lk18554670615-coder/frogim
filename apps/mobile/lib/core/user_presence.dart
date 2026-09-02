import 'dart:async';

import 'package:flutter/foundation.dart';

enum UserPresenceStatus { online, offline, unknown, hidden }

class UserPresenceSnapshot {
  const UserPresenceSnapshot(this.userId, this.status, {this.checkedAt});
  final String userId;
  final UserPresenceStatus status;
  final DateTime? checkedAt;

  factory UserPresenceSnapshot.fromJson(Map<String, Object?> json) =>
      UserPresenceSnapshot(
        json['userId'] as String,
        UserPresenceStatus.values
                .where((v) => v.name == json['status'])
                .firstOrNull ??
            UserPresenceStatus.unknown,
        checkedAt: DateTime.tryParse(json['checkedAt']?.toString() ?? ''),
      );
}

typedef PresenceLoader =
    Future<List<UserPresenceSnapshot>> Function(
      List<String> ids, {
      String? groupId,
    });
typedef PresenceKey = ({String userId, String? groupId});

class _PresenceWatch {
  int references = 0;
  int revision = 0;
  bool dirty = true;
  bool inFlight = false;
  UserPresenceSnapshot? value;
}

/// Memory-only, account/context-isolated and shared by all visible panels.
/// Widgets own watches, not timers; overlapping watches coalesce by context/UID.
class PresenceCoordinator extends ChangeNotifier {
  PresenceCoordinator(this.load);
  final PresenceLoader load;
  final _watches = <PresenceKey, _PresenceWatch>{};
  String? _account;
  int _epoch = 0;
  int _requests = 0;
  bool _foreground = true;
  bool _disposed = false;
  bool _scheduled = false;
  Timer? _timer;

  UserPresenceStatus status(String id, {String? groupId}) =>
      _watches[(userId: id, groupId: groupId)]?.value?.status ??
      UserPresenceStatus.hidden;

  void setAccount(String? account) {
    if (_disposed || _account == account) return;
    _account = account;
    invalidate();
  }

  void setForeground(bool foreground) {
    if (_disposed || _foreground == foreground) return;
    _foreground = foreground;
    invalidate();
  }

  VoidCallback watch(String id, {String? groupId}) {
    final key = (userId: id, groupId: groupId);
    final entry = _watches.putIfAbsent(key, _PresenceWatch.new);
    entry.references++;
    if (!entry.inFlight) entry.dirty = true;
    _schedule();
    _updateTimer();
    var released = false;
    return () {
      if (released || _disposed) return;
      released = true;
      if (--entry.references == 0 && identical(_watches[key], entry)) {
        entry.value = null;
        entry.revision++;
        entry.dirty = true;
        // A quick close/reopen must not start an overlapping query.
        if (!entry.inFlight) _watches.remove(key);
      }
      _updateTimer();
    };
  }

  /// Clear privileged observations immediately; never reuse another group's
  /// authorization or an old response after a friendship/role/session change.
  void invalidate() {
    if (_disposed) return;
    _epoch++;
    for (final entry in _watches.values) {
      entry.value = null;
      entry.dirty = true;
      entry.revision++;
    }
    notifyListeners();
    _updateTimer();
    _schedule();
  }

  bool get _enabled =>
      !_disposed &&
      _foreground &&
      _account != null &&
      _watches.values.any((entry) => entry.references > 0);

  void _updateTimer() {
    if (!_enabled) {
      _timer?.cancel();
      _timer = null;
      return;
    }
    _timer ??= Timer.periodic(const Duration(seconds: 10), (_) {
      for (final entry in _watches.values) {
        if (!entry.inFlight) entry.dirty = true;
      }
      _schedule();
    });
  }

  void _schedule() {
    if (_scheduled || !_enabled) return;
    _scheduled = true;
    scheduleMicrotask(() {
      _scheduled = false;
      _drain();
    });
  }

  void _drain() {
    if (!_enabled) return;
    while (_requests < 2) {
      final pending = _watches.entries
          .where(
            (e) => e.value.references > 0 && e.value.dirty && !e.value.inFlight,
          )
          .toList();
      if (pending.isEmpty) return;
      final group = pending.first.key.groupId;
      final batch = pending
          .where((e) => e.key.groupId == group)
          .take(200)
          .toList();
      final revisions = [for (final e in batch) e.value.revision];
      for (final e in batch) {
        e.value.dirty = false;
        e.value.inFlight = true;
      }
      _requests++;
      unawaited(_fetch(batch, revisions, _epoch, group));
    }
  }

  Future<void> _fetch(
    List<MapEntry<PresenceKey, _PresenceWatch>> batch,
    List<int> revisions,
    int epoch,
    String? group,
  ) async {
    var result = <String, UserPresenceSnapshot>{};
    try {
      final rows = await load([
        for (final e in batch) e.key.userId,
      ], groupId: group).timeout(const Duration(seconds: 15));
      result = {for (final row in rows) row.userId: row};
    } catch (_) {
      /* Failure is unknown, never stale online or false offline. */
    }
    _requests--;
    var changed = false;
    for (var i = 0; i < batch.length; i++) {
      final e = batch[i];
      e.value.inFlight = false;
      if (e.value.references == 0 && identical(_watches[e.key], e.value)) {
        _watches.remove(e.key);
      }
      if (!_enabled ||
          epoch != _epoch ||
          e.value.revision != revisions[i] ||
          !identical(_watches[e.key], e.value)) {
        continue;
      }
      e.value.value =
          result[e.key.userId] ??
          UserPresenceSnapshot(e.key.userId, UserPresenceStatus.unknown);
      changed = true;
    }
    if (!_disposed) {
      if (changed) notifyListeners();
      _schedule();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _epoch++;
    _timer?.cancel();
    _watches.clear();
    super.dispose();
  }
}
