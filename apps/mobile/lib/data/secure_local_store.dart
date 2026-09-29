import 'dart:convert';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:flutter/foundation.dart';
import 'session_coordination.dart';

/// Encrypted, account-scoped cache. On Web this requires HTTPS so
/// `flutter_secure_storage` can use WebCrypto for the wrapping key.
class SecureLocalStore {
  SecureLocalStore({
    FlutterSecureStorage? secureStorage,
    String? namespace,
    this.requirePersistentKey = false,
    this.tabLocalSession = false,
    this.tabLocalKeys = const {},
  }) : _secureStorage = secureStorage ?? const FlutterSecureStorage(),
       _storagePrefix = namespace == null
           ? _prefix
           : 'nexachat.tenant.secure.v1.${Uri.encodeComponent(namespace)}.';

  static const _keyName = 'nexachat.cache.key.v1';
  static const _prefix = 'nexachat.secure.v1.';
  // Immutable per-instance scope: an old asynchronous write keeps its old
  // namespace even after a different repository/account is constructed.
  final String _storagePrefix;
  final FlutterSecureStorage _secureStorage;
  final bool requirePersistentKey;
  final bool tabLocalSession;
  final Set<String> tabLocalKeys;
  bool _tabLocal(String key) =>
      kIsWeb &&
      (tabLocalKeys.contains(key) || tabLocalSession && key == 'session');
  final AesGcm _cipher = AesGcm.with256bits();
  SecretKey? _memoryKey;

  Future<void> writeJson(String key, Object value) async {
    final secretKey = await _key();
    final nonce = _cipher.newNonce();
    final box = await _cipher.encrypt(
      utf8.encode(jsonEncode(value)),
      secretKey: secretKey,
      nonce: nonce,
    );
    final envelope = jsonEncode({
      'n': base64Encode(nonce),
      'c': base64Encode(box.cipherText),
      'm': base64Encode(box.mac.bytes),
    });
    if (_tabLocal(key)) {
      writeTabSession('$_storagePrefix$key', envelope);
      return;
    }
    final prefs = await SharedPreferences.getInstance();
    if (!await prefs.setString('$_storagePrefix$key', envelope)) {
      throw StateError('无法保存加密数据，请检查设备存储');
    }
  }

  Future<Object?> readJson(String key) async {
    final prefs = await SharedPreferences.getInstance();
    // SharedPreferences caches across calls, but another tab may have rotated
    // the platform credential. Reload before reading shared encrypted state.
    if (kIsWeb) await prefs.reload();
    final envelope = _tabLocal(key)
        ? readTabSession('$_storagePrefix$key')
        : prefs.getString('$_storagePrefix$key');
    if (envelope == null) return null;
    // A temporarily unavailable platform keystore is not corrupt ciphertext.
    // Preserve the pending task instead of deleting it or generating a new key.
    final persistentKey = requirePersistentKey || kIsWeb
        ? await _key(createIfMissing: false)
        : null;
    try {
      final raw = jsonDecode(envelope) as Map<String, Object?>;
      final clear = await _cipher.decrypt(
        SecretBox(
          base64Decode(raw['c']! as String),
          nonce: base64Decode(raw['n']! as String),
          mac: Mac(base64Decode(raw['m']! as String)),
        ),
        secretKey: persistentKey ?? await _key(),
      );
      return jsonDecode(utf8.decode(clear));
    } catch (_) {
      // Never let a late decode failure erase newer ciphertext from another
      // tab. Platform authority is preserved for an explicit recovery/login.
      if (requirePersistentKey || kIsWeb) {
        throw StateError('加密认证数据暂不可读，请重新登录或恢复安全存储');
      }
      if (_tabLocal(key)) {
        if (readTabSession('$_storagePrefix$key') == envelope) {
          removeTabSession('$_storagePrefix$key');
        }
      } else {
        if (kIsWeb) await prefs.reload();
        if (prefs.getString('$_storagePrefix$key') == envelope) {
          await prefs.remove('$_storagePrefix$key');
        }
      }
      return null;
    }
  }

  Future<void> remove(String key) async {
    if (_tabLocal(key)) {
      removeTabSession('$_storagePrefix$key');
      return;
    }
    final prefs = await SharedPreferences.getInstance();
    if (!await prefs.remove('$_storagePrefix$key')) {
      throw StateError('无法移除加密数据，请检查设备存储');
    }
  }

  Future<void> clearAccountData() async {
    if (kIsWeb && tabLocalSession) removeTabSession('${_storagePrefix}session');
    if (kIsWeb) {
      for (final key in tabLocalKeys) {
        removeTabSession('$_storagePrefix$key');
      }
    }
    final prefs = await SharedPreferences.getInstance();
    final keys = prefs.getKeys().where((key) => key.startsWith(_storagePrefix));
    for (final key in keys) {
      await prefs.remove(key);
    }
  }

  Future<SecretKey> _key({bool createIfMissing = true}) async {
    if (_memoryKey case final key?) return key;
    return withSecureKeyLock(() => _loadKey(createIfMissing: createIfMissing));
  }

  Future<SecretKey> _loadKey({bool createIfMissing = true}) async {
    if (_memoryKey case final key?) return key;
    String? encoded;
    try {
      encoded = await _secureStorage.read(key: _keyName);
    } catch (_) {
      if (requirePersistentKey || kIsWeb) {
        throw StateError('无法读取安全存储，暂不能进行平台认证操作');
      }
      encoded = null;
    }
    if (encoded == null) {
      if (!createIfMissing) throw StateError('平台安全凭据暂不可读，请恢复设备安全存储后重试');
      final generated = SecretKeyData.random(length: 32);
      final bytes = await generated.extractBytes();
      encoded = base64Encode(bytes);
      try {
        await _secureStorage.write(key: _keyName, value: encoded);
      } catch (_) {
        if (requirePersistentKey || kIsWeb) {
          throw StateError('无法持久保存安全凭据，暂不能进行平台认证操作');
        }
        // Widget tests and unsupported desktop targets have no keystore plugin.
        // Keep the random key process-local; production mobile persists it.
      }
    }
    return _memoryKey = SecretKey(base64Decode(encoded));
  }
}
