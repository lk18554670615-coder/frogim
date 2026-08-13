Map<String, Object?> _objectMap(Object? value) {
  if (value is! Map) return const {};
  return value.map((key, entry) => MapEntry(key.toString(), entry));
}

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
