import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/tenant_call_scope.dart';
import 'package:linli_im/core/tenant_push.dart';
import 'package:linli_im/data/native_call_state_channel.dart';

Map<String, Object?> _scope({String generation = 'a'}) => {
  'tenantId': 'tenant-a',
  'localUserId': 'user-a',
  'assignmentVersion': 1,
  'authVersion': 2,
  'realmVersion': 3,
  'expiresAt': '2099-01-01T00:00:00Z',
  'callSessionId': generation * 43,
};
TenantPushBinding _binding() => TenantPushBinding({
  ..._scope(),
  'pushBindingId': 'binding-1',
  'pushBindingRevision': 1,
  'leaseExpiresAt': '2099-01-01T00:00:00Z',
  'secret': 'never-persist-this',
});

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('test/tenant-native-calls');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late ChannelNativeCallState state;
  late List<Map<Object?, Object?>> writes;
  setUp(() {
    state = ChannelNativeCallState(channel: channel);
    writes = [];
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'beginTenantCallControl') return 'controller-1';
      writes.add(Map<Object?, Object?>.from(call.arguments as Map));
      return true;
    });
  });
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test(
    'atomic snapshot contains identity and lease, never credential fields',
    () async {
      await state.replace(TenantCallScope(_scope()));
      await state.bind('getui', _binding());
      expect(writes.last['control'], 'controller-1');
      expect(writes.last['sequence'], 2);
      final binding = (writes.last['bindings'] as List).single as Map;
      expect(binding['pushBindingId'], 'binding-1');
      expect(binding.containsKey('secret'), isFalse);
      expect(binding.containsKey('callSessionId'), isFalse);
      await state.replace(TenantCallScope(_scope(generation: 'b')));
      expect(writes.last['bindings'], isEmpty);
    },
  );
  test(
    'logout queued behind a delayed write leaves native state revoked',
    () async {
      final gate = Completer<void>();
      messenger.setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'beginTenantCallControl') return 'controller-1';
        if (writes.isEmpty) await gate.future;
        writes.add(Map<Object?, Object?>.from(call.arguments as Map));
        return true;
      });
      final login = state.replace(TenantCallScope(_scope()));
      final bind = state.bind('getui', _binding());
      final logout = state.replace(null);
      gate.complete();
      await Future.wait([login, bind, logout]);
      expect(writes.map((value) => value['sequence']), [1, 2, 3]);
      expect(writes.last['scope'], isNull);
      expect(writes.last['bindings'], isEmpty);
      expect(() => state.bind('getui', _binding()), throwsStateError);
    },
  );
  test(
    'unconfirmed native state fails, but does not poison later revocation',
    () async {
      var fail = true;
      messenger.setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'beginTenantCallControl') return 'controller-1';
        writes.add(Map<Object?, Object?>.from(call.arguments as Map));
        if (fail) {
          fail = false;
          return false;
        }
        return true;
      });
      await expectLater(
        state.replace(TenantCallScope(_scope())),
        throwsStateError,
      );
      await state.replace(null);
      expect(writes.last['scope'], isNull);
    },
  );
  test(
    'binding for a different identity cannot enter the native snapshot',
    () async {
      await state.replace(TenantCallScope(_scope()));
      final wrong = TenantPushBinding({
        ..._binding().context,
        'realmVersion': 4,
      });
      expect(() => state.bind('getui', wrong), throwsStateError);
      expect(writes.length, 1);
    },
  );
  test(
    'business credential refresh retains same-session binding only',
    () async {
      await state.replace(TenantCallScope(_scope()));
      await state.bind('getui', _binding());
      await state.replace(
        TenantCallScope({..._scope(), 'expiresAt': '2099-01-02T00:00:00Z'}),
      );
      expect(writes.last['bindings'], hasLength(1));
      await state.replace(TenantCallScope(_scope()), resetBindings: true);
      expect(writes.last['bindings'], isEmpty);
    },
  );
}
