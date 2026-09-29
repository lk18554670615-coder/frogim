/// SDK readiness is distinct from native VoIP token acceptance and platform
/// binding readiness. No credentials or SDK errors are retained here.
class NativePushStartup {
  bool getuiReady = false;

  Future<void> run({
    required bool getuiConfigured,
    required Future<void> Function() startGetui,
    required Future<void> Function() restoreGetui,
    required Future<void> Function() syncProviders,
    required bool Function() active,
    Duration settle = const Duration(milliseconds: 800),
    Duration sdkTimeout = const Duration(seconds: 5),
  }) async {
    if (!active()) return;
    if (getuiConfigured) {
      try {
        await startGetui().timeout(sdkTimeout);
        if (!active()) return;
        getuiReady = true;
      } catch (_) {
        getuiReady = false;
      }
    }
    if (settle > Duration.zero) await Future<void>.delayed(settle);
    if (!active()) return;
    if (getuiReady) {
      try {
        await restoreGetui().timeout(sdkTimeout);
      } catch (_) {
        // A delayed CID / launch payload does not disable another provider.
      }
    }
    if (active()) await syncProviders();
  }
}
