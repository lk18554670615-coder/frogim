import 'models.dart';

/// Presentation only. The server and WuKongIM remain authoritative for sends.
class GroupSendPolicy {
  const GroupSendPolicy({required this.profile, required this.member});

  final GroupProfile profile;
  final GroupMember? member;

  String? restrictionAt(DateTime now) {
    if (profile.dissolvedAt != null) return '群聊已解散，无法发送消息';
    final current = member;
    if (current == null) return '你已不在该群聊中，无法发送消息';
    if (current.mutedUntil?.isAfter(now) == true) {
      return '你已被禁言，暂时无法在该群发送消息';
    }
    if (profile.allMutedUntil?.isAfter(now) == true &&
        !current.isOwner &&
        !current.isAdmin) {
      return '群聊已开启全员禁言，仅群主和管理员可发言';
    }
    return null;
  }

  DateTime? nextChangeAfter(DateTime now) {
    final times = [
      member?.mutedUntil,
      profile.allMutedUntil,
    ].whereType<DateTime>().where((time) => time.isAfter(now)).toList()..sort();
    return times.firstOrNull;
  }
}
