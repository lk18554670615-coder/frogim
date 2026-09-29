/// Non-secret identity carried through native call UI and persisted actions.
/// It never grants API authority: CallController still loads the authoritative
/// call from the immutable enterprise repository before accepting it.
class TenantCallScope {
  TenantCallScope(Map<String, Object?> input)
    : data = Map.unmodifiable({
        for (final key in keys) key: input[key],
        if (input['callSessionId'] != null)
          'callSessionId': input['callSessionId'],
      }) {
    if (!_id(data['tenantId']) ||
        !_id(data['localUserId']) ||
        !versions.every((key) => _version(data[key])) ||
        !_expiry(data['expiresAt'])) {
      throw const FormatException('通话身份未确认');
    }
    if (data['callSessionId'] != null &&
        (data['callSessionId'] is! String ||
            !RegExp(
              r'^[A-Za-z0-9_-]{43}$',
            ).hasMatch(data['callSessionId']! as String))) {
      throw const FormatException('通话登录代次无效');
    }
  }
  static const versions = ['assignmentVersion', 'authVersion', 'realmVersion'];
  static const keys = ['tenantId', 'localUserId', ...versions, 'expiresAt'];
  final Map<String, Object?> data;
  static bool _id(Object? value) =>
      value is String &&
      RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$').hasMatch(value);
  static bool _version(Object? value) =>
      value is int && value > 0 && value <= 9007199254740991;
  static bool _expiry(Object? value) {
    if (value is! String) return false;
    final match = RegExp(
      r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$',
    ).firstMatch(value);
    if (match == null) return false;
    final values = [for (var i = 1; i <= 6; i++) int.parse(match.group(i)!)];
    final date = DateTime.utc(values[0], values[1], values[2]);
    final zone = match.group(7)!;
    return date.year == values[0] &&
        date.month == values[1] &&
        date.day == values[2] &&
        values[3] < 24 &&
        values[4] < 60 &&
        values[5] < 60 &&
        (zone == 'Z' ||
            (int.parse(zone.substring(1, 3)) < 24 &&
                int.parse(zone.substring(4)) < 60)) &&
        DateTime.tryParse(value) != null;
  }

  DateTime get expiresAt => DateTime.parse(data['expiresAt']! as String);
  bool sameIdentity(TenantCallScope other) => keys
      .where((key) => key != 'expiresAt')
      .every((key) => data[key] == other.data[key]);
  bool get validNow => DateTime.now().toUtc().isBefore(expiresAt);
  bool sameSession(TenantCallScope other) =>
      sameIdentity(other) &&
      data['callSessionId'] != null &&
      data['callSessionId'] == other.data['callSessionId'];
  String callSeed(String callId) =>
      'tenant-call-v1|${data['tenantId']}|${data['localUserId']}|'
      '${data['assignmentVersion']}|${data['authVersion']}|${data['realmVersion']}|${data['callSessionId']}|$callId';

  static TenantCallScope? parse(Object? value) {
    if (value is! Map) return null;
    try {
      return TenantCallScope(Map<String, Object?>.from(value));
    } catch (_) {
      return null;
    }
  }
}
