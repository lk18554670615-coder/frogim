@TestOn('browser')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/data/session_coordination.dart';
// Test runner does not register plugins as a built Flutter app does. Exercise
// the already locked Web implementation, not a mock shared-preference cache.
// ignore: depend_on_referenced_packages
import 'package:shared_preferences_web/shared_preferences_web.dart';
import 'package:web/web.dart' as web;

// Keep this test at test/ root: Flutter's Windows browser-test selector does
// not escape backslashes in nested suite names. This is a real browser context,
// not a mocked coordinator.
class LockFrame {
  LockFrame(String namespace) {
    listener = ((web.MessageEvent event) {
      if (event.source != frame.contentWindow) return;
      events.add(event.data.dartify() as String);
    }).toJS;
    web.window.addEventListener('message', listener);
    final name = jsonEncode('frogim.platform.credentials.v1.$namespace');
    frame.srcdoc =
        '''<!doctype html><script>
      let release;
      addEventListener('message', async event => {
        if (event.data === 'take') {
          parent.postMessage('queued', '*');
          await navigator.locks.request($name, async () => {
            await new Promise(resolve => {
              release = resolve;
              parent.postMessage('acquired', '*');
            });
          });
          parent.postMessage('released', '*');
        } else if (event.data === 'release') { release(); }
        else if (event.data === 'signal') {
          localStorage.setItem($name, crypto.randomUUID());
        }
      });
      parent.postMessage('ready', '*');
    </script>'''
            .toJS;
  }
  final frame = web.HTMLIFrameElement();
  final events = StreamController<String>.broadcast();
  late final JSFunction listener;
  Future<void> start() async {
    final ready = wait('ready');
    web.document.body!.append(frame);
    await ready;
  }

  Future<void> wait(String expected) => events.stream
      .firstWhere((event) => event == expected)
      .timeout(const Duration(seconds: 5));
  void send(String command) =>
      frame.contentWindow!.postMessage(command.toJS, '*'.toJS);
  void close() {
    frame.remove();
    web.window.removeEventListener('message', listener);
    unawaited(events.close());
  }
}

