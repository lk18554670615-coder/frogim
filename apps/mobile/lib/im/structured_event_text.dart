Map<String, Object?> _objectMap(Object? value) {
  if (value is! Map) return const {};
  return value.map((key, entry) => MapEntry(key.toString(), entry));
}

const groupAnnouncementUpdatedEvent = 'group.announcement.updated';
const groupAnnouncementUpdatedText = '群公告已更新，点击查看';

bool isGroupSystemEvent(Map<String, Object?> payload) =>
    (_eventValue(payload, 'event')?.toString() ?? '').startsWith('group.');

/// Returns a readable operation summary for group system events.
///
/// Older messages used a generic `[群系统消息]` digest. Those placeholders are
/// deliberately ignored so their structured event and data can still be
/// rendered without rewriting message history.
String groupSystemEventDisplayText(Map<String, Object?> payload) {
  final explicit = _explicitDigest(
    payload,
    placeholders: const {'[群系统消息]', '[系统消息]', '群系统消息', '系统消息'},
  );
  if (explicit.isNotEmpty) return explicit;

  final event = (_eventValue(payload, 'event')?.toString() ?? '').trim();
  final data = _objectMap(payload['data']);
  return switch (event) {
    groupAnnouncementUpdatedEvent => groupAnnouncementUpdatedText,
    'group.created' => '群聊已创建',
    'group.profile.updated' => '群资料已更新',
    'group.history.updated' =>
      _asBool(data['historyVisibleToNewMembers']) == true
          ? '已允许新成员查看入群前消息'
          : _asBool(data['historyVisibleToNewMembers']) == false
          ? '已关闭新成员查看入群前消息'
          : '群历史消息可见范围已更新',
    'group.invite.accepted' => '有成员接受邀请并加入群聊',
    'group.invite.rejected' => '有成员拒绝了群聊邀请',
    'group.invite.cancelled' => '群聊邀请已取消',
    'group.member.joined' =>
      data['source'] == 'qr' ? '有成员通过二维码加入群聊' : '有成员加入群聊',
    'group.members.added' ||
    'group.member_added' => _memberAddedText(data['userIds']),
    'group.member.leave' => '有成员退出群聊',
    'group.member.remove' => '有成员被移出群聊',
    'group.member.role' => switch (data['role']) {
      'admin' => '已设置一名群管理员',
      'member' => '已取消一名群管理员',
      _ => '群成员角色已更新',
    },
    'group.member.transfer' => '群主已转让',
    'group.member.mute' => _memberMuteText(data),
    'group.member.nickname' => '群昵称已更新',
    'group.blacklist.added' => '有成员被加入群黑名单',
    'group.blacklist.removed' => '有成员已移出群黑名单',
    'group.mute_all.updated' =>
      _asBool(data['muted']) == true
          ? '已开启全员禁言'
          : _asBool(data['muted']) == false
          ? '已解除全员禁言'
          : '全员禁言设置已更新',
    'group.ban.updated' =>
      _asBool(data['banned']) == true
          ? '群聊已封禁'
          : _asBool(data['banned']) == false
          ? '群聊已解除封禁'
          : '群聊封禁状态已更新',
    'group.message.pinned' => '已置顶一条群消息',
    'group.message.unpinned' => '已取消一条群消息置顶',
    'group.disbanded' => '群聊已解散',
    _ => '[群系统消息]',
  };
}

String _memberAddedText(Object? value) {
  final count = value is List ? value.length : 0;
  return count > 1 ? '$count 位成员已加入群聊' : '有成员加入群聊';
}

String _memberMuteText(Map<String, Object?> data) {
  final muted = _asBool(data['muted']);
  if (muted == true) return '已禁言一名群成员';
  if (muted == false) return '已解除一名群成员的禁言';
  if (data.containsKey('mutedUntil')) {
    final until = data['mutedUntil'];
    return until == null || until.toString().trim().isEmpty
        ? '已解除一名群成员的禁言'
        : '已禁言一名群成员';
  }
  return '群成员禁言设置已更新';
}

bool? _asBool(Object? value) => switch (value) {
  bool result => result,
  num result => result != 0,
  String result when result.toLowerCase() == 'true' || result == '1' => true,
  String result when result.toLowerCase() == 'false' || result == '0' => false,
  _ => null,
};

Object? _eventValue(Map<String, Object?> payload, String key) {
  final data = _objectMap(payload['data']);
  final call = _objectMap(payload['call']);
  return payload[key] ?? data[key] ?? call[key];
}

String _explicitDigest(
  Map<String, Object?> payload, {
  required Set<String> placeholders,
}) {
  for (final key in const ['digest', 'content', 'text']) {
    final value = (payload[key]?.toString() ?? '').trim();
    if (value.isNotEmpty && !placeholders.contains(value)) return value;
  }
  return '';
}

String callEventDisplayText(Map<String, Object?> payload) {
  final explicit = _explicitDigest(
    payload,
    placeholders: const {'[通话]', '[通话消息]'},
  );
  if (explicit.isNotEmpty) return explicit;

  final mediaType = (_eventValue(payload, 'mediaType')?.toString() ?? '')
      .toLowerCase();
  final label = mediaType == 'video' ? '视频通话' : '语音通话';
  final event = (_eventValue(payload, 'event')?.toString() ?? '').trim();
  final status = (_eventValue(payload, 'status')?.toString() ?? '').trim();
  final duration = switch (_eventValue(payload, 'durationSeconds')) {
    final num value => value.toInt(),
    final String value => int.tryParse(value) ?? 0,
    _ => 0,
  };
  final durationLabel = duration > 0 ? ' · ${_callDuration(duration)}' : '';

  return switch (event) {
    'call.invite' || 'call.invited' => '$label邀请',
    'call.accepted' => '$label已接通',
    'call.rejected' => '$label已拒绝',
    'call.cancelled' => '$label已取消',
    'call.timeout' => '$label未接通',
    'call.ended' || 'call.end' => '$label已结束$durationLabel',
    'call.participant_declined' => '有成员拒绝加入$label',
    'call.participant_left' => '有成员离开$label',
    _ => switch (status) {
      'accepted' => '$label已接通',
      'rejected' => '$label已拒绝',
      'cancelled' => '$label已取消',
      'missed' => '$label未接通',
      'ended' => '$label已结束$durationLabel',
      _ => '[通话]',
    },
  };
}

String supportEventDisplayText(Map<String, Object?> payload) {
  final explicit = _explicitDigest(
    payload,
    placeholders: const {'[客服消息]', '[客服事件]'},
  );
  if (explicit.isNotEmpty) return explicit;
  return switch (_eventValue(payload, 'event')) {
    'support.session.queued' => '已进入客服队列，请稍候',
    'support.session.assigned' => '客服已接入会话',
    'support.session.transferred' => '客服会话已转接',
    'support.session.ended' => '客服会话已结束',
    'support.session.rated' => '已提交客服评价',
    'support.session.updated' => '客服会话状态已更新',
    _ => '[客服消息]',
  };
}

String _callDuration(int seconds) {
  final safe = seconds < 0 ? 0 : seconds;
  final hours = safe ~/ 3600;
  final minutes = (safe % 3600) ~/ 60;
  final remainder = safe % 60;
  if (hours > 0) {
    return '${hours.toString().padLeft(2, '0')}:'
        '${minutes.toString().padLeft(2, '0')}:'
        '${remainder.toString().padLeft(2, '0')}';
  }
  return '${minutes.toString().padLeft(2, '0')}:'
      '${remainder.toString().padLeft(2, '0')}';
}
