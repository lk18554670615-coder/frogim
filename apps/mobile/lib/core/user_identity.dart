bool isInternalUserHandle(String? value) {
  final handle = value?.trim() ?? '';
  return RegExp(
    r'^(?:ll|usr)_[a-z0-9]{12,}$',
    caseSensitive: false,
  ).hasMatch(handle);
}

String? publicUserHandle(String? value) {
  final handle = value?.trim() ?? '';
  if (handle.isEmpty || isInternalUserHandle(handle)) return null;
  return handle;
}

String publicUserHandleLabel(String? value, {String fallback = '尚未设置呱呱号'}) {
  final handle = publicUserHandle(value);
  return handle == null ? fallback : '@$handle';
}

bool userIdentityMatchesQuery({
  required String id,
  required String? handle,
  required String query,
}) {
  final normalizedQuery = query.trim().toLowerCase();
  if (normalizedQuery.isEmpty) return false;
  if (id.trim().toLowerCase() == normalizedQuery) return true;
  final publicHandle = publicUserHandle(handle)?.toLowerCase();
  return publicHandle != null && publicHandle == normalizedQuery;
}

bool samePublicUserIdentity({
  required String firstId,
  required String? firstHandle,
  required String secondId,
  required String? secondHandle,
}) {
  if (firstId.trim().isNotEmpty &&
      firstId.trim().toLowerCase() == secondId.trim().toLowerCase()) {
    return true;
  }
  final left = publicUserHandle(firstHandle)?.toLowerCase();
  final right = publicUserHandle(secondHandle)?.toLowerCase();
  return left != null && right != null && left == right;
}
