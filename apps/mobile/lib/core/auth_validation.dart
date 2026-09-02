import 'dart:convert';

class AuthPolicy {
  const AuthPolicy({
    this.registrationEnabled = true,
    this.passwordMinLength = 8,
    this.passwordMaxBytes = 72,
    this.messageRecallMinutes = 2,
    this.directRecallMinutes = 1440,
    this.groupRecallMinutes = 1440,
  });

  final bool registrationEnabled;
  final int passwordMinLength;
  final int passwordMaxBytes;
  // Legacy wire key retained for the independent message editing policy.
  final int messageRecallMinutes;
  final int directRecallMinutes;
  final int groupRecallMinutes;

  factory AuthPolicy.fromJson(Map<String, Object?> json) {
    final minimum = ((json['passwordMinLength'] as num?)?.toInt() ?? 8)
        .clamp(8, 16)
        .toInt();
    final maximum = ((json['passwordMaxBytes'] as num?)?.toInt() ?? 72)
        .clamp(minimum, 72)
        .toInt();
    final recallMinutes = ((json['messageRecallMinutes'] as num?)?.toInt() ?? 2)
        .clamp(1, 1440)
        .toInt();
    return AuthPolicy(
      registrationEnabled: json['registrationEnabled'] is bool
          ? json['registrationEnabled']! as bool
          : true,
      passwordMinLength: minimum,
      passwordMaxBytes: maximum,
      messageRecallMinutes: recallMinutes,
      directRecallMinutes:
          ((json['directRecallMinutes'] as num?)?.toInt() ?? 1440)
              .clamp(1, 10080)
              .toInt(),
      groupRecallMinutes:
          ((json['groupRecallMinutes'] as num?)?.toInt() ?? 1440)
              .clamp(1, 10080)
              .toInt(),
    );
  }

  Duration get messageMutationWindow => Duration(minutes: messageRecallMinutes);
  Duration get directRecallWindow => Duration(minutes: directRecallMinutes);
  Duration get groupRecallWindow => Duration(minutes: groupRecallMinutes);

  String get passwordHelperText =>
      '至少 $passwordMinLength 个字符，最多 $passwordMaxBytes 字节';

  String? passwordError(String password) {
    if (password.runes.length < passwordMinLength) {
      return '密码至少 $passwordMinLength 个字符';
    }
    if (utf8.encode(password).length > passwordMaxBytes) {
      return '密码过长，请缩短后重试';
    }
    return null;
  }
}

bool validAuthPhone(String value) =>
    RegExp(r'^\+?[0-9]{6,32}$').hasMatch(value.trim());
