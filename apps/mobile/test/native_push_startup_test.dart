import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/native_push_startup.dart';
import 'package:getuiflut/getuiflut.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'Getui startup awaits native acknowledgement and propagates failure',
    () async {
      const channel = MethodChannel('getuiflut');
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      final gate = Completer<Object?>();
      messenger.setMockMethodCallHandler(channel, (_) => gate.future);
      var acknowledged = false;
      final started = Getuiflut().startSdk(
        appId: 'fixture-app',
        appKey: 'fixture-key',
        appSecret: 'fixture-secret',
      );
      final observed = started.then((_) => acknowledged = true);
      await Future<void>.delayed(Duration.zero);
      expect(acknowledged, isFalse);
      gate.complete(null);
      await observed;
      expect(acknowledged, isTrue);
      messenger.setMockMethodCallHandler(
        channel,
        (_) async => throw PlatformException(code: 'SDK_UNAVAILABLE'),
      );
      await expectLater(
        Getuiflut().initGetuiSdk,
        throwsA(isA<PlatformException>()),
      );
    },
  );
  test(
    'missing SDK completion does not indefinitely block independent sync',
    () async {
      final startup = NativePushStartup();
      var synced = false;
      await startup.run(
        getuiConfigured: true,
        startGetui: () => Completer<void>().future,
        restoreGetui: () async => fail('unconfirmed SDK must not be queried'),
        syncProviders: () async => synced = true,
        active: () => true,
        settle: Duration.zero,
        sdkTimeout: const Duration(milliseconds: 2),
      );
      expect(synced, isTrue);
      expect(startup.getuiReady, isFalse);
    },
  );
  for (final outcome in [
    'disabled',
    'start-failed',
    'restore-failed',
    'ready',
  ]) {
    test('independent provider sync survives Getui $outcome', () async {
      final startup = NativePushStartup();
      final calls = <String>[];
      await startup.run(
        getuiConfigured: outcome != 'disabled',
        startGetui: () async {
          calls.add('start');
          if (outcome == 'start-failed') throw MissingPluginException();
        },
        restoreGetui: () async {
          calls.add('restore');
          if (outcome == 'restore-failed') {
            throw PlatformException(code: 'not-ready');
          }
        },
        syncProviders: () async => calls.add('sync'),
        active: () => true,
        settle: Duration.zero,
      );
      expect(calls.last, 'sync');
      expect(calls.where((value) => value == 'sync'), hasLength(1));
      expect(
        startup.getuiReady,
        outcome == 'restore-failed' || outcome == 'ready',
      );
      if (outcome == 'disabled') expect(calls, ['sync']);
      if (outcome == 'start-failed') expect(calls, ['start', 'sync']);
    });
  }
  test(
    'dispose during initialization prevents restore and registration',
    () async {
      final startup = NativePushStartup();
      var active = true;
      await startup.run(
        getuiConfigured: true,
        startGetui: () async => active = false,
        restoreGetui: () async => fail('disposed restore'),
        syncProviders: () async => fail('disposed registration'),
        active: () => active,
        settle: Duration.zero,
      );
      expect(startup.getuiReady, isFalse);
    },
  );
}
