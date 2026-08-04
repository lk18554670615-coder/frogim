import 'dart:convert';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Encrypted, account-scoped cache. On Web this requires HTTPS so
/// `flutter_secure_storage` can use WebCrypto for the wrapping key.
class SecureLocalStore {
  SecureLocalStore({FlutterSecureStorage? secureStorage})
    : _secureStorage = secureStorage ?? const FlutterSecureStorage();

  static const _keyName = 'nexachat.cache.key.v1';
  static const _prefix = 'nexachat.secure.v1.';
  final FlutterSecureStorage _secureStorage;
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
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('$_prefix$key', envelope);
  }

  Future<Object?> readJson(String key) async {
    final prefs = await SharedPreferences.getInstance();
    final envelope = prefs.getString('$_prefix$key');
    if (envelope == null) return null;
    try {
      final raw = jsonDecode(envelope) as Map<String, Object?>;
      final clear = await _cipher.decrypt(
        SecretBox(
          base64Decode(raw['c']! as String),
          nonce: base64Decode(raw['n']! as String),
          mac: Mac(base64Decode(raw['m']! as String)),
        ),
        secretKey: await _key(),
      );
      return jsonDecode(utf8.decode(clear));
    } catch (_) {
      await prefs.remove('$_prefix$key');
      return null;
    }
  }

  Future<void> remove(String key) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('$_prefix$key');
  }

  Future<void> clearAccountData() async {
    final prefs = await SharedPreferences.getInstance();
    final keys = prefs.getKeys().where((key) => key.startsWith(_prefix));
    for (final key in keys) {
      await prefs.remove(key);
    }
  }

  Future<SecretKey> _key() async {
    if (_memoryKey case final key?) return key;
    String? encoded;
    try {
      encoded = await _secureStorage.read(key: _keyName);
    } catch (_) {
      encoded = null;
    }
    if (encoded == null) {
      final generated = SecretKeyData.random(length: 32);
      final bytes = await generated.extractBytes();
      encoded = base64Encode(bytes);
      try {
        await _secureStorage.write(key: _keyName, value: encoded);
      } catch (_) {
        // Widget tests and unsupported desktop targets have no keystore plugin.
        // Keep the random key process-local; production mobile persists it.
      }
    }
    return _memoryKey = SecretKey(base64Decode(encoded));
  }
}
