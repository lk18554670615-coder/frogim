import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/tenant_call_scope.dart';
import 'package:linli_im/calls/system_call_service_native.dart';

Map<String, Object?> callScopeFixture() => {
  'tenantId': 'tenant-a',
  'localUserId': 'user-a',
  'assignmentVersion': 1,
  'authVersion': 2,
  'realmVersion': 3,
  'expiresAt': '2099-01-01T00:00:00Z',
  'callSessionId': 'a' * 43,
};

void main() {
  test('call scope contains only non-secret allowlisted fields', () {
    final source = {...callScopeFixture(), 'token': 'not-a-real-token'};
    final scope = TenantCallScope(source);
    source['tenantId'] = 'changed';
    expect(scope.data.containsKey('token'), isFalse);
    expect(scope.data['tenantId'], 'tenant-a');
    expect(() => scope.data['tenantId'] = 'changed', throwsUnsupportedError);
  });
  test('native and Dart use a stable tenant, user, version and login seed', () {
    final scope = TenantCallScope(callScopeFixture());
    expect(
      scope.callSeed('call-1'),
      'tenant-call-v1|tenant-a|user-a|1|2|3|${'a' * 43}|call-1',
    );
    final id = systemCallIdFor('call-1', scope: scope);
    for (final change in [
      {'tenantId': 'tenant-b'},
      {'localUserId': 'user-b'},
      {'assignmentVersion': 2},
      {'authVersion': 3},
      {'realmVersion': 4},
      {'callSessionId': 'b' * 43},
    ]) {
      final other = TenantCallScope({...callScopeFixture(), ...change});
      expect(scope.sameSession(other), isFalse);
      expect(systemCallIdFor('call-1', scope: other), isNot(id));
    }
    expect(
      systemCallIdFor(
        'call-1',
        scope: TenantCallScope({
          ...callScopeFixture(),
          'expiresAt': '2099-02-01T00:00:00Z',
        }),
      ),
      id,
    ); // Refresh does not create a duplicate native call.
  });
  for (final expiry in [
    '2099-02-30T00:00:00Z',
    '2099-01-01',
    '2099-01-01T00:00:00',
    '2099-01-01T25:00:00Z',
    '2099-01-01T00:00:00+25:00',
  ]) {
    test('rejects invalid native expiry $expiry', () {
      expect(
        TenantCallScope.parse({...callScopeFixture(), 'expiresAt': expiry}),
        isNull,
      );
    });
  }
  test('rejects unsafe versions and ambiguous explicit action scope', () {
    for (final version in [0, -1, 1.5, '1', 9007199254740992]) {
      expect(
        TenantCallScope.parse({...callScopeFixture(), 'authVersion': version}),
        isNull,
      );
    }
    expect(
      systemCallActionFromMap({
        'type': 'accept',
        'serverCallId': 'call-1',
        'systemCallId': 'native-id',
        'tenantScope': {'tenantId': 'tenant-a'},
      }),
      isNull,
    );
  });
}
