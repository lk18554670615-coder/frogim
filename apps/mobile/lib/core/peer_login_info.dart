/// A conversation-scoped projection, not part of the public user profile/cache.
class PeerLoginInfo {
  const PeerLoginInfo({
    required this.userId,
    this.lastLoginIp = '',
    this.regionLabel = '未记录',
  });

  final String userId;
  final String lastLoginIp;
  final String regionLabel;

  factory PeerLoginInfo.fromJson(Map<String, Object?> json) {
    final region = json['region'];
    final values = region is Map ? region : const {};
    final parts = <String>[];
    for (final key in ['country', 'province', 'city', 'isp']) {
      final value = (values[key] as String?)?.trim() ?? '';
      if (value.isNotEmpty && !parts.contains(value)) parts.add(value);
    }
    final label = switch (values['status']) {
      'ok' => parts.isEmpty ? '暂不可用' : parts.join(' · '),
      'private' => '内网地址',
      'loopback' => '本机回环地址',
      'reserved' => '保留地址',
      'unknown' => '未记录',
      _ => '暂不可用',
    };
    return PeerLoginInfo(
      userId: json['userId'] as String? ?? '',
      lastLoginIp: (json['lastLoginIp'] as String? ?? '').trim(),
      regionLabel: label,
    );
  }
}