class TestKeystore extends FlutterSecureStorage {
  final values = <String, String>{};
  int writes = 0;
  bool failRead = false;
  @override
  Future<String?> read({
    required String key,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    if (failRead) throw StateError('synthetic keystore unavailable');
    await Future<void>.delayed(const Duration(milliseconds: 10));
    return values[key];
  }

  @override
  Future<void> write({
    required String key,
    required String? value,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    writes++;
    values[key] = value!;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  SharedPreferencesPlugin.registerWith(null);
  String namespace() => 'test.${web.window.crypto.randomUUID()}';

  test('real Web Locks serialize Dart and another browsing context', () async {
    final ns = namespace();
    final coordinator = createSessionCoordination(ns), frame = LockFrame(ns);
    addTearDown(coordinator.close);
    addTearDown(frame.close);
    await frame.start();
    final entered = Completer<void>(), release = Completer<void>();
    final first = coordinator.run(() async {
      entered.complete();
      await release.future;
    });
    await entered.future;
    var acquired = false;
    final acquiring = frame.wait('acquired').then((_) => acquired = true);
    final queued = frame.wait('queued');
    frame.send('take');
    await queued;
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(acquired, false);
    release.complete();
    await first;
    await acquiring;
    var thirdEntered = false;
    final third = coordinator.run(() async => thirdEntered = true);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(thirdEntered, false);
    frame.send('release');
    await third.timeout(const Duration(seconds: 5));
    expect(thirdEntered, true);
  });

  test(
    'destroying a context releases its lock and work errors release Dart lock',
    () async {
      final ns = namespace();
      final coordinator = createSessionCoordination(ns), frame = LockFrame(ns);
      addTearDown(coordinator.close);
      await frame.start();
      final acquired = frame.wait('acquired');
      frame.send('take');
      await acquired;
      final next = coordinator.run(() async => 'released');
      frame.close();
      expect(await next.timeout(const Duration(seconds: 5)), 'released');
      await expectLater(
        coordinator.run(() async => throw StateError('fixture')),
        throwsStateError,
      );
      expect(await coordinator.run(() async => 42), 42);
    },
  );

  test('storage notification carries only a random marker', () async {
    final ns = namespace();
    final coordinator = createSessionCoordination(ns), frame = LockFrame(ns);
    addTearDown(coordinator.close);
    addTearDown(frame.close);
    final key = 'frogim.platform.credentials.v1.$ns';
    addTearDown(() => web.window.localStorage.removeItem(key));
    await frame.start();
    final changed = coordinator.changes.first;
    frame.send('signal');
    await changed.timeout(const Duration(seconds: 2));
    expect(
      web.window.localStorage.getItem(key),
      matches(RegExp(r'^[0-9a-f-]{36}$')),
    );
  });

  test(
    'secure key creation is serialized and business credentials are tab-local encrypted',
    () async {
      final ns = namespace(), keystore = TestKeystore();
      final a = SecureLocalStore(
        namespace: ns,
        secureStorage: keystore,
        tabLocalSession: true,
      );
      final b = SecureLocalStore(
        namespace: ns,
        secureStorage: keystore,
        tabLocalSession: true,
      );
      addTearDown(a.clearAccountData);
      await Future.wait([
        a.writeJson('session', {'token': 'synthetic-only'}),
        b.writeJson('fixture', {'count': 1}),
      ]);
      expect(keystore.writes, 1);
      expect(await b.readJson('session'), {'token': 'synthetic-only'});
      final key =
          'nexachat.tenant.secure.v1.${Uri.encodeComponent(ns)}.session';
      final encrypted = web.window.sessionStorage.getItem(key)!;
      expect(encrypted, isNot(contains('synthetic-only')));
      expect(web.window.localStorage.getItem('flutter.$key'), isNull);
      final prefix =
          'flutter.nexachat.tenant.secure.v1.${Uri.encodeComponent(ns)}.';
      await b.writeJson('external', {'count': 2});
      web.window.localStorage.setItem(
        '${prefix}fixture',
        web.window.localStorage.getItem('${prefix}external')!,
      );
      expect(await a.readJson('fixture'), {'count': 2});
      // A fresh top-level window may initially clone sessionStorage. Subsequent
      // changes must be isolated; an iframe would NOT establish this property.
      final popup = web.window.open('about:blank', 'storage-${namespace()}');
      expect(popup, isNotNull, reason: 'Test browser must permit a test popup');
      if (popup == null) return;
      addTearDown(() => popup.close());
      popup.sessionStorage.setItem(key, 'other-tab-encrypted-placeholder');
      expect(web.window.sessionStorage.getItem(key), encrypted);
      await a.remove('session');
      expect(
        popup.sessionStorage.getItem(key),
        'other-tab-encrypted-placeholder',
      );
    },
  );
  test(
    'temporary Web keystore failure never replaces the global key or deletes ciphertext',
    () async {
      final ns = namespace(), keystore = TestKeystore();
      final first = SecureLocalStore(namespace: ns, secureStorage: keystore);
      addTearDown(first.clearAccountData);
      await first.writeJson('fixture', {'count': 1});
      final key =
          'flutter.nexachat.tenant.secure.v1.${Uri.encodeComponent(ns)}.fixture';
      final encrypted = web.window.localStorage.getItem(key);
      final next = SecureLocalStore(namespace: ns, secureStorage: keystore);
      keystore.failRead = true;
      await expectLater(next.readJson('fixture'), throwsStateError);
      await expectLater(
        next.writeJson('fixture', {'count': 2}),
        throwsStateError,
      );
      expect(keystore.writes, 1);
      expect(web.window.localStorage.getItem(key), encrypted);
      keystore.failRead = false;
      expect(await next.readJson('fixture'), {'count': 1});
    },
  );
}
