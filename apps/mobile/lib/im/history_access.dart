/// A server-issued, membership-specific read boundary. Never inferred from a
/// role or from the earliest locally cached message.
class GroupHistoryAccess {
  const GroupHistoryAccess({
    required this.version,
    required this.visibleAll,
    this.afterSeq,
    this.afterTimestamp,
  });
  final int version;
  final bool visibleAll;
  final int? afterSeq;
  final int? afterTimestamp;

  static GroupHistoryAccess? parse(Object? value) {
    if (value is! Map) return null;
    int? integer(Object? raw) =>
        raw is num && raw.isFinite && raw == raw.truncateToDouble()
        ? raw.toInt()
        : null;
    final version = integer(value['version']);
    final seq = integer(value['afterSeq']);
    final stamp = integer(value['afterTimestamp']);
    if (version == null ||
        version < 1 ||
        value['visibleAll'] is! bool ||
        (value['visibleAll'] != true && seq == null && stamp == null) ||
        (seq != null && seq < 0)) {
      return null;
    }
    return GroupHistoryAccess(
      version: version,
      visibleAll: value['visibleAll'] == true,
      afterSeq: seq,
      afterTimestamp: stamp,
    );
  }

  bool allows(int sequence, DateTime timestamp) =>
      visibleAll ||
      (afterSeq != null
          ? sequence > afterSeq!
          : afterTimestamp != null &&
                timestamp.millisecondsSinceEpoch ~/ 1000 > afterTimestamp!);

  String get fingerprint => '$version:$visibleAll:$afterSeq:$afterTimestamp';
}

/// Optional native cache capability. This removes local rows, NOT server
/// messages or SDK tombstones, so reopening history can fetch them again.
abstract interface class WukongHistoryCache {
  Future<void> invalidateGroupHistory(
    String channelId,
    GroupHistoryAccess? access,
  );
}
