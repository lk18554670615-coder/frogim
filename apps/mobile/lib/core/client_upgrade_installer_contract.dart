import 'client_upgrade.dart';

enum ClientUpgradeInstallPhase {
  downloading,
  preparing,
  requestingPermission,
  openingInstaller,
}

class ClientUpgradeInstallProgress {
  const ClientUpgradeInstallProgress({
    required this.phase,
    this.receivedBytes = 0,
    this.totalBytes,
  });

  final ClientUpgradeInstallPhase phase;
  final int receivedBytes;
  final int? totalBytes;

  double? get fraction {
    final total = totalBytes;
    if (total == null || total <= 0) return null;
    return (receivedBytes / total).clamp(0, 1);
  }
}

class ClientUpgradeInstallException implements Exception {
  const ClientUpgradeInstallException(this.message);

  final String message;

  @override
  String toString() => message;
}

abstract interface class ClientUpgradeInstaller {
  bool get downloadsInApp;

  Future<void> install(
    ClientUpgradeDecision decision, {
    required void Function(ClientUpgradeInstallProgress progress) onProgress,
  });
}
