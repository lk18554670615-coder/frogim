import 'models.dart';

/// Complete membership is separate from the conversation's bounded preview.
/// Memory only; callers invalidate it on account/membership changes.
class GroupMemberDirectory {
  GroupMemberDirectory(
    this.loader, {
    required this.onChanged,
    DateTime Function()? now,
  }) : _now = now ?? DateTime.now;

  final Future<List<GroupMember>> Function(String) loader;
  final void Function() onChanged;
  final DateTime Function() _now;
  final _entries = <String, _MemberSnapshot>{};
  final _pending = <String, Future<List<GroupMember>>>{};
  final _tokens = <String, Object>{};
  final _missingRetryAfter = <String, DateTime>{};
  bool _disposed = false;

  List<GroupMember>? members(String id) => _entries[id]?.members;
  GroupMember? member(String id, String userId) => _entries[id]?.byId[userId];

  Future<List<GroupMember>> load(String id, {bool force = false}) {
    if (_disposed) return Future.error(const GroupMembersInvalidated());
    final pending = _pending[id];
    if (pending != null) return pending;
    final cached = _entries[id];
    if (!force &&
        cached != null &&
        _now().difference(cached.loadedAt) < const Duration(minutes: 1)) {
      _entries.remove(id);
      _entries[id] = cached;
      return Future.value(cached.members);
    }
    final token = Object();
    _tokens[id] = token;
    final operation = Future<List<GroupMember>>.sync(() => loader(id))
        .then((rows) {
          if (_disposed || !identical(_tokens[id], token)) {
            throw const GroupMembersInvalidated();
          }
          final unique = {
            for (final row in rows)
              if (row.user.id.isNotEmpty) row.user.id: row,
          };
          final result = List<GroupMember>.unmodifiable(unique.values);
          _entries.remove(id);
          _entries[id] = _MemberSnapshot(result, unique, _now());
          // Do not accumulate every group ever visited during a long session.
          while (_entries.length > 32) {
            final oldest = _entries.keys.first;
            _entries.remove(oldest);
            _missingRetryAfter.remove(oldest);
          }
          onChanged();
          return result;
        })
        .whenComplete(() {
          if (identical(_tokens[id], token)) {
            _pending.remove(id);
            _tokens.remove(id);
          }
        });
    _pending[id] = operation;
    return operation;
  }

  /// A message may arrive before the membership CMD. Coalesce lookups and
  /// throttle absent/left senders so historical messages cannot cause a storm.
  Future<void> loadForSender(String id, String senderId) async {
    if (_disposed || senderId.isEmpty || member(id, senderId) != null) {
      return;
    }
    if (_now().isBefore(_missingRetryAfter[id] ?? DateTime(1970))) return;
    if (!_missingRetryAfter.containsKey(id) &&
        _missingRetryAfter.length >= 32) {
      _missingRetryAfter.remove(_missingRetryAfter.keys.first);
    }
    _missingRetryAfter[id] = _now().add(const Duration(seconds: 10));
    try {
      await load(id, force: true);
    } catch (_) {
      // Failed identity hydration must not prevent message delivery.
    }
  }

  void invalidate([String? id]) {
    if (id == null) {
      _entries.clear();
      _tokens.clear();
      _pending.clear();
      _missingRetryAfter.clear();
    } else {
      _entries.remove(id);
      _tokens.remove(id);
      _pending.remove(id);
      _missingRetryAfter.remove(id);
    }
  }

  void retainGroups(Set<String> ids) {
    for (final id in {
      ..._entries.keys,
      ..._pending.keys,
      ..._missingRetryAfter.keys,
    }) {
      if (!ids.contains(id)) invalidate(id);
    }
  }

  void dispose() {
    _disposed = true;
    invalidate();
  }
}

class GroupMembersInvalidated implements Exception {
  const GroupMembersInvalidated();
}

class _MemberSnapshot {
  const _MemberSnapshot(this.members, this.byId, this.loadedAt);
  final List<GroupMember> members;
  final Map<String, GroupMember> byId;
  final DateTime loadedAt;
}
