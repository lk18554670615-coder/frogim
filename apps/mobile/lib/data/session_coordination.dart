import 'session_coordination_contract.dart';
import 'session_coordination_stub.dart'
    if (dart.library.js_interop) 'session_coordination_web.dart'
    as impl;
export 'session_coordination_contract.dart';

SessionCoordination createSessionCoordination(String namespace) =>
    impl.createSessionCoordination(namespace);

Future<T> withSecureKeyLock<T>(Future<T> Function() work) =>
    impl.withSecureKeyLock(work);

String? readTabSession(String key) => impl.readTabSession(key);
void writeTabSession(String key, String value) =>
    impl.writeTabSession(key, value);
void removeTabSession(String key) => impl.removeTabSession(key);
