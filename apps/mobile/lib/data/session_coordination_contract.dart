/// Same-origin coordination carries no tokens, identities or business data.
/// The caller re-reads encrypted authority while holding [run].
abstract interface class SessionCoordination {
  bool get enabled;
  Stream<void> get changes;
  Future<T> run<T>(Future<T> Function() work);
  void changed();
  void close();
}
