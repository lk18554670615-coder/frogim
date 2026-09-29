import 'dart:convert';
import 'dart:math';

enum TenantPasswordProgress { unconfirmed, pending, completed, expired }

class TenantPasswordResult {
  const TenantPasswordResult(this.requestId, this.jobId, this.progress);
  final String requestId, jobId;
  final TenantPasswordProgress progress;

  factory TenantPasswordResult.fromJson(
    Map<String, Object?> json,
    String expected,
  ) {
    final status = switch (json['status']) {
      'unconfirmed' => TenantPasswordProgress.unconfirmed,
      'pending' => TenantPasswordProgress.pending,
      'completed' => TenantPasswordProgress.completed,
      _ => throw const FormatException('密码任务状态无法确认'),
    };
    final job = json['jobId'];
    if (json['requestId'] != expected ||
        (status != TenantPasswordProgress.unconfirmed &&
            (job is! String || !_id.hasMatch(job)))) {
      throw const FormatException('密码任务响应无法确认');
    }
    return TenantPasswordResult(expected, job is String ? job : '', status);
  }
}

final _id = RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$');

/// Stored encrypted, without passwords or SMS codes. Uses the original refresh
/// credential for a change, or a purpose-bound query capability for SMS recovery.
/// Neither can regain login authority through the task status endpoint.
class TenantPasswordTask {
  const TenantPasswordTask({
    required this.requestId,
    required this.token,
    required this.createdAt,
    this.jobId = '',
    this.recovery = false,
    this.progress = TenantPasswordProgress.unconfirmed,
  });
  final String requestId, token, jobId;
  final DateTime createdAt;
  final bool recovery;
  final TenantPasswordProgress progress;

  factory TenantPasswordTask.create(String token) {
    final random = Random.secure();
    return TenantPasswordTask(
      requestId:
          'pwd_${base64Url.encode(List.generate(24, (_) => random.nextInt(256))).replaceAll('=', '')}',
      token: token,
      createdAt: DateTime.now().toUtc(),
    );
  }
  factory TenantPasswordTask.recovery() {
    final random = Random.secure();
    final token = base64Url
        .encode(List.generate(32, (_) => random.nextInt(256)))
        .replaceAll('=', '');
    final task = TenantPasswordTask.create(token);
    return TenantPasswordTask(
      requestId: task.requestId,
      token: token,
      createdAt: task.createdAt,
      recovery: true,
    );
  }
  bool get expired =>
      DateTime.now().toUtc().difference(createdAt) >= const Duration(hours: 24);
  TenantPasswordTask update(TenantPasswordResult result) {
    if (result.requestId != requestId ||
        (jobId.isNotEmpty && result.jobId != jobId) ||
        (progress == TenantPasswordProgress.completed &&
            result.progress != progress)) {
      throw const FormatException('密码任务不匹配，请稍后重新查询');
    }
    return TenantPasswordTask(
      requestId: requestId,
      token: token,
      createdAt: createdAt,
      jobId: result.jobId,
      progress: result.progress,
      recovery: recovery,
    );
  }

  Map<String, Object?> toJson() => {
    'requestId': requestId,
    'token': token,
    'createdAt': createdAt.toIso8601String(),
    'jobId': jobId,
    'status': progress.name,
    if (recovery) 'recovery': true,
  };
  factory TenantPasswordTask.fromJson(Map<String, Object?> json) {
    final request = json['requestId'], token = json['token'];
    final created = DateTime.tryParse(json['createdAt']?.toString() ?? '');
    if (request is! String ||
        !_id.hasMatch(request) ||
        token is! String ||
        !RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(token) ||
        created == null ||
        created.isAfter(
          DateTime.now().toUtc().add(const Duration(minutes: 5)),
        )) {
      throw const FormatException('密码任务凭据不正确');
    }
    final result = TenantPasswordResult.fromJson(json, request);
    return TenantPasswordTask(
      requestId: request,
      token: token,
      createdAt: created.toUtc(),
      jobId: result.jobId,
      progress: result.progress,
      recovery: json['recovery'] == true,
    );
  }
}

abstract interface class TenantPasswordRepository {
  bool get supportsPasswordChange;
  TenantPasswordProgress? get passwordChangeProgress;
  Future<void> changeLoginPassword(String current, String next);
  Future<void> checkPasswordChange();
  Future<void> dismissPasswordChange();
}
