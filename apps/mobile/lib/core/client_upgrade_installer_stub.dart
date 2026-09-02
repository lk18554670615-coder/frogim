import 'package:http/http.dart' as http;
import 'package:url_launcher/url_launcher.dart';

import 'client_upgrade.dart';
import 'client_upgrade_installer_contract.dart';

ClientUpgradeInstaller createClientUpgradeInstaller({http.Client? client}) =>
    const _ExternalClientUpgradeInstaller();

class _ExternalClientUpgradeInstaller implements ClientUpgradeInstaller {
  const _ExternalClientUpgradeInstaller();

  @override
  bool get downloadsInApp => false;

  @override
  Future<void> install(
    ClientUpgradeDecision decision, {
    required void Function(ClientUpgradeInstallProgress progress) onProgress,
  }) async {
    final uri = Uri.tryParse(decision.downloadUrl);
    if (uri == null || (uri.scheme != 'https' && uri.scheme != 'http')) {
      throw const ClientUpgradeInstallException('更新地址无效，请联系管理员');
    }
    final opened = await launchUrl(uri, mode: LaunchMode.platformDefault);
    if (!opened) {
      throw const ClientUpgradeInstallException('更新地址暂时无法打开，请稍后重试');
    }
  }
}
