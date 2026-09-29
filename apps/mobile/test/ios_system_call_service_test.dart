import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:linli_im/calls/system_call_identity.dart';
import 'package:linli_im/calls/system_call_service_ios.dart';
import 'package:linli_im/calls/system_call_service_contract.dart';
import 'package:linli_im/core/runtime_endpoints.dart';
import 'package:linli_im/core/tenant_context.dart';
import 'package:linli_im/core/tenant_call_scope.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final owner = Object();
  const bridge = MethodChannel('test/ios-calls');
  late IosSystemCallService service;
  late List<MethodCall> calls;
  final session = CallSession(
    id: 'same-call',
    conversationId: 'conv-1',
    callerId: 'peer',
    calleeId: 'me',
    mediaType: CallMediaType.audio,
    status: 'invited',
    invitedAt: DateTime.now(),
    expiresAt: DateTime.now().add(const Duration(minutes: 2)),
  );
  void attach(String tenant, {String generation = 'a'}) =>
      RuntimeEndpoints.attach(
        owner,
        TenantContext(
          tenantId: tenant,
          displayName: tenant,
          httpBaseUrl: Uri.parse('https://$tenant.example.test'),
          assignmentVersion: 1,
          configVersion: 1,
        ),
        localUserId: 'me',
        authVersion: 1,
        realmVersion: 1,
        expiresAt: DateTime.now().toUtc().add(const Duration(hours: 1)),
        callSessionId: generation * 43,
      );
  setUp(() {
    calls = [];
    attach('tenant-a');
    service = IosSystemCallService(channel: bridge, managed: true);
    messenger.setMockMethodCallHandler(bridge, (call) async {
      calls.add(call);
      if (call.method == 'drainLaunchActions') return [];
      if (call.method == 'voipToken') return 'fixture-only-voip-token';
      return true;
    });
  });
  tearDown(() async {
    await service.dispose();
    RuntimeEndpoints.clear(owner);
    messenger.setMockMethodCallHandler(bridge, null);
  });
  test(
    'presentation and all controls use exact scoped UUID and native bridge',
    () async {
      final scope = TenantCallScope(RuntimeEndpoints.notificationContext);
      final id = systemCallIdFor(session.id, scope: scope);
      expect(
        await service.showIncoming(
          session: session,
          callerName: 'public name',
          callerHandle: 'public handle',
        ),
        isTrue,
      );
      final shown =
          calls.singleWhere((c) => c.method == 'showIncoming').arguments as Map;
      expect(shown['id'], id);
      expect(shown['tenantScope'], scope.data);
      expect(shown['displayName'], 'public name');
      expect(shown['handle'], 'public handle');
      await service.setConnected(session.id);
      await service.setMuted(session.id, true);
      await service.end(session.id);
      expect(calls.last.arguments, {'id': id});
      expect(await service.voipPushToken(), 'fixture-only-voip-token');
    },
  );
  test(
    'identity change while native UI reports ends original UUID only',
    () async {
      final old = TenantCallScope(RuntimeEndpoints.notificationContext);
      final gate = Completer<void>();
      final started = Completer<void>();
      messenger.setMockMethodCallHandler(bridge, (call) async {
        calls.add(call);
        if (call.method == 'drainLaunchActions') return [];
        if (call.method == 'showIncoming') {
          started.complete();
          await gate.future;
        }
        return true;
      });
      final shown = service.showIncoming(session: session, callerName: 'test');
      await started.future;
      attach('tenant-b');
      gate.complete();
      expect(await shown, isFalse);
      expect(calls.last.method, 'endCall');
      expect(calls.last.arguments, {
        'id': systemCallIdFor(session.id, scope: old),
      });
    },
  );
  test(
    'old restored call cannot overwrite current same-call mapping',
    () async {
      final current = TenantCallScope(RuntimeEndpoints.notificationContext);
      final old = TenantCallScope({...current.data, 'callSessionId': 'b' * 43});
      messenger.setMockMethodCallHandler(bridge, (call) async {
        calls.add(call);
        if (call.method == 'drainLaunchActions') {
          return [
            for (final scope in [current, old])
              {
                'type': 'restore',
                'serverCallId': session.id,
                'systemCallId': systemCallIdFor(session.id, scope: scope),
                'tenantScope': scope.data,
              },
          ];
        }
        return true;
      });
      final actions = <SystemCallAction>[];
      final subscription = service.actions.listen(actions.add);
      addTearDown(subscription.cancel);
      await service.initialize();
      await Future<void>.delayed(Duration.zero);
      expect(
        actions,
        hasLength(2),
      ); // Controller, not event reception, authorizes cold-start actions.
      await service.setConnected(session.id);
      expect(calls.last.arguments, {
        'id': systemCallIdFor(session.id, scope: current),
      });
    },
  );
  test('no identity means no managed native call presentation', () async {
    RuntimeEndpoints.clear(owner);
    expect(
      await service.showIncoming(session: session, callerName: 'test'),
      isFalse,
    );
    expect(calls.where((c) => c.method == 'showIncoming'), isEmpty);
  });
  test('unconfirmed report is false and cleans exact native ID', () async {
    messenger.setMockMethodCallHandler(bridge, (call) async {
      calls.add(call);
      return call.method == 'drainLaunchActions' ? [] : false;
    });
    expect(
      await service.showIncoming(session: session, callerName: 'test'),
      isFalse,
    );
    expect(calls.last.method, 'endCall');
  });
  test(
    'same-session renewal keeps precise controls without replaying accept',
    () async {
      final current = TenantCallScope(RuntimeEndpoints.notificationContext);
      final previous = TenantCallScope({
        ...current.data,
        'expiresAt': '2020-01-01T00:00:00Z',
      });
      final id = systemCallIdFor(session.id, scope: previous);
      final actions = <SystemCallAction>[];
      final subscription = service.actions.listen(actions.add);
      addTearDown(subscription.cancel);
      messenger.setMockMethodCallHandler(bridge, (call) async {
        calls.add(call);
        if (call.method == 'drainLaunchActions') {
          return [
            {
              'type': 'accept',
              'serverCallId': session.id,
              'systemCallId': id,
              'tenantScope': previous.data,
            },
          ];
        }
        return true;
      });
      await service.initialize();
      await Future<void>.delayed(Duration.zero);
      expect(actions.single.tenantScope!.validNow, isFalse);
      await service.setConnected(session.id);
      await service.setMuted(session.id, true);
      await service.end(session.id);
      expect(
        calls.where(
          (c) => ['callConnected', 'muteCall', 'endCall'].contains(c.method),
        ),
        hasLength(3),
      );
      expect(calls.last.arguments, {'id': id});
    },
  );
  test('mute on-off-on actions are not permanently deduplicated', () async {
    final scope = TenantCallScope(RuntimeEndpoints.notificationContext);
    final actions = <SystemCallAction>[];
    final subscription = service.actions.listen(actions.add);
    addTearDown(subscription.cancel);
    await service.initialize();
    for (final muted in [true, false, true]) {
      await messenger.handlePlatformMessage(
        bridge.name,
        const StandardMethodCodec().encodeMethodCall(
          MethodCall('systemCallAction', {
            'type': 'mute',
            'serverCallId': session.id,
            'systemCallId': systemCallIdFor(session.id, scope: scope),
            'tenantScope': scope.data,
            'muted': muted,
          }),
        ),
        (_) {},
      );
    }
    await Future<void>.delayed(Duration.zero);
    expect(actions.map((a) => a.muted), [true, false, true]);
  });
  test(
    'native failure cleans exact UUID and missing token is recoverable',
    () async {
      messenger.setMockMethodCallHandler(bridge, (call) async {
        calls.add(call);
        if (call.method == 'drainLaunchActions') return [];
        if (call.method == 'endCall') return true;
        throw PlatformException(code: 'CALLS_UNAVAILABLE');
      });
      expect(
        await service.showIncoming(session: session, callerName: 'test'),
        isFalse,
      );
      expect(calls.last.method, 'endCall');
      expect(await service.voipPushToken(), isNull);
    },
  );
}
