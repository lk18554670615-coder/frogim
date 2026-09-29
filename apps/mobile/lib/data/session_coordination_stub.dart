import 'session_coordination_contract.dart';

SessionCoordination createSessionCoordination(String namespace) => _Local();

class _Local implements SessionCoordination {
  @override
  bool get enabled => false;
  @override
  Stream<void> get changes => const Stream.empty();
  @override
  Future<T> run<T>(Future<T> Function() work) => work();
  @override
  void changed() {}
  @override
  void close() {}
}

Future<T> withSecureKeyLock<T>(Future<T> Function() work) => work();
String? readTabSession(String key) =>
    throw UnsupportedError('Browser session storage');
void writeTabSession(String key, String value) =>
    throw UnsupportedError('Browser session storage');
void removeTabSession(String key) =>
    throw UnsupportedError('Browser session storage');
