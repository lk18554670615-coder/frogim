import 'dart:convert';
import 'package:crypto/crypto.dart';
import '../core/tenant_call_scope.dart';
import 'system_call_service_contract.dart';

String systemCallIdFor(String serverCallId, {TenantCallScope? scope}) {
  final seed = scope?.callSeed(serverCallId) ?? serverCallId;
  final bytes = sha256.convert(utf8.encode(seed)).bytes.toList();
  bytes[6] = (bytes[6] & 0x0f) | 0x50;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  final hex = bytes
      .take(16)
      .map((v) => v.toRadixString(16).padLeft(2, '0'))
      .join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-'
      '${hex.substring(16, 20)}-${hex.substring(20, 32)}';
}

SystemCallAction? systemCallActionFromMap(Map<Object?, Object?> json) {
  final typeName = json['type']?.toString();
  final serverCallId = json['serverCallId']?.toString();
  final systemCallId = json['systemCallId']?.toString();
  if (typeName == null ||
      serverCallId == null ||
      serverCallId.isEmpty ||
      systemCallId == null ||
      systemCallId.isEmpty) {
    return null;
  }
  final type = SystemCallActionType.values
      .where((v) => v.name == typeName)
      .firstOrNull;
  if (type == null) return null;
  final scope = TenantCallScope.parse(json['tenantScope']);
  if (json.containsKey('tenantScope') && scope == null) return null;
  if (json['muted'] != null && json['muted'] is! bool) return null;
  return SystemCallAction(
    type: type,
    serverCallId: serverCallId,
    systemCallId: systemCallId,
    muted: json['muted'] as bool?,
    tenantScope: scope,
  );
}
