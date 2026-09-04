import 'secure_local_store.dart';

/// Independent of message windows/eviction. A stale ACK or historical page must
/// never erase a known deletion. Account IDs are part of every key.
class MessageDeletionCache {
  MessageDeletionCache(this.store);
  final SecureLocalStore store;
  final Map<String, Set<String>> _ids = {};
  final Map<String, Future<void>> _loads = {};
  Future<void> _writes = Future.value();
  bool contains(String? uid, String id) => _ids[uid]?.contains(id) ?? false;

  Future<void> load(String uid) => _loads.putIfAbsent(uid, () async {
    try {
      final raw = await store.readJson('mutual-deletions.$uid');
      _ids
          .putIfAbsent(uid, () => {})
          .addAll((raw is List ? raw : const []).whereType<String>());
    } catch (_) {
      _loads.remove(uid);
      rethrow;
    }
  });

  Future<void> mark(String uid, Iterable<String> ids) {
    // Publish in memory before awaiting storage, including concurrent arrivals.
    _ids.putIfAbsent(uid, () => {}).addAll(ids);
    final operation = _writes.catchError((Object _) {}).then((_) async {
      await load(uid);
      await store.writeJson('mutual-deletions.$uid', _ids[uid]!.toList());
    });
    _writes = operation;
    return operation;
  }
}
