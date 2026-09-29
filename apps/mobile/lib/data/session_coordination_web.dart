import 'dart:async';
import 'dart:js_interop';
import 'package:web/web.dart' as web;
import 'session_coordination_contract.dart';

SessionCoordination createSessionCoordination(String namespace) =>
    _Browser('frogim.platform.credentials.v1.$namespace');

Future<T> _locked<T>(String name, Future<T> Function() work) async {
  final abort = web.AbortController();
  final timer = Timer(const Duration(seconds: 20), () => abort.abort());
  late T result;
  Object? failure;
  StackTrace? trace;
  Future<JSAny?> execute(web.Lock? lock) async {
    timer.cancel();
    if (lock == null) throw StateError('浏览器认证锁未确认');
    try {
      result = await work();
    } catch (error, stack) {
      failure = error;
      trace = stack;
    }
    return null;
  }

  try {
    await web.window.navigator.locks
        .request(
          name,
          web.LockOptions(mode: 'exclusive', signal: abort.signal),
          ((web.Lock? lock) => execute(lock).toJS).toJS,
        )
        .toDart;
  } catch (_) {
    throw StateError('浏览器认证协调暂不可用，请稍后重试或使用支持 Web Locks 的浏览器');
  } finally {
    timer.cancel();
  }
  if (failure != null) Error.throwWithStackTrace(failure!, trace!);
  return result;
}

class _Browser implements SessionCoordination {
  _Browser(this.name) {
    _storage = ((web.Event raw) {
      final event = raw as web.StorageEvent;
      if (event.key == name || event.key == null) _signal();
    }).toJS;
    _visible = ((web.Event _) {
      if (web.document.visibilityState == 'visible') _signal();
    }).toJS;
    web.window.addEventListener('storage', _storage);
    web.document.addEventListener('visibilitychange', _visible);
    // Storage notifications are advisory, not authority. A resumed tab and a
    // missed event both re-read under the lock. No credential crosses this bus.
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => _signal());
  }
  final String name;
  final _changes = StreamController<void>.broadcast();
  late final JSFunction _storage, _visible;
  Timer? _timer;
  bool _closed = false;
  @override
  bool get enabled => true;
  @override
  Stream<void> get changes => _changes.stream;
  void _signal() {
    if (!_closed) _changes.add(null);
  }

  @override
  Future<T> run<T>(Future<T> Function() work) => _locked(name, () async {
    if (_closed) throw StateError('认证协调已关闭');
    return work();
  });
  @override
  void changed() {
    if (_closed) return;
    try {
      web.window.localStorage.setItem(name, web.window.crypto.randomUUID());
    } catch (_) {}
  }

  @override
  void close() {
    if (_closed) return;
    _closed = true;
    _timer?.cancel();
    web.window.removeEventListener('storage', _storage);
    web.document.removeEventListener('visibilitychange', _visible);
    unawaited(_changes.close());
  }
}

Future<T> withSecureKeyLock<T>(Future<T> Function() work) =>
    _locked('frogim.secure-cache-key.v1', work);

String? readTabSession(String key) => web.window.sessionStorage.getItem(key);
void writeTabSession(String key, String value) =>
    web.window.sessionStorage.setItem(key, value);
void removeTabSession(String key) => web.window.sessionStorage.removeItem(key);
