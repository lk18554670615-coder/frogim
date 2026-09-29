import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/data/secure_local_store.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('enterprise/user/generation caches and logout are isolated', () async {
    final a = SecureLocalStore(namespace: 'tenant.a.user.local.binding.1');
    final b = SecureLocalStore(namespace: 'tenant.b.user.local.binding.1');
    final returned = SecureLocalStore(
      namespace: 'tenant.a.user.local.binding.3',
    );
    final legacy = SecureLocalStore();
    await a.writeJson('draft.chat', {'text': 'source draft'});
    await b.writeJson('draft.chat', {'text': 'target draft'});
    await returned.writeJson('session', {'accessToken': 'new-generation'});
    await legacy.writeJson('session', {'accessToken': 'standalone'});
    expect(await a.readJson('draft.chat'), {'text': 'source draft'});
    expect(await b.readJson('draft.chat'), {'text': 'target draft'});
    expect(await returned.readJson('draft.chat'), isNull);
    await legacy.clearAccountData();
    expect(await a.readJson('draft.chat'), isNotNull);
    await a.clearAccountData();
    expect(await a.readJson('draft.chat'), isNull);
    expect(await b.readJson('draft.chat'), isNotNull);
    expect(await returned.readJson('session'), isNotNull);
  });
}
