import 'dart:convert';

class AuthPolicy {
  const AuthPolicy({
    this.registrationEnabled = true,
    this.passwordMinLength = 8,
    this.passwordMaxBytes = 72,
  });

  final bool registrationEnabled;
  final int passwordMinLength;
  final int passwordMaxBytes;

  factory AuthPolicy.fromJson(Map<String, Object?> json) {
    final minimum = ((json['passwordMinLength'] as num?)?.toInt() ?? 8)
        .clamp(8, 16)
        .toInt();
    final maximum = ((json['passwordMaxBytes'] as num?)?.toInt() ?? 72)
        .clamp(minimum, 72)
        .toInt();
    return AuthPolicy(
      registrationEnabled: json['registrationEnabled'] is bool
          ? json['registrationEnabled']! as bool
          : true,
      passwordMinLength: minimum,
      passwordMaxBytes: maximum,
    );
  }

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
