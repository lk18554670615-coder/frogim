import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:linli_im/calls/system_call_service_native.dart';
import 'package:linli_im/core/runtime_endpoints.dart';
import 'package:linli_im/core/tenant_context.dart';
import 'package:linli_im/core/tenant_call_scope.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final owner = Object();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const plugin = MethodChannel('flutter_callkit_incoming');
  const events = MethodChannel('flutter_callkit_incoming_events');
  late NativeSystemCallService service;
  late List<MethodCall> methods;
  void attach(String tenant) => RuntimeEndpoints.attach(
    owner,
    TenantContext(
      tenantId: tenant,
      displayName: tenant,
      httpBaseUrl: Uri.parse('https://$tenant.example.test'),
      assignmentVersion: 1,
      configVersion: 1,
    ),
    localUserId: 'user',
    authVersion: 1,
    realmVersion: 1,
    expiresAt: DateTime.now().toUtc().add(const Duration(hours: 1)),
    callSessionId: 'a' * 43,
  );
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    methods = [];
    service = NativeSystemCallService();
    messenger.setMockMethodCallHandler(events, (_) async => null);
    attach('tenant-a');
  });
  tearDown(() async {
    await service.dispose();
    RuntimeEndpoints.clear(owner);
    messenger.setMockMethodCallHandler(plugin, null);
    messenger.setMockMethodCallHandler(events, null);
  });

  test(
    'foreign restored call with identical server ID cannot overwrite current mapping',
    () async {
      final current = TenantCallScope(RuntimeEndpoints.notificationContext);
      final foreign = TenantCallScope({
        ...current.data,
        'tenantId': 'tenant-b',
      });
      final currentId = systemCallIdFor('shared-call', scope: current);
      final foreignId = systemCallIdFor('shared-call', scope: foreign);
      messenger.setMockMethodCallHandler(plugin, (call) async {
        methods.add(call);
        if (call.method == 'activeCalls') {
          return [
            for (final scope in [current, foreign])
              {
                'id': systemCallIdFor('shared-call', scope: scope),
                'extra': {
                  'serverCallId': 'shared-call',
                  'tenantScope': scope.data,
                },
              },
          ];
        }
        return null;
      });
      await service.initialize();
      await service.setConnected('shared-call');
      await service.end('shared-call');
      expect(
        methods
            .where((value) => value.method == 'callConnected')
            .single
            .arguments,
        {'id': currentId},
      );
      expect(
        methods.where((value) => value.method == 'endCall').single.arguments,
        {'id': currentId},
      );
      await service.dismiss(foreignId);
      expect(
        methods.where((value) => value.method == 'endCall').last.arguments,
        {'id': foreignId},
      );
    },
  );

  test(
    'native presentation finishing after account change ends only its original UUID',
    () async {
      final scope = TenantCallScope(RuntimeEndpoints.notificationContext);
      final gate = Completer<void>();
      final started = Completer<void>();
      messenger.setMockMethodCallHandler(plugin, (call) async {
        methods.add(call);
        if (call.method == 'activeCalls') return [];
        if (call.method == 'showCallkitIncoming') {
          started.complete();
          await gate.future;
        }
        return null;
      });
      final session = CallSession(
        id: 'call-1',
        conversationId: 'conv-1',
        callerId: 'peer',
        calleeId: 'user',
        mediaType: CallMediaType.audio,
        status: 'invited',
        invitedAt: DateTime.now(),
        expiresAt: DateTime.now().add(const Duration(seconds: 30)),
      );
      final shown = service.showIncoming(
        session: session,
        callerName: 'test user',
      );
      await started.future;
      attach('tenant-b');
      gate.complete();
      expect(await shown, isFalse);
      final end = methods.where((value) => value.method == 'endCall').single;
      expect(end.arguments, {'id': systemCallIdFor('call-1', scope: scope)});
      await service.end('call-1');
      expect(methods.where((value) => value.method == 'endCall'), hasLength(1));
    },
  );

  test(
    'same-session renewal preserves controls for an established native ID',
    () async {
      final previous = TenantCallScope({
        ...RuntimeEndpoints.notificationContext,
        'expiresAt': '2020-01-01T00:00:00Z',
      });
      final id = systemCallIdFor('call-renewed', scope: previous);
      messenger.setMockMethodCallHandler(plugin, (call) async {
        methods.add(call);
        if (call.method == 'activeCalls') {
          return [
            {
              'id': id,
              'extra': {
                'serverCallId': 'call-renewed',
                'tenantScope': previous.data,
              },
            },
          ];
        }
        return null;
      });
      await service.initialize();
      await service.end('call-renewed');
      expect(methods.last.method, 'endCall');
      expect(methods.last.arguments, {'id': id});
    },
  );
}
